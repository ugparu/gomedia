package webrtc

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/utils/buffer"
	"github.com/ugparu/gomedia/utils/lifecycle"
	"github.com/ugparu/gomedia/utils/logger"
)

type webRTCWriter struct {
	lifecycle.AsyncManager[*webRTCWriter]
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
}

// New creates a new Streamer with the given channel size.
// It initializes an innerWriter and embeds it into an outerWebRTC, providing synchronization and lifecycle management.
func New(chanSize int, targetDuration time.Duration) gomedia.WebRTCStreamer {
	wr := &webRTCWriter{
		AsyncManager: nil,
		streams: &sortedStreams{
			sortedURLs:     []string{},
			streams:        map[string]*stream{},
			pendingPeers:   map[*peerTrack]bool{},
			targetDuration: targetDuration,
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
	}
	wr.AsyncManager = lifecycle.NewFailSafeAsyncManager(wr)
	return wr
}

// Start is a method of innerWriter that starts the innerWriter.
// It satisfies the SafeStarter interface from goutils/lifecycle package.
func (element *webRTCWriter) Write() {
	startFunc := func(*webRTCWriter) error {
		return nil
	}
	_ = element.Start(startFunc)
}

// Step is a method of innerWriter that performs a single step of processing.
// It handles various channels to update codec parameters, write packets, manage peers, and close peers.
func (element *webRTCWriter) Step(stopCh <-chan struct{}) (err error) {
	select {
	case <-stopCh:
		return &lifecycle.BreakError{}
	case peer := <-element.peersChan:
		// Validate that target URL is provided
		if peer.TargetURL == "" {
			peer.Err = errors.New("target URL is empty")
			close(peer.Done)
			return peer.Err
		}

		// Validate that target stream exists
		targetStream, ok := element.streams.streams[peer.TargetURL]
		if !ok || targetStream == nil || targetStream.codecPar.VideoCodecParameters == nil {
			peer.Err = errors.New("target stream not found: " + peer.TargetURL)
			close(peer.Done)
			return peer.Err
		}

		codecType := targetStream.codecPar.VideoCodecParameters.Type()
		go element.addConnection(peer, peer.TargetURL, codecType)
	case peerURL := <-element.changePeersChan:
		return element.streams.Move(peerURL)
	case peerTrack := <-element.connectPeersChan:
		if err = element.streams.Insert(peerTrack); err != nil {
			err = errors.Join(err, element.removePeer(peerTrack))
			return err
		}
	case peer := <-element.closePeersChan:
		if err = element.removePeer(peer); err != nil {
			return err
		}
	case rmURL := <-element.rmSrcCh:
		element.removeSource(rmURL)
		logger.Infof(element, "Sending setAvailableStreams after removing source %s", rmURL)
		element.sendAvailableStreams()
	case addURL := <-element.addSrcCh:
		element.addSource(addURL)
	case inpPkt := <-element.inpPktCh:
		switch pkt := inpPkt.(type) {
		case gomedia.VideoPacket:
			if err = element.checkCodecParameters(inpPkt.URL(), pkt.CodecParameters()); err != nil {
				inpPkt.Close()
				return
			}
		case gomedia.AudioPacket:
			if err = element.checkCodecParameters(inpPkt.URL(), pkt.CodecParameters()); err != nil {
				inpPkt.Close()
				return
			}
		}
		if err = element.streams.writePacket(inpPkt); err != nil {
			return err
		}
	}

	return nil
}

// hasSource checks if a source URL is registered in the sources slice.
func (element *webRTCWriter) hasSource(addr string) bool {
	for _, src := range element.sources {
		if src == addr {
			return true
		}
	}
	return false
}

// addSource adds a new source URL to the sources slice.
// The stream will be created when codec parameters are received.
func (element *webRTCWriter) addSource(addr string) {
	if element.hasSource(addr) {
		logger.Infof(element, "Source %s already exists, skipping", addr)
		return
	}

	element.sources = append(element.sources, addr)
	logger.Infof(element, "Added new source %s", addr)

	parsedURL, err := url.Parse(addr)
	if err == nil {
		element.name = "WEBRTC_WRITER " + parsedURL.Hostname()
	}
}

// removeSource removes a source URL from the sources slice and streams.
func (element *webRTCWriter) removeSource(addr string) {
	// Remove from sources slice
	for i, src := range element.sources {
		if src == addr {
			element.sources = append(element.sources[:i], element.sources[i+1:]...)
			break
		}
	}

	// Remove from streams if it exists
	element.streams.Remove(addr)
}

// checkCodecParameters updates the codec parameters based on the provided map.
// It manages stream sizes, updates codec parameters, and sends messages to inform peers about available streams.
// If the stream doesn't exist yet but the source is registered, creates the stream with the codec parameters.
func (element *webRTCWriter) checkCodecParameters(addr string, codecPar gomedia.CodecParameters) (err error) {
	// Only process sources that were registered via AddSource
	if !element.hasSource(addr) {
		return
	}

	// If stream doesn't exist yet, create it with the codec parameters
	if !element.streams.Exists(addr) {
		_ = element.streams.Add(addr, codecPar)
		element.sendAvailableStreams()

		parsedURL, err := url.Parse(addr)
		if err != nil {
			return err
		}
		element.name = "WEBRTC_WRITER " + parsedURL.Hostname()

		return nil
	}

	_, changed := element.streams.Update(addr, codecPar)
	if !changed {
		return
	}

	parsedURL, err := url.Parse(addr)
	if err != nil {
		return err
	}
	element.name = "WEBRTC_WRITER " + parsedURL.Hostname()

	element.sendAvailableStreams()

	return nil
}

// buildAvailableStreamsMessage builds the setAvailableStreams message with current resolutions.
func (element *webRTCWriter) buildAvailableStreamsMessage() ([]byte, error) {
	reqMsg := &codecReq{
		Token:   "",
		Command: "setAvailableStreams",
		Message: codec{
			Type:        "video",
			Resolutions: []resolution{},
		},
	}

	for _, url := range element.streams.sortedURLs {
		reqMsg.Message.Resolutions = append(reqMsg.Message.Resolutions, resolution{
			URL:    url,
			Width:  int(element.streams.streams[url].codecPar.Width()),  //nolint:gosec
			Height: int(element.streams.streams[url].codecPar.Height()), //nolint:gosec
		})
	}

	return json.Marshal(reqMsg)
}

// sendAvailableStreamsToPeer sends setAvailableStreams message to a single peer.
// This is called when a new peer connects to send them the current available streams.
func (element *webRTCWriter) sendAvailableStreamsToPeer(peer *peerTrack) {
	if peer.DataChannel == nil || len(element.streams.sortedURLs) == 0 {
		return
	}

	bytes, err := element.buildAvailableStreamsMessage()
	if err != nil {
		logger.Errorf(element, "Failed to marshal setAvailableStreams: %v", err)
		return
	}

	logger.Infof(element, "Sending message to new peer %s", bytes)
	if err = peer.DataChannel.Send(bytes); err != nil {
		logger.Errorf(element, "Failed to send setAvailableStreams to new peer: %v", err)
	}
}

// sendAvailableStreams sends setAvailableStreams message to all connected peers.
// This should be called whenever streams are added or removed to keep peers in sync.
func (element *webRTCWriter) sendAvailableStreams() {
	bytes, err := element.buildAvailableStreamsMessage()
	if err != nil {
		logger.Errorf(element, "Failed to marshal setAvailableStreams: %v", err)
		return
	}

	for _, stream := range element.streams.streams {
		for peer := range stream.tracks {
			logger.Infof(element, "Sending message %s", bytes)
			if peer.DataChannel != nil {
				if err = peer.DataChannel.Send(bytes); err != nil {
					logger.Errorf(element, "Failed to send setAvailableStreams: %v", err)
				}
			}
		}
	}

	// Also notify pending peers if any
	for peer := range element.streams.pendingPeers {
		logger.Infof(element, "Sending message to pending peer %s", bytes)
		if peer.DataChannel != nil {
			if err = peer.DataChannel.Send(bytes); err != nil {
				logger.Errorf(element, "Failed to send setAvailableStreams to pending peer: %v", err)
			}
		}
	}
}

// extractFmtpLineFromSDP parses the SDP to find the first fmtp line that matches the given codec type
func extractFmtpLineFromSDP(sdp string, codecType gomedia.CodecType) string {
	lines := strings.Split(sdp, "\n")
	var payloadType string
	var targetCodec string

	// Map codec type to SDP codec name
	switch codecType {
	case gomedia.H264:
		targetCodec = "H264"
	case gomedia.H265:
		targetCodec = "H265"
	default:
		return ""
	}

	// Find the payload type for the target codec
	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Look for rtpmap lines: a=rtpmap:96 H264/90000
		if strings.HasPrefix(line, "a=rtpmap:") {
			parts := strings.Split(line, " ")
			if len(parts) >= 2 {
				// Extract payload type (e.g., "96" from "a=rtpmap:96")
				ptPart := strings.Split(parts[0], ":")
				if len(ptPart) >= 2 {
					// Check if codec matches (e.g., "H264/90000")
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

	// Find the fmtp line for this payload type
	fmtpPrefix := "a=fmtp:" + payloadType + " "
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, fmtpPrefix) {
			// Return the fmtp parameters (everything after "a=fmtp:PT ")
			return strings.TrimPrefix(line, fmtpPrefix)
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

	pt := new(peerTrack)
	pt.PeerConnection = peer
	pt.targetURL = targetURL
	pt.delay = max(time.Second*11, time.Second*time.Duration(inpPeer.Delay))
	pt.vflush = make(chan struct{})
	pt.aflush = make(chan struct{})
	pt.done = make(chan struct{})
	const bufSize = 500
	pt.vChan = make(chan gomedia.VideoPacket, bufSize)
	pt.vBuf = buffer.Get(0)
	pt.aChan = make(chan gomedia.AudioPacket, bufSize)
	pt.aBuf = buffer.Get(0)

	var once sync.Once
	peer.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		logger.Infof(element, "Connection state has changed to %s", connectionState.String())

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
			logger.Infof(element, "Data channel '%s'-'%d' opened", d.Label(), d.ID())

			// Send available streams to the newly connected peer immediately
			element.sendAvailableStreamsToPeer(pt)

			element.connectPeersChan <- pt
		})

		d.OnMessage(func(msg webrtc.DataChannelMessage) {
			logger.Infof(element, "Message from data channel '%s': '%s'", d.Label(), string(msg.Data))

			req := &dataChanReq{
				Token:   "",
				Command: "",
				Message: "",
			}

			if err = json.Unmarshal(msg.Data, req); err != nil {
				logger.Errorf(element, "Can not process data channel msg: %v", err)
			}

			newPeerURL := &peerURL{
				peerTrack: pt,
				Token:     req.Token,
				URL:       req.Message,
			}

			if element.streams.Exists(newPeerURL.URL) {
				element.changePeersChan <- newPeerURL
				return
			}

			respMsg := &resp{
				Token:   newPeerURL.Token,
				Status:  http.StatusNotFound,
				Message: "Not Found",
			}
			var respBytes []byte
			respBytes, err = json.Marshal(respMsg)
			if err != nil {
				logger.Errorf(element, "Can not send response to data channel: %v", err)
				return
			}

			if newPeerURL.DataChannel != nil {
				if err = newPeerURL.DataChannel.Send(respBytes); err != nil {
					logger.Errorf(element, "Can not send response to data channel: %v", err)
					return
				}
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

	<-gatherCompletePromise

	inpPeer.SDP = base64.StdEncoding.EncodeToString([]byte(peer.LocalDescription().SDP))

	go writeVideoPacketsToPeer(pt, pt.vflush, pt.vChan, pt.vt, pt.vBuf, pt.delay)
	go writeAudioPacketsToPeer(pt, pt.aflush, pt.aChan, pt.at, pt.aBuf, pt.delay)

	return nil
}

// removePeer removes a peer from all existing and to-be-added peers, removes associated tracks,
// closes the DataChannel, and closes the PeerConnection.
// This function is idempotent - calling it multiple times is safe.
func (element *webRTCWriter) removePeer(peer *peerTrack) (err error) {
	// Check if already removed by testing if done channel is closed
	select {
	case <-peer.done:
		// Already closed, nothing to do
		return nil
	default:
	}

	for _, peers := range element.streams.streams {
		logger.Debug(element, "Removing peer track from stream")
		delete(peers.tracks, peer)
		delete(peers.toAdd, peer)
	}

	// Also remove from pendingPeers if present
	delete(element.streams.pendingPeers, peer)

	senders := peer.PeerConnection.GetSenders()
	logger.Debug(element, "Removing peer track from senders")
	for _, stream := range senders {
		_ = peer.RemoveTrack(stream)
	}
	if peer.DataChannel != nil {
		logger.Debug(element, "Closing data channel")
		_ = peer.DataChannel.Close()
	}
	logger.Debug(element, "Closing peer connection")
	_ = peer.PeerConnection.Close()

	peer.vBuf.Release()
	peer.aBuf.Release()

	logger.Debug(element, "Closing done channel")
	close(peer.done)

	// Drain remaining packets from channels to avoid leaking cloned packets
	for {
		select {
		case pkt := <-peer.vChan:
			pkt.Close()
		case pkt := <-peer.aChan:
			pkt.Close()
		default:
			return nil
		}
	}
}

// Close closes the innerWriter by closing the input packet channel and removing all existing peers.
// It calls the removePeer method for each existing peer.
func (element *webRTCWriter) Close_() { //nolint: revive
	close(element.inpPktCh)
	for _, peers := range element.streams.streams {
		for peer := range peers.tracks {
			if err := element.removePeer(peer); err != nil {
				logger.Errorf(element, "%v", err)
			}
		}
	}
	for peer := range element.streams.pendingPeers {
		if err := element.removePeer(peer); err != nil {
			logger.Errorf(element, "%v", err)
		}
	}
	// Close stream buffers to release remaining packets
	for _, str := range element.streams.streams {
		str.buffer.Close()
	}
}

// Parameters returns the sorted URLs and codec parameters map of the innerWriter.
// It returns nil for both values if the innerWriter is nil.
func (element *webRTCWriter) SortedResolutions() *gomedia.WebRTCCodec {
	codec := &gomedia.WebRTCCodec{HasAudio: false, Resolutions: make([]gomedia.Resolution, 0)}

	for _, url := range element.streams.sortedURLs {
		if element.streams.streams[url].codecPar.AudioCodecParameters != nil {
			codec.HasAudio = true
		}
		codec.Resolutions = append(codec.Resolutions, gomedia.Resolution{
			URL:    url,
			Width:  int(element.streams.streams[url].codecPar.Width()),  //nolint:gosec
			Height: int(element.streams.streams[url].codecPar.Height()), //nolint:gosec
		})
	}

	return codec
}

// Packets returns the input packet channel of the innerWriter.
func (element *webRTCWriter) Packets() chan<- gomedia.Packet {
	return element.inpPktCh
}

// Peers returns the peers channel of the innerWriter.
func (element *webRTCWriter) Peers() chan<- *gomedia.WebRTCPeer {
	return element.peersChan
}

// RemoveSource returns the remove source channel of the writer.
func (element *webRTCWriter) RemoveSource() chan<- string {
	return element.rmSrcCh
}

// AddSource returns the add source channel of the writer.
func (element *webRTCWriter) AddSource() chan<- string {
	return element.addSrcCh
}

// String returns a string representation of the innerWriter, including the number of tracks.
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
