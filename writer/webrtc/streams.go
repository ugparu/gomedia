package webrtc

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/utils"
	"github.com/ugparu/gomedia/utils/buffer"
	"github.com/ugparu/gomedia/utils/logger"
	"github.com/ugparu/gomedia/utils/nal"
)

type stream struct {
	tracks   map[*peerTrack]bool
	toAdd    map[*peerTrack]*peerURL
	buffer   *Buffer
	codecPar gomedia.CodecParametersPair
}

// sortedStreams is a map of sorted stream URLs based on their sizes.
type sortedStreams struct {
	sortedURLs     []string           // Sorted list of stream URLs based on their sizes.
	streams        map[string]*stream // Map of streams indexed by their URLs.
	targetDuration time.Duration
}

// Exists checks if a stream URL exists in the sortedStreams.
func (ss *sortedStreams) Exists(url string) bool {
	_, found := ss.streams[url]
	return found
}

// Add adds a new stream to the sortedStreams map.
// Returns if the stream was added or changed.
func (ss *sortedStreams) Update(newURL string, newCodecPar gomedia.CodecParameters) bool {
	stream, found := ss.streams[newURL]
	if !found {
		return true
	}

	switch par := newCodecPar.(type) {
	case gomedia.VideoCodecParameters:
		if stream.codecPar.VideoCodecParameters == par {
			return false
		}
		logger.Infof(ss, "Updating stream %s with new codec parameters: %dx%d to %dx%d",
			newURL, stream.codecPar.VideoCodecParameters.Width(), stream.codecPar.VideoCodecParameters.Height(),
			par.Width(), par.Height())
		stream.codecPar.VideoCodecParameters = par
		stream.buffer.Reset()

		const flushDuration = time.Second * 3

		for peer := range stream.tracks {
			select {
			case peer.flush <- struct{}{}:
			case <-time.After(flushDuration):
				logger.Errorf(ss, "Failed to flush peer %v", peer)
			}
		}
	case gomedia.AudioCodecParameters:
		if stream.codecPar.AudioCodecParameters == par {
			return false
		}
		stream.codecPar.AudioCodecParameters = par
	default:
		return false
	}

	for i := len(ss.sortedURLs) - 1; i >= 1; i-- {
		var oldResolution uint
		if ss.streams[ss.sortedURLs[i-1]].codecPar.VideoCodecParameters != nil {
			oldResolution = ss.streams[ss.sortedURLs[i-1]].codecPar.VideoCodecParameters.Width() *
				ss.streams[ss.sortedURLs[i-1]].codecPar.VideoCodecParameters.Height()
		}
		var newResolution uint
		if ss.streams[ss.sortedURLs[i]].codecPar.VideoCodecParameters != nil {
			newResolution = ss.streams[ss.sortedURLs[i]].codecPar.VideoCodecParameters.Width() *
				ss.streams[ss.sortedURLs[i]].codecPar.VideoCodecParameters.Height()
		}

		if oldResolution > newResolution {
			ss.sortedURLs[i], ss.sortedURLs[i-1] = ss.sortedURLs[i-1], ss.sortedURLs[i]
		} else {
			break
		}
	}

	return true
}

// Add adds a new stream URL with its size to the sortedStreams.
func (ss *sortedStreams) Add(url string, newCodecPar gomedia.CodecParameters) {
	if _, found := ss.streams[url]; found {
		return
	}

	pair := gomedia.CodecParametersPair{
		URL:                  url,
		VideoCodecParameters: nil,
		AudioCodecParameters: nil,
	}

	switch par := newCodecPar.(type) {
	case gomedia.VideoCodecParameters:
		pair.VideoCodecParameters = par
	case gomedia.AudioCodecParameters:
		pair.AudioCodecParameters = par
	}

	ss.streams[url] = &stream{
		tracks: map[*peerTrack]bool{},
		toAdd:  map[*peerTrack]*peerURL{},
		buffer: &Buffer{
			gops:           nil,
			duration:       0,
			targetDuration: ss.targetDuration,
		},
		codecPar: pair,
	}
	ss.sortedURLs = append(ss.sortedURLs, url)
	for i := len(ss.sortedURLs) - 1; i >= 1; i-- {
		if ss.streams[ss.sortedURLs[i]].codecPar.Width()*ss.streams[ss.sortedURLs[i]].codecPar.Height() <
			ss.streams[ss.sortedURLs[i-1]].codecPar.Width()*ss.streams[ss.sortedURLs[i-1]].codecPar.Height() {
			ss.sortedURLs[i], ss.sortedURLs[i-1] = ss.sortedURLs[i-1], ss.sortedURLs[i]
		}
	}
}

// Remove removes a stream URL from the sortedStreams.
func (ss *sortedStreams) Remove(removeURL string) {
	str, found := ss.streams[removeURL]
	if !found {
		return
	}

	index := 0
	for i, url := range ss.sortedURLs {
		if url == removeURL {
			index = i
			break
		}
	}

	var changeURL string
	if len(ss.sortedURLs) > 1 {
		if index > 0 {
			changeURL = ss.sortedURLs[index-1]
		} else {
			changeURL = ss.sortedURLs[index+1]
		}
	}

	if changeURL == "" {
		for conn := range str.tracks {
			conn.PeerConnection.Close()
		}
	} else {
		for track := range str.tracks {
			if err := ss.Move(&peerURL{
				peerTrack: track,
				Token:     "",
				URL:       changeURL,
			}); err != nil {
				logger.Error(ss, err.Error())
			}
		}
	}
	delete(ss.streams, removeURL)
	ss.sortedURLs = append(ss.sortedURLs[:index], ss.sortedURLs[index+1:]...)
}

func (ss *sortedStreams) Insert(pt *peerTrack) (err error) {
	if len(ss.sortedURLs) == 0 {
		return errors.New("no codec parameters")
	}
	ss.streams[ss.sortedURLs[0]].tracks[pt] = false

	return nil
}

func (ss *sortedStreams) Move(pu *peerURL) (err error) {
	ss.streams[pu.URL].toAdd[pu.peerTrack] = pu
	return nil
}

// Define sentinel errors
var (
	ErrPacketTooSmall = errors.New("packet data too small")
	ErrStreamNotFound = errors.New("stream not found for URL")
)

// validatePacket validates the input packet and returns the corresponding stream
func (ss *sortedStreams) validatePacket(pkt gomedia.Packet) (*stream, error) {
	if pkt == nil {
		return nil, &utils.NilPacketError{}
	}

	if pkt.Len() < minPktSz {
		return nil, ErrPacketTooSmall
	}

	str, found := ss.streams[pkt.URL()]
	if !found {
		logger.Debugf(ss, "unknown url %s", pkt.URL())
		return nil, ErrStreamNotFound
	}

	return str, nil
}

// processPendingTracks processes tracks that are pending addition to the stream
func (ss *sortedStreams) processPendingTracks(str *stream, pkt gomedia.Packet) []*peerTrack {
	var removeFromToAdd []*peerTrack

	for _, pu := range str.toAdd {
		if canAdd, peerBuf := ss.canAddTrackToPeer(str, pkt, pu); canAdd {
			removeFromToAdd = append(removeFromToAdd, pu.peerTrack)
			ss.moveTrackToStream(str, pu, peerBuf)
			ss.notifyTrackChange(pu)
		}
	}

	return removeFromToAdd
}

// canAddTrackToPeer determines if a track can be added to a peer
func (ss *sortedStreams) canAddTrackToPeer(str *stream, pkt gomedia.Packet, pu *peerURL) (bool, []gomedia.Packet) {
	_, peerBuf := str.buffer.GetBuffer(time.Now().Add(-pu.delay))

	// Case 1: Empty buffer requires a key frame in the current packet
	if len(peerBuf) == 0 {
		return ss.hasKeyFrame(pkt), peerBuf
	}

	for _, bufPkt := range peerBuf {
		if _, ok := bufPkt.(gomedia.VideoPacket); ok {
			return ss.hasKeyFrame(bufPkt), peerBuf
		}
	}

	return false, nil
}

// hasKeyFrame checks if the packet is a video key frame
func (ss *sortedStreams) hasKeyFrame(pkt gomedia.Packet) bool {
	vPkt, ok := pkt.(gomedia.VideoPacket)
	return ok && vPkt.IsKeyFrame()
}

// moveTrackToStream moves a track to a specific stream
func (ss *sortedStreams) moveTrackToStream(str *stream, pu *peerURL, peerBuf []gomedia.Packet) {
	// Remove from all other streams
	for _, curPeers := range ss.streams {
		delete(curPeers.tracks, pu.peerTrack)
	}

	const flushDuration = time.Second * 3
	select {
	case pu.flush <- struct{}{}:
	case <-time.After(flushDuration):
		logger.Errorf(ss, "Failed to flush peer %v", pu.peerTrack)
	}

	for _, bufPkt := range peerBuf {
		switch packet := bufPkt.(type) {
		case gomedia.VideoPacket:
			pu.peerTrack.vChan <- packet
		case gomedia.AudioPacket:
			pu.peerTrack.aChan <- packet
		}
	}

	// Add to current stream
	str.tracks[pu.peerTrack] = true
}

// notifyTrackChange sends a notification about track changes
func (ss *sortedStreams) notifyTrackChange(pu *peerURL) {
	go func(pu *peerURL) {
		var bytes []byte
		var err error

		if pu.Token == "" {
			reqMsg := &dataChanReq{
				Token:   pu.Token,
				Command: "setStreamUrl",
				Message: pu.URL,
			}
			bytes, err = json.Marshal(reqMsg)
		} else {
			respMsg := &resp{
				Token:   pu.Token,
				Status:  http.StatusOK,
				Message: "Ok",
			}
			bytes, err = json.Marshal(respMsg)
		}

		if err != nil {
			logger.Error(ss, err.Error())
			return
		}

		logger.Infof(ss, "Sending message %s", bytes)
		if pu.DataChannel != nil {
			if err = pu.DataChannel.Send(bytes); err != nil {
				logger.Error(ss, err.Error())
			}
		}
	}(pu)
}

// processExistingTracks processes tracks that are already part of the stream
func (ss *sortedStreams) processExistingTracks(str *stream, pkt gomedia.Packet) error {
	for peer, seeded := range str.tracks {
		if !seeded {
			if err := ss.seedTrack(str, peer); err != nil {
				return err
			}
		} else {
			ss.bufferPacketForPeer(peer, pkt)
		}
	}

	return nil
}

// seedTrack initializes a new track with buffer data
func (ss *sortedStreams) seedTrack(str *stream, peer *peerTrack) error {
	seedBuf, peerBuf := str.buffer.GetBuffer(time.Now().Add(-peer.delay))

	const pktDur = time.Millisecond * 5
	const gracePeriod = time.Millisecond * 150

	start := time.Now()
	targetSeedDuration := time.Duration(len(seedBuf))*pktDur + gracePeriod

	if len(seedBuf) > 0 {
		targetSeedDuration += gracePeriod
		ticker := time.NewTicker(pktDur)
		defer ticker.Stop()

		for _, vPkt := range seedBuf {
			bufSample := media.Sample{
				Data:               []byte{},
				Timestamp:          vPkt.StartTime(),
				Duration:           pktDur,
				PacketTimestamp:    uint32(vPkt.Timestamp()), //nolint:gosec
				PrevDroppedPackets: 0,
				Metadata:           nil,
			}

			if vPkt.IsKeyFrame() {
				bufSample.Data = appendCodecParameters(vPkt.CodecParameters())
			}

			var bufNalus [][]byte
			vPkt.View(func(data buffer.PooledBuffer) {
				bufNalus, _ = nal.SplitNALUs(data.Data())
			})

			for _, nalu := range bufNalus {
				bufSample.Data = append(bufSample.Data, append([]byte{0, 0, 0, 1}, nalu...)...)
			}

			if err := peer.vt.WriteSample(bufSample); err != nil {
				return err
			}

			<-ticker.C
		}
	}

	// Buffer packets for the peer
	for _, bufPkt := range peerBuf {
		switch packet := bufPkt.(type) {
		case gomedia.VideoPacket:
			peer.vChan <- packet
		case gomedia.AudioPacket:
			peer.aChan <- packet
		}
	}

	str.tracks[peer] = true

	startReq := &dataChanReq{
		Token:   "",
		Command: "startStream",
		Message: "",
	}

	bytes, err := json.Marshal(startReq)
	if err != nil {
		return err
	}

	logger.Infof(ss, "Sending message %s after seeding %d packets", string(bytes), len(seedBuf))

	if err = peer.DataChannel.Send(bytes); err != nil {
		return err
	}

	actualDur := time.Since(start)
	if actualDur < targetSeedDuration {
		time.Sleep(targetSeedDuration - actualDur)
	}

	return nil
}

// bufferPacketForPeer adds a packet to peer's buffer
func (ss *sortedStreams) bufferPacketForPeer(peer *peerTrack, pkt gomedia.Packet) {
	switch packet := pkt.(type) {
	case gomedia.VideoPacket:
		peer.vChan <- packet
	case gomedia.AudioPacket:
		peer.aChan <- packet
	}
}

// writePacket processes a packet and distributes it to relevant peers
func (ss *sortedStreams) writePacket(pkt gomedia.Packet) (err error) {
	// Validate input packet
	str, err := ss.validatePacket(pkt)
	if err != nil {
		// Ignore specific errors that shouldn't propagate
		if errors.Is(err, ErrPacketTooSmall) || errors.Is(err, ErrStreamNotFound) {
			return nil
		}
		return err
	}

	// Add packet to buffer
	str.buffer.AddPacket(pkt)

	// Process pending tracks
	removeFromToAdd := ss.processPendingTracks(str, pkt)

	// Remove processed tracks from pending list
	for _, pt := range removeFromToAdd {
		delete(str.toAdd, pt)
	}

	// Process existing tracks
	return ss.processExistingTracks(str, pkt)
}
