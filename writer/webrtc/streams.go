package webrtc

import (
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/utils"
	"github.com/ugparu/gomedia/utils/logger"
)

type stream struct {
	tracks   map[*peerTrack]bool
	toAdd    map[*peerTrack]*peerURL
	buffer   *Buffer
	codecPar gomedia.CodecParametersPair
}

// sortedStreams maintains per-source streams sorted ascending by resolution so
// ABR clients see the smallest stream first. pendingPeers holds peers whose
// target stream has momentarily vanished (e.g. during a source restart) and
// lets them rejoin as soon as any stream reappears.
type sortedStreams struct {
	log            logger.Logger
	sortedURLs     []string
	streams        map[string]*stream
	pendingPeers   map[*peerTrack]bool
	failedPeers    []*peerTrack
	targetDuration time.Duration
	videoCodecType gomedia.CodecType
	signaling      SignalingHandler
}

func (ss *sortedStreams) Exists(url string) bool {
	_, found := ss.streams[url]
	return found
}

// Update rebinds codec parameters on a live stream. A change in video codec
// type (H264 ↔ H265) returns every attached peer for tear-down — WebRTC
// tracks are pinned to the negotiated MIME type and cannot be repointed.
// Resolution changes only flush peer buffers so the next keyframe re-syncs.
func (ss *sortedStreams) Update(newURL string, newCodecPar gomedia.CodecParameters) ([]*peerTrack, bool) {
	stream, found := ss.streams[newURL]
	if !found {
		return nil, false
	}

	switch par := newCodecPar.(type) {
	case gomedia.VideoCodecParameters:
		if stream.codecPar.VideoCodecParameters == par {
			return nil, false
		}

		oldType := stream.codecPar.VideoCodecParameters.Type()
		newType := par.Type()
		if oldType != newType {
			ss.log.Infof(ss, "Video codec type changed from %s to %s on stream %s, disconnecting all peers",
				oldType, newType, newURL)

			var peersToRemove []*peerTrack
			for peer := range stream.tracks {
				peersToRemove = append(peersToRemove, peer)
			}
			for peer := range stream.toAdd {
				peersToRemove = append(peersToRemove, peer)
			}

			stream.tracks = make(map[*peerTrack]bool)
			stream.toAdd = make(map[*peerTrack]*peerURL)
			stream.codecPar.VideoCodecParameters = par
			ss.videoCodecType = newType
			stream.buffer.Reset()
			ss.sortURLsByResolution()

			return peersToRemove, true
		}

		ss.log.Infof(ss, "Updating stream %s with new codec parameters: %dx%d to %dx%d",
			newURL, stream.codecPar.VideoCodecParameters.Width(), stream.codecPar.VideoCodecParameters.Height(),
			par.Width(), par.Height())
		stream.codecPar.VideoCodecParameters = par
		ss.videoCodecType = stream.codecPar.VideoCodecParameters.Type()

		stream.buffer.Reset()

		const flushDuration = time.Second * 3

		for peer := range stream.tracks {
			select {
			case peer.vflush <- struct{}{}:
			case <-time.After(flushDuration):
				ss.log.Errorf(ss, "Failed to flush peer %v", peer)
			}
		}
	case gomedia.AudioCodecParameters:
		if stream.codecPar.AudioCodecParameters == par {
			return nil, false
		}
		stream.codecPar.AudioCodecParameters = par
	default:
		return nil, false
	}

	ss.sortURLsByResolution()

	return nil, true
}

// sortURLsByResolution performs a single insertion-sort pass since only the
// just-updated stream's position can be wrong; O(n) is plenty for the few
// streams a single source set contains.
func (ss *sortedStreams) sortURLsByResolution() {
	for i := len(ss.sortedURLs) - 1; i >= 1; i-- {
		oldResolution := ss.streams[ss.sortedURLs[i-1]].codecPar.VideoCodecParameters.Width() *
			ss.streams[ss.sortedURLs[i-1]].codecPar.VideoCodecParameters.Height()
		newResolution := ss.streams[ss.sortedURLs[i]].codecPar.VideoCodecParameters.Width() *
			ss.streams[ss.sortedURLs[i]].codecPar.VideoCodecParameters.Height()

		if oldResolution > newResolution {
			ss.sortedURLs[i], ss.sortedURLs[i-1] = ss.sortedURLs[i-1], ss.sortedURLs[i]
		} else {
			break
		}
	}
}

// Add creates the stream once its video codec parameters are known. Audio-only
// first packets are ignored — without video parameters there is nothing to
// negotiate against a WebRTC offer. Any peers parked in pendingPeers (from
// a previous Remove) are migrated onto the new stream and returned so the
// caller can notify them via setStreamUrl.
func (ss *sortedStreams) Add(url string, newCodecPar gomedia.CodecParameters) []*peerTrack {
	if _, found := ss.streams[url]; found {
		return nil
	}

	videoPar, ok := newCodecPar.(gomedia.VideoCodecParameters)
	if !ok || videoPar == nil {
		return nil
	}

	pair := gomedia.CodecParametersPair{
		SourceID:             url,
		VideoCodecParameters: videoPar,
		AudioCodecParameters: nil,
	}

	ss.streams[url] = &stream{
		tracks: map[*peerTrack]bool{},
		toAdd:  map[*peerTrack]*peerURL{},
		buffer: &Buffer{
			log:             ss.log,
			gops:            nil,
			duration:        0,
			targetDuration:  ss.targetDuration,
			hardCapDuration: ss.targetDuration + time.Second,
		},
		codecPar: pair,
	}
	ss.sortedURLs = append(ss.sortedURLs, url)

	ss.sortURLsByResolution()

	var movedPeers []*peerTrack
	if len(ss.pendingPeers) > 0 {
		for peer := range ss.pendingPeers {
			const flushDuration = time.Second * 3
			timer := time.After(flushDuration)
			vFlushed := false
			aFlushed := false
			for !(vFlushed && aFlushed) {
				select {
				case peer.vflush <- struct{}{}:
					vFlushed = true
				case peer.aflush <- struct{}{}:
					aFlushed = true
				case <-timer:
					ss.log.Errorf(ss, "Failed to flush peer %v when moving from pending", peer)
					vFlushed = true
					aFlushed = true
				}
			}

			// Mark seeded=true: these peers already completed WebRTC negotiation on a
			// prior stream, so they only need setStreamUrl, not a fresh seed.
			ss.streams[url].tracks[peer] = true
			movedPeers = append(movedPeers, peer)
			delete(ss.pendingPeers, peer)
		}
	}
	return movedPeers
}

// Remove migrates attached peers to the adjacent stream in sort order so
// clients keep playing during a source rotation. When no other stream
// exists the peers are parked in pendingPeers until Add brings one back.
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
		if ss.pendingPeers == nil {
			ss.pendingPeers = make(map[*peerTrack]bool)
		}
		maps.Copy(ss.pendingPeers, str.tracks)
		for peer := range str.toAdd {
			ss.pendingPeers[peer] = true
		}
	} else {
		for track := range str.tracks {
			if err := ss.Move(&peerURL{
				peerTrack: track,
				Token:     "",
				URL:       changeURL,
			}); err != nil {
				ss.log.Error(ss, err.Error())
			}
		}
		for peer := range str.toAdd {
			if err := ss.Move(&peerURL{
				peerTrack: peer,
				Token:     "",
				URL:       changeURL,
			}); err != nil {
				ss.log.Error(ss, err.Error())
			}
		}
	}

	str.buffer.Close()

	delete(ss.streams, removeURL)
	ss.sortedURLs = append(ss.sortedURLs[:index], ss.sortedURLs[index+1:]...)
}

// Insert attaches a freshly connected peer to its chosen stream or parks it
// in pendingPeers when the stream is absent (seeded=false so writePacket
// will later send the initial keyframe burst).
func (ss *sortedStreams) Insert(pt *peerTrack) (err error) {
	if str, ok := ss.streams[pt.targetURL]; ok && str != nil {
		str.tracks[pt] = false
		return nil
	} else if !ok {
		return fmt.Errorf("unknown URL: %s", pt.targetURL)
	}

	if ss.pendingPeers == nil {
		ss.pendingPeers = make(map[*peerTrack]bool)
	}
	ss.pendingPeers[pt] = false
	return nil
}

func (ss *sortedStreams) Move(pu *peerURL) (err error) {
	targetStream := ss.streams[pu.URL]
	if targetStream == nil {
		return errors.New("target stream does not exist: " + pu.URL)
	}

	ss.streams[pu.URL].toAdd[pu.peerTrack] = pu
	return nil
}

var (
	ErrPacketTooSmall = errors.New("packet data too small")
	ErrStreamNotFound = errors.New("stream not found for URL")
)

func (ss *sortedStreams) validatePacket(pkt gomedia.Packet) (*stream, error) {
	if pkt == nil {
		return nil, &utils.NilPacketError{}
	}

	if pkt.Len() < minPktSz {
		return nil, ErrPacketTooSmall
	}

	str, found := ss.streams[pkt.SourceID()]
	if !found {
		ss.log.Debugf(ss, "unknown url %s", pkt.SourceID())
		return nil, ErrStreamNotFound
	}

	return str, nil
}

func (ss *sortedStreams) processPendingTracks(str *stream, pkt gomedia.Packet) []*peerTrack {
	var removeFromToAdd []*peerTrack

	for _, pu := range str.toAdd {
		if canAdd, peerBuf := ss.canAddTrackToPeer(str, pkt, pu); canAdd {
			removeFromToAdd = append(removeFromToAdd, pu.peerTrack)
			ss.moveTrackToStream(str, pu, peerBuf)
		}
	}

	return removeFromToAdd
}

// canAddTrackToPeer reports whether seed data up to the peer's delay window
// already contains a keyframe the track can decode from. It also returns the
// buffer slice the caller should splice in, potentially prepended with seed
// frames so the player starts at a valid IDR rather than mid-GoP.
func (ss *sortedStreams) canAddTrackToPeer(str *stream, pkt gomedia.Packet, pu *peerURL) (bool, []gomedia.Packet) {
	seedBuf, peerBuf := str.buffer.GetBuffer(time.Now().Add(-pu.delay))

	if len(peerBuf) == 0 {
		return ss.hasKeyFrame(pkt), peerBuf
	}

	const lookback = 3
	seedStart := 0
	if len(seedBuf) > lookback {
		seedStart = len(seedBuf) - lookback
	}
	seedFrames := seedBuf[seedStart:]

	peerVideoCount := 0
	keyframeInSeedIdx := -1
	keyframeInPeerIdx := -1

	for i, vp := range seedFrames {
		if vp.IsKeyFrame() {
			keyframeInSeedIdx = seedStart + i
			break
		}
	}

	if keyframeInSeedIdx < 0 {
		for i, bufPkt := range peerBuf {
			if vp, ok := bufPkt.(gomedia.VideoPacket); ok {
				if vp.IsKeyFrame() {
					keyframeInPeerIdx = i
					break
				}
				peerVideoCount++
				if peerVideoCount >= lookback {
					break
				}
			}
		}
	}

	if keyframeInSeedIdx >= 0 {
		newPeerBuf := make([]gomedia.Packet, 0, len(seedBuf)-keyframeInSeedIdx+len(peerBuf))
		for _, vp := range seedBuf[keyframeInSeedIdx:] {
			newPeerBuf = append(newPeerBuf, vp)
		}
		newPeerBuf = append(newPeerBuf, peerBuf...)
		return true, newPeerBuf
	}
	if keyframeInPeerIdx >= 0 {
		return true, peerBuf[keyframeInPeerIdx:]
	}
	return false, nil
}

func (ss *sortedStreams) hasKeyFrame(pkt gomedia.Packet) bool {
	vPkt, ok := pkt.(gomedia.VideoPacket)
	return ok && vPkt.IsKeyFrame()
}

func (ss *sortedStreams) moveTrackToStream(str *stream, pu *peerURL, peerBuf []gomedia.Packet) {
	for _, curPeers := range ss.streams {
		delete(curPeers.tracks, pu.peerTrack)
	}

	const flushDuration = time.Second * 3
	select {
	case pu.vflush <- struct{}{}:
	case <-time.After(flushDuration):
		ss.log.Errorf(ss, "Failed to flush peer %v", pu.peerTrack)
	}

	select {
	case pu.aflush <- struct{}{}:
	case <-time.After(flushDuration):
		ss.log.Errorf(ss, "Failed to flush peer %v", pu.peerTrack)
	}

	const sendTimeout = time.Millisecond * 100
	for _, bufPkt := range peerBuf {
		select {
		case <-pu.done:
			return
		default:
		}

		clone := bufPkt.Clone(false)

		switch clonePkt := clone.(type) {
		case gomedia.VideoPacket:
			select {
			case pu.peerTrack.vChan <- clonePkt:
			case <-time.After(sendTimeout):
				clonePkt.Release()
				ss.log.Errorf(ss, "Timeout sending video packet to peer during stream move")
			}
		case gomedia.AudioPacket:
			select {
			case pu.peerTrack.aChan <- clonePkt:
			case <-time.After(sendTimeout):
				clonePkt.Release()
				ss.log.Errorf(ss, "Timeout sending audio packet to peer during stream move")
			}
		}
	}

	str.tracks[pu.peerTrack] = true

	if pu.peerTrack != nil && pu.peerTrack.DataChannel != nil {
		bytes, err := ss.signaling.BuildStreamMoved(pu.Token, pu.URL)
		if err != nil {
			ss.log.Errorf(ss, "Failed to marshal stream moved notification: %v", err)
			return
		}

		select {
		case <-pu.done:
			return
		default:
			ss.log.Infof(ss, "Sending stream moved message %s", bytes)
			if err = pu.peerTrack.DataChannel.Send(bytes); err != nil {
				ss.log.Errorf(ss, "Failed to send stream moved notification: %v", err)
			}
		}
	}
}

func (ss *sortedStreams) processExistingTracks(str *stream, pkt gomedia.Packet) error {
	for peer, seeded := range str.tracks {
		if !seeded {
			if err := ss.seedTrack(str, peer); err != nil {
				ss.log.Errorf(ss, "Failed to seed peer, removing: %v", err)
				delete(str.tracks, peer)
				ss.failedPeers = append(ss.failedPeers, peer)
			}
		} else {
			ss.bufferPacketForPeer(peer, pkt)
		}
	}

	return nil
}

// seedTrack primes a freshly connected peer with the current GoP so the
// decoder can start on a keyframe. Seed frames get a tiny synthetic duration
// so the player flushes them quickly and converges on real-time playback.
func (ss *sortedStreams) seedTrack(str *stream, peer *peerTrack) error {
	if peer.DataChannel == nil {
		return errors.New("peer data channel is nil")
	}

	seedBuf, peerBuf := str.buffer.GetBuffer(time.Now().Add(-peer.delay))

	for _, vPkt := range seedBuf {
		clonePkt := vPkt.Clone(false).(gomedia.VideoPacket)
		const pktDur = time.Millisecond * 5
		clonePkt.SetDuration(pktDur)
		select {
		case peer.vChan <- clonePkt:
		case <-peer.done:
			clonePkt.Release()
			return errors.New("peer disconnected during seeding")
		}
	}

	for _, bufPkt := range peerBuf {
		switch packet := bufPkt.(type) {
		case gomedia.VideoPacket:
			clonePkt := packet.Clone(false).(gomedia.VideoPacket)
			select {
			case peer.vChan <- clonePkt:
			case <-peer.done:
				clonePkt.Release()
				return errors.New("peer disconnected during seeding")
			}
		case gomedia.AudioPacket:
			clonePkt := packet.Clone(false).(gomedia.AudioPacket)
			select {
			case peer.aChan <- clonePkt:
			case <-peer.done:
				clonePkt.Release()
				return errors.New("peer disconnected during seeding")
			}
		}
	}

	str.tracks[peer] = true

	bytes, err := ss.signaling.BuildStreamStarted()
	if err != nil {
		return err
	}

	ss.log.Infof(ss, "Sending message %s after seeding %d packets", string(bytes), len(seedBuf))

	if err = peer.DataChannel.Send(bytes); err != nil {
		return err
	}

	return nil
}

// bufferPacketForPeer forwards a clone into the peer's channel; if the
// channel is full the clone is dropped rather than blocking the writer
// goroutine — in real-time delivery a stale frame is worse than a gap.
func (ss *sortedStreams) bufferPacketForPeer(peer *peerTrack, pkt gomedia.Packet) {
	select {
	case <-peer.done:
		return
	default:
	}

	switch packet := pkt.(type) {
	case gomedia.VideoPacket:
		clonePkt := packet.Clone(false).(gomedia.VideoPacket)
		select {
		case peer.vChan <- clonePkt:
		case <-peer.done:
			clonePkt.Release()
		default:
			clonePkt.Release()
		}
	case gomedia.AudioPacket:
		clonePkt := packet.Clone(false).(gomedia.AudioPacket)
		select {
		case peer.aChan <- clonePkt:
		case <-peer.done:
			clonePkt.Release()
		default:
			clonePkt.Release()
		}
	}
}

func (ss *sortedStreams) writePacket(pkt gomedia.Packet) (err error) {
	str, err := ss.validatePacket(pkt)
	if err != nil {
		if pkt != nil {
			pkt.Release()
		}
		if errors.Is(err, ErrPacketTooSmall) || errors.Is(err, ErrStreamNotFound) {
			return nil
		}
		return err
	}

	// AddPacket returns false until the first keyframe — drop anything
	// before that point, no decoder can use it without a seed.
	if stored := str.buffer.AddPacket(pkt); !stored {
		pkt.Release()
		return nil
	}

	removeFromToAdd := ss.processPendingTracks(str, pkt)
	for _, pt := range removeFromToAdd {
		delete(str.toAdd, pt)
	}

	return ss.processExistingTracks(str, pkt)
}
