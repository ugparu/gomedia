package webrtc

import (
	"encoding/base64"
	"errors"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/utils/buffer"
	"github.com/ugparu/gomedia/utils/lifecycle"
	"github.com/ugparu/gomedia/utils/logger"
)

type Option func(*webRTCWriter)

func WithLogger(l logger.Logger) Option {
	return func(w *webRTCWriter) { w.log = l }
}

// WithSignalingHandler overrides the default data channel signaling protocol.
func WithSignalingHandler(h SignalingHandler) Option {
	return func(w *webRTCWriter) { w.signaling = h }
}

type webRTCWriter struct {
	lifecycle.AsyncManager[*webRTCWriter]
	log              logger.Logger
	streams          *sortedStreams
	sources          []string
	peersChan        chan *gomedia.WebRTCPeer
	changePeersChan  chan *peerURL
	connectPeersChan chan *peerTrack
	closePeersChan   chan *peerTrack
	inpPktCh         chan gomedia.Packet
	rmSrcCh          chan string
	addSrcCh         chan string
	name             string
	signaling        SignalingHandler
}

func New(chanSize int, targetDuration time.Duration, opts ...Option) gomedia.WebRTCStreamer {
	wr := &webRTCWriter{
		AsyncManager: nil,
		log:          logger.Default,
		streams: &sortedStreams{
			log:            logger.Default,
			sortedURLs:     []string{},
			streams:        map[string]*stream{},
			pendingPeers:   map[*peerTrack]bool{},
			targetDuration: targetDuration,
			signaling:      nil, // set after options
		},
		sources:          []string{},
		peersChan:        make(chan *gomedia.WebRTCPeer, chanSize),
		changePeersChan:  make(chan *peerURL, chanSize),
		connectPeersChan: make(chan *peerTrack, chanSize),
		closePeersChan:   make(chan *peerTrack, chanSize),
		inpPktCh:         make(chan gomedia.Packet, chanSize),
		rmSrcCh:          make(chan string, chanSize),
		addSrcCh:         make(chan string, chanSize),
		name:             "WEBRTC_WRITER",
		signaling:        &DefaultSignalingHandler{},
	}
	for _, o := range opts {
		o(wr)
	}
	wr.streams.log = wr.log
	wr.streams.signaling = wr.signaling
	wr.AsyncManager = lifecycle.NewFailSafeAsyncManager(wr, wr.log)
	return wr
}

func (element *webRTCWriter) Write() {
	startFunc := func(*webRTCWriter) error {
		return nil
	}
	_ = element.Start(startFunc)
}

// Step multiplexes source add/remove, peer lifecycle (connect, rebind, close),
// and packet write events onto a single goroutine so streams state is never
// touched concurrently.
func (element *webRTCWriter) Step(stopCh <-chan struct{}) (err error) {
	select {
	case <-stopCh:
		return &lifecycle.BreakError{}
	case peer := <-element.peersChan:
		if peer.TargetURL == "" {
			peer.Err = errors.New("target URL is empty")
			close(peer.Done)
			return peer.Err
		}

		targetStream, ok := element.streams.streams[peer.TargetURL]
		if !ok || targetStream == nil || targetStream.codecPar.VideoCodecParameters == nil {
			peer.Err = errors.New("target stream not found: " + peer.TargetURL)
			close(peer.Done)
			return peer.Err
		}

		codecType := targetStream.codecPar.VideoCodecParameters.Type()
		go element.addConnection(peer, peer.TargetURL, codecType)
	case peerURL := <-element.changePeersChan:
		if !element.streams.Exists(peerURL.URL) {
			respBytes, marshalErr := element.signaling.BuildErrorResponse(peerURL.Token)
			if marshalErr != nil {
				return marshalErr
			}
			if peerURL.DataChannel != nil {
				if sendErr := peerURL.DataChannel.Send(respBytes); sendErr != nil {
					element.log.Errorf(element, "Can not send response to data channel: %v", sendErr)
				}
			}
			return nil
		}
		return element.streams.Move(peerURL)
	case peerTrack := <-element.connectPeersChan:
		if err = element.streams.Insert(peerTrack); err != nil {
			err = errors.Join(err, element.removePeer(peerTrack))
			return err
		}
		element.sendAvailableStreamsToPeer(peerTrack)
	case peer := <-element.closePeersChan:
		if err = element.removePeer(peer); err != nil {
			return err
		}
	case rmURL := <-element.rmSrcCh:
		element.removeSource(rmURL)
		element.log.Infof(element, "Sending setAvailableStreams after removing source %s", rmURL)
		element.sendAvailableStreams()
	case addURL := <-element.addSrcCh:
		element.addSource(addURL)
	case inpPkt := <-element.inpPktCh:
		switch pkt := inpPkt.(type) {
		case gomedia.VideoPacket:
			if err = element.checkCodecParameters(inpPkt.SourceID(), pkt.CodecParameters()); err != nil {
				inpPkt.Release()
				return
			}
		case gomedia.AudioPacket:
			if err = element.checkCodecParameters(inpPkt.SourceID(), pkt.CodecParameters()); err != nil {
				inpPkt.Release()
				return
			}
		}
		if err = element.streams.writePacket(inpPkt); err != nil {
			return err
		}
		for _, peer := range element.streams.failedPeers {
			if rmErr := element.removePeer(peer); rmErr != nil {
				element.log.Errorf(element, "Failed to remove broken peer: %v", rmErr)
			}
		}
		element.streams.failedPeers = element.streams.failedPeers[:0]
	}

	return nil
}

func (element *webRTCWriter) hasSource(addr string) bool {
	return slices.Contains(element.sources, addr)
}

// addSource registers a source URL. The underlying stream is not created
// until the first packet arrives so codec parameters are known.
func (element *webRTCWriter) addSource(addr string) {
	if element.hasSource(addr) {
		element.log.Infof(element, "Source %s already exists, skipping", addr)
		return
	}

	element.sources = append(element.sources, addr)
	element.log.Infof(element, "Added new source %s", addr)

	if len(element.sources) == 1 {
		parsedURL, err := url.Parse(addr)
		if err == nil {
			element.name = "WEBRTC_WRITER " + parsedURL.Hostname()
		}
	}
}

func (element *webRTCWriter) removeSource(addr string) {
	for i, src := range element.sources {
		if src == addr {
			element.sources = append(element.sources[:i], element.sources[i+1:]...)
			break
		}
	}
	element.streams.Remove(addr)
}

// checkCodecParameters lazily creates the stream on first packet and tears
// down peers whose negotiated codec no longer matches after a parameter
// change (e.g. SPS/PPS updates incompatible with the original offer).
func (element *webRTCWriter) checkCodecParameters(addr string, codecPar gomedia.CodecParameters) (err error) {
	if !element.hasSource(addr) {
		return
	}

	if !element.streams.Exists(addr) {
		_ = element.streams.Add(addr, codecPar)
		element.sendAvailableStreams()
		return nil
	}

	peersToRemove, changed := element.streams.Update(addr, codecPar)
	if !changed {
		return
	}

	for _, peer := range peersToRemove {
		if rmErr := element.removePeer(peer); rmErr != nil {
			element.log.Errorf(element, "Failed to remove peer after codec change: %v", rmErr)
		}
	}

	element.sendAvailableStreams()

	return nil
}

func (element *webRTCWriter) buildAvailableStreamsMessage() ([]byte, error) {
	resolutions := make([]gomedia.Resolution, 0, len(element.streams.sortedURLs))
	for _, url := range element.streams.sortedURLs {
		resolutions = append(resolutions, gomedia.Resolution{
			URL:    url,
			Width:  int(element.streams.streams[url].codecPar.Width()),  //nolint:gosec
			Height: int(element.streams.streams[url].codecPar.Height()), //nolint:gosec
			Codec:  element.streams.streams[url].codecPar.VideoCodecParameters.Type().String(),
		})
	}
	return element.signaling.BuildAvailableStreams(resolutions)
}

func (element *webRTCWriter) sendAvailableStreamsToPeer(peer *peerTrack) {
	if peer.DataChannel == nil || len(element.streams.sortedURLs) == 0 {
		return
	}

	bytes, err := element.buildAvailableStreamsMessage()
	if err != nil {
		element.log.Errorf(element, "Failed to marshal setAvailableStreams: %v", err)
		return
	}

	element.log.Infof(element, "Sending message to new peer %s", bytes)
	if err = peer.DataChannel.Send(bytes); err != nil {
		element.log.Errorf(element, "Failed to send setAvailableStreams to new peer: %v", err)
	}
}

// sendAvailableStreams broadcasts the current stream list to every connected
// and pending peer; call after any change to streams so peers can re-subscribe.
func (element *webRTCWriter) sendAvailableStreams() {
	bytes, err := element.buildAvailableStreamsMessage()
	if err != nil {
		element.log.Errorf(element, "Failed to marshal setAvailableStreams: %v", err)
		return
	}

	for _, stream := range element.streams.streams {
		for peer := range stream.tracks {
			element.log.Infof(element, "Sending message %s", bytes)
			if peer.DataChannel != nil {
				if err = peer.DataChannel.Send(bytes); err != nil {
					element.log.Errorf(element, "Failed to send setAvailableStreams: %v", err)
				}
			}
		}
	}

	for peer := range element.streams.pendingPeers {
		element.log.Infof(element, "Sending message to pending peer %s", bytes)
		if peer.DataChannel != nil {
			if err = peer.DataChannel.Send(bytes); err != nil {
				element.log.Errorf(element, "Failed to send setAvailableStreams to pending peer: %v", err)
			}
		}
	}
}

// extractFmtpLineFromSDP finds the fmtp parameters (e.g. profile-level-id,
// sprop-parameter-sets) for the offer's H.264/H.265 payload type so the local
// track advertises a compatible format in the answer.
func extractFmtpLineFromSDP(sdp string, codecType gomedia.CodecType) string {
	lines := strings.Split(sdp, "\n")
	var payloadType string
	var targetCodec string

	switch codecType {
	case gomedia.H264:
		targetCodec = "H264"
	case gomedia.H265:
		targetCodec = "H265"
	default:
		return ""
	}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "a=rtpmap:") {
			parts := strings.Split(line, " ")
			if len(parts) >= 2 {
				ptPart := strings.Split(parts[0], ":")
				if len(ptPart) >= 2 {
					if strings.HasPrefix(parts[1], targetCodec+"/") {
						payloadType = ptPart[1]
						break
					}
				}
			}
		}
	}

	if payloadType == "" {
		return ""
	}

	fmtpPrefix := "a=fmtp:" + payloadType + " "
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, fmtpPrefix); ok {
			return rest
		}
	}

	return ""
}

func (element *webRTCWriter) addConnection(inpPeer *gomedia.WebRTCPeer, targetURL string, codecType gomedia.CodecType) (err error) {
	defer close(inpPeer.Done)

	sdp64 := inpPeer.SDP

	sdpB, err := base64.StdEncoding.DecodeString(sdp64)
	if err != nil {
		inpPeer.Err = err
		return inpPeer.Err
	}

	offer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  string(sdpB),
	}
	peer, err := api.NewPeerConnection(Conf)
	if err != nil {
		inpPeer.Err = err
		return inpPeer.Err
	}
	defer func() {
		if err != nil {
			_ = peer.Close()
		}
	}()

	pt := new(peerTrack)
	pt.PeerConnection = peer
	pt.log = element.log
	pt.targetURL = targetURL
	pt.delay = max(time.Second/2, time.Second*time.Duration(inpPeer.Delay))
	pt.vflush = make(chan struct{})
	pt.aflush = make(chan struct{})
	pt.done = make(chan struct{})
	const bufSize = 500
	pt.vChan = make(chan gomedia.VideoPacket, bufSize)
	pt.aChan = make(chan gomedia.AudioPacket, bufSize)

	var once sync.Once
	peer.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		element.log.Infof(element, "Connection state has changed to %s", connectionState.String())

		if connectionState == webrtc.ICEConnectionStateDisconnected ||
			connectionState == webrtc.ICEConnectionStateClosed || connectionState == webrtc.ICEConnectionStateFailed {
			once.Do(func() {
				element.closePeersChan <- pt
			})
		}
	})

	peer.OnDataChannel(func(d *webrtc.DataChannel) {
		d.OnOpen(func() {
			pt.DataChannel = d
			element.log.Infof(element, "Data channel '%s'-'%d' opened", d.Label(), d.ID())

			element.connectPeersChan <- pt
		})

		d.OnMessage(func(msg webrtc.DataChannelMessage) {
			element.log.Infof(element, "Message from data channel '%s': '%s'", d.Label(), string(msg.Data))

			token, targetURL, parseErr := element.signaling.ParseMessage(msg.Data)
			if parseErr != nil {
				element.log.Errorf(element, "Can not process data channel msg: %v", parseErr)
				return
			}

			element.changePeersChan <- &peerURL{
				peerTrack: pt,
				Token:     token,
				URL:       targetURL,
			}
		})
	})

	mimeType := webrtc.MimeTypeH264
	if codecType == gomedia.H265 {
		mimeType = webrtc.MimeTypeH265
	}

	vtrack, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
		MimeType:     mimeType,
		ClockRate:    90000, //nolint:mnd // 90k
		Channels:     0,
		SDPFmtpLine:  extractFmtpLineFromSDP(offer.SDP, codecType),
		RTCPFeedback: []webrtc.RTCPFeedback{},
	}, "video", "pion-video")
	if err != nil {
		inpPeer.Err = err
		return inpPeer.Err
	}
	pt.vt = vtrack

	vRTPSender, err := peer.AddTrack(vtrack)
	if err != nil {
		inpPeer.Err = err
		return inpPeer.Err
	}
	go dropRTCP(vRTPSender)

	atrack, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
		MimeType:     webrtc.MimeTypePCMA,
		ClockRate:    8000,
		Channels:     1,
		SDPFmtpLine:  "",
		RTCPFeedback: []webrtc.RTCPFeedback{},
	}, "audio", "pion-audio")
	if err != nil {
		inpPeer.Err = err
		return inpPeer.Err
	}
	pt.at = atrack

	aRTPSender, err := peer.AddTrack(atrack)
	if err != nil {
		inpPeer.Err = err
		return inpPeer.Err
	}
	go dropRTCP(aRTPSender)

	if err = peer.SetRemoteDescription(offer); err != nil {
		inpPeer.Err = err
		return inpPeer.Err
	}

	answer, err := peer.CreateAnswer(nil)
	if err != nil {
		inpPeer.Err = err
		return inpPeer.Err
	}
	gatherCompletePromise := webrtc.GatheringCompletePromise(peer)

	if err = peer.SetLocalDescription(answer); err != nil {
		inpPeer.Err = err
		return inpPeer.Err
	}

	pt.aBuf = buffer.Get(0)
	pt.vBuf = buffer.Get(0)

	<-gatherCompletePromise

	inpPeer.SDP = base64.StdEncoding.EncodeToString([]byte(peer.LocalDescription().SDP))

	go writeVideoPacketsToPeer(pt, pt.vflush, pt.vChan, pt.vt, pt.vBuf, pt.delay)
	go writeAudioPacketsToPeer(pt, pt.aflush, pt.aChan, pt.at, pt.aBuf, pt.delay)

	return nil
}

// removePeer is idempotent: it detects prior removal via peer.done before
// closing the PeerConnection and DataChannel, then drains v/aChan so cloned
// packets don't leak their ring-buffer slots.
func (element *webRTCWriter) removePeer(peer *peerTrack) (err error) {
	select {
	case <-peer.done:
		return nil
	default:
	}

	for _, peers := range element.streams.streams {
		delete(peers.tracks, peer)
		delete(peers.toAdd, peer)
	}
	delete(element.streams.pendingPeers, peer)

	senders := peer.PeerConnection.GetSenders()
	for _, stream := range senders {
		_ = peer.RemoveTrack(stream)
	}
	if peer.DataChannel != nil {
		_ = peer.DataChannel.Close()
	}
	_ = peer.PeerConnection.Close()

	close(peer.done)

	for {
		select {
		case pkt := <-peer.vChan:
			pkt.Release()
		case pkt := <-peer.aChan:
			pkt.Release()
		default:
			return nil
		}
	}
}

func (element *webRTCWriter) Release() { //nolint: revive
	close(element.inpPktCh)
	for pkt := range element.inpPktCh {
		if pkt != nil {
			pkt.Release()
		}
	}
	for _, peers := range element.streams.streams {
		for peer := range peers.tracks {
			if err := element.removePeer(peer); err != nil {
				element.log.Errorf(element, "%v", err)
			}
		}
	}
	for peer := range element.streams.pendingPeers {
		if err := element.removePeer(peer); err != nil {
			element.log.Errorf(element, "%v", err)
		}
	}
	for _, str := range element.streams.streams {
		str.buffer.Close()
	}
}

func (element *webRTCWriter) SortedResolutions() *gomedia.WebRTCCodec {
	codec := &gomedia.WebRTCCodec{HasAudio: false, Resolutions: make([]gomedia.Resolution, 0, len(element.streams.sortedURLs))}

	for _, url := range element.streams.sortedURLs {
		if element.streams.streams[url].codecPar.AudioCodecParameters != nil {
			codec.HasAudio = true
		}
		codec.Resolutions = append(codec.Resolutions, gomedia.Resolution{
			URL:    url,
			Width:  int(element.streams.streams[url].codecPar.Width()),  //nolint:gosec
			Height: int(element.streams.streams[url].codecPar.Height()), //nolint:gosec
			Codec:  element.streams.streams[url].codecPar.VideoCodecParameters.Type().String(),
		})
	}

	return codec
}

func (element *webRTCWriter) Packets() chan<- gomedia.Packet {
	return element.inpPktCh
}

func (element *webRTCWriter) Peers() chan<- *gomedia.WebRTCPeer {
	return element.peersChan
}

func (element *webRTCWriter) RemoveSource() chan<- string {
	return element.rmSrcCh
}

func (element *webRTCWriter) AddSource() chan<- string {
	return element.addSrcCh
}

func (element *webRTCWriter) String() string {
	return element.name
}

func dropRTCP(rs *webrtc.RTPSender) {
	const bufSize = 999
	rtcpBuf := make([]byte, bufSize)
	for {
		if _, _, rtcpErr := rs.Read(rtcpBuf); rtcpErr != nil {
			return
		}
	}
}
