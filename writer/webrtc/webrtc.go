package webrtc

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/utils/lifecycle"
	"github.com/ugparu/gomedia/utils/logger"
)

var peeerTracks = 0

type webRTCWriter struct {
	lifecycle.AsyncManager[*webRTCWriter]
	streams          *sortedStreams
	peersChan        chan gomedia.WebRTCPeer
	changePeersChan  chan *peerURL
	connectPeersChan chan *peerTrack
	closePeersChan   chan *peerTrack
	inpPktCh         chan gomedia.Packet
	rmSrcCh          chan string
}

// New creates a new Streamer with the given channel size.
// It initializes an innerWriter and embeds it into an outerWebRTC, providing synchronization and lifecycle management.
func New(chanSize int) gomedia.WebRTCStreamer {
	wr := &webRTCWriter{
		AsyncManager: nil,
		streams: &sortedStreams{
			sortedURLs: []string{},
			streams:    map[string]*stream{},
		},
		peersChan:        make(chan gomedia.WebRTCPeer),
		changePeersChan:  make(chan *peerURL, chanSize),
		connectPeersChan: make(chan *peerTrack, chanSize),
		closePeersChan:   make(chan *peerTrack, chanSize),
		inpPktCh:         make(chan gomedia.Packet, chanSize),
		rmSrcCh:          make(chan string, chanSize),
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
	case rmURL := <-element.rmSrcCh:
		element.streams.Remove(rmURL)
	case inpPkt := <-element.inpPktCh:
		switch pkt := inpPkt.(type) {
		case gomedia.VideoPacket:
			if err = element.checkCodecParameters(inpPkt.URL(), pkt.CodecParameters()); err != nil {
				return
			}
		case gomedia.AudioPacket:
			if err = element.checkCodecParameters(inpPkt.URL(), pkt.CodecParameters()); err != nil {
				return
			}
		}

		if err = element.streams.writePacket(inpPkt); err != nil {
			return err
		}
	case peer := <-element.peersChan:
		element.peersChan <- element.addConnection(peer)
		return peer.Err
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
	}
	return nil
}

// checkCodecParameters updates the codec parameters based on the provided map.
// It manages stream sizes, updates codec parameters, and sends messages to inform peers about available streams.
func (element *webRTCWriter) checkCodecParameters(url string, codecPar gomedia.CodecParameters) (err error) {
	if !element.streams.Exists(url) {
		if _, ok := codecPar.(gomedia.VideoCodecParameters); !ok {
			return
		}

		element.streams.Add(url, codecPar)
	} else if !element.streams.Update(url, codecPar) {
		return
	}

	reqMsg := &codecReq{
		Token:   "",
		Command: "setAvailableStreams",
		Message: codec{
			Type:        "video",
			Resolutions: []resolution{},
		},
	}

	for _, url := range element.streams.sortedURLs {
		if element.streams.streams[url].codecPar.VideoCodecParameters == nil {
			continue
		}
		reqMsg.Message.Resolutions = append(reqMsg.Message.Resolutions, resolution{
			URL:    url,
			Width:  int(element.streams.streams[url].codecPar.Width()),  //nolint:gosec
			Height: int(element.streams.streams[url].codecPar.Height()), //nolint:gosec
		})
	}

	var bytes []byte
	if bytes, err = json.Marshal(reqMsg); err != nil {
		return err
	}

	for _, stream := range element.streams.streams {
		for peer := range stream.tracks {
			logger.Infof(element, "Sending message %s", bytes)
			if peer.DataChannel != nil {
				if err = peer.DataChannel.Send(bytes); err != nil {
					return err
				}
			}
		}
	}
	return nil
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

func (element *webRTCWriter) addConnection(inpPeer gomedia.WebRTCPeer) gomedia.WebRTCPeer {
	sdp64 := inpPeer.SDP

	if len(element.streams.sortedURLs) == 0 {
		inpPeer.Err = errors.New("no streams available")
		return inpPeer
	}

	sdpB, err := base64.StdEncoding.DecodeString(sdp64)
	if err != nil {
		inpPeer.Err = err
		return inpPeer
	}

	offer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  string(sdpB),
	}
	peer, err := api.NewPeerConnection(conf)
	peeerTracks++
	logger.Infof(element, "peeerTracks added: %d", peeerTracks)
	runtime.SetFinalizer(peer, func(*webrtc.PeerConnection) {
		peeerTracks--
		logger.Infof(element, "peeerTracks removed: %d", peeerTracks)
	})

	if err != nil {
		inpPeer.Err = err
		return inpPeer
	}

	codecType := element.streams.streams[element.streams.sortedURLs[0]].codecPar.VideoCodecParameters.Type()
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
		return inpPeer
	}

	vRTPSender, err := peer.AddTrack(vtrack)
	if err != nil {
		inpPeer.Err = err
		return inpPeer
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
		return inpPeer
	}

	aRTPSender, err := peer.AddTrack(atrack)
	if err != nil {
		inpPeer.Err = err
		return inpPeer
	}
	go dropRTCP(aRTPSender)

	const bufSize = 1000
	pt := &peerTrack{
		PeerConnection: peer,
		vt:             vtrack,
		at:             atrack,
		delay:          time.Second * time.Duration(inpPeer.Delay),
		aBuf:           make(chan gomedia.AudioPacket, bufSize),
		vBuf:           make(chan gomedia.VideoPacket, bufSize),
		flush:          make(chan struct{}),
		done:           make(chan struct{}),
		DataChannel:    nil,
	}

	go writeVideoPacketsToPeer(pt)
	go writeAudioPacketsToPeer(pt)

	peer.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		logger.Infof(element, "Connection state has changed to %s", connectionState.String())
		if connectionState == webrtc.ICEConnectionStateDisconnected ||
			connectionState == webrtc.ICEConnectionStateClosed || connectionState == webrtc.ICEConnectionStateFailed {
			element.closePeersChan <- pt
		}
	})

	peer.OnDataChannel(func(d *webrtc.DataChannel) {
		d.OnOpen(func() {
			pt.DataChannel = d
			logger.Infof(element, "Data channel '%s'-'%d' opened", d.Label(), d.ID())
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

	if err = peer.SetRemoteDescription(offer); err != nil {
		inpPeer.Err = err
		return inpPeer
	}

	answer, err := peer.CreateAnswer(nil)
	if err != nil {
		inpPeer.Err = err
		return inpPeer
	}
	gatherCompletePromise := webrtc.GatheringCompletePromise(peer)

	if err = peer.SetLocalDescription(answer); err != nil {
		inpPeer.Err = err
		return inpPeer
	}

	<-gatherCompletePromise

	inpPeer.SDP = base64.StdEncoding.EncodeToString([]byte(peer.LocalDescription().SDP))

	return inpPeer
}

// removePeer removes a peer from all existing and to-be-added peers, removes associated tracks,
// closes the DataChannel, and closes the PeerConnection.
func (element *webRTCWriter) removePeer(peer *peerTrack) (err error) {
	for _, peers := range element.streams.streams {
		delete(peers.tracks, peer)
		delete(peers.toAdd, peer)
	}
	senders := peer.GetSenders()
	for _, stream := range senders {
		if err = peer.RemoveTrack(stream); err != nil {
			return err
		}
	}
	if peer.DataChannel != nil {
		peer.DataChannel.Close()
	}
	peer.PeerConnection.Close()
	close(peer.done)
	return nil
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
func (element *webRTCWriter) Peers() chan gomedia.WebRTCPeer {
	return element.peersChan
}

// RemoveSource returns the remove source channel of the writer.
func (element *webRTCWriter) RemoveSource() chan<- string {
	return element.rmSrcCh
}

// String returns a string representation of the innerWriter, including the number of tracks.
func (element *webRTCWriter) String() string {
	return fmt.Sprintf("WEBRTC_WRITER lvls=%d", len(element.streams.streams))
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
