package webrtc

import (
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/h264"
	"github.com/ugparu/gomedia/codec/h265"
	"github.com/ugparu/gomedia/utils/buffer"
	"github.com/ugparu/gomedia/utils/logger"
	"github.com/ugparu/gomedia/utils/nal"
)

const minPktSz = 5

// correctionStep is how far each packet's duration is nudged per frame to
// steer the RTP timeline back toward the configured delay target without jumps.
const correctionStep = time.Millisecond * 3

// forceSkipLag / forceSkipQueueDepth gate the emergency keyframe skip.
// Soft catch-up (paceSleep=0 while late) is preferred because GOPs can be
// multi-second; forced skip only fires when we are already late AND the peer
// queue is still deep — i.e. WriteSample cannot drain as fast as packets arrive.
// forceSkipTarget is how close to the delay target a force-skip tries to land.
const (
	forceSkipLag        = 1500 * time.Millisecond
	forceSkipTarget     = 500 * time.Millisecond
	forceSkipQueueDepth = 50 // ~2s at 25fps; well below the 500-slot peer chan
)

// packetLag is how far behind the configured delay target pkt currently sits.
// Positive means late; negative means early.
func packetLag(start time.Time, delay time.Duration) time.Duration {
	return time.Since(start) - delay
}

// paceSleep returns how long the writer should wait before the next sample.
// When late (miss > 0) it returns 0 so the backlog drains as fast as
// WriteSample allows — no keyframe skip (GOP may be several seconds).
// When on time / early it paces by the sample duration as before.
func paceSleep(duration, elapsed, miss time.Duration) time.Duration {
	if miss > 0 {
		return 0
	}
	sleep := duration - elapsed
	if sleep < 0 {
		return 0
	}
	return sleep
}

// needForceSkip reports whether soft catch-up is losing ground and a keyframe
// jump is required. queueLen is len(chan) after the current packet was taken.
func needForceSkip(miss time.Duration, queueLen int) bool {
	return miss >= forceSkipLag && queueLen >= forceSkipQueueDepth
}

// forceSkipVideo drops queued packets until it holds a keyframe within maxMiss
// of the delay target (or the newest keyframe if all are still late). Intervening
// deltas are discarded; once a good keyframe is found the rest of the channel is
// left intact. Returns nil when no keyframe was available.
func forceSkipVideo(pkt gomedia.VideoPacket, vChan <-chan gomedia.VideoPacket, delay, maxMiss time.Duration) (gomedia.VideoPacket, int) {
	dropped := 0
	var best gomedia.VideoPacket

	consider := func(p gomedia.VideoPacket) {
		if p.IsKeyFrame() {
			if best != nil {
				best.Release()
				dropped++
			}
			best = p
			return
		}
		p.Release()
		dropped++
	}

	consider(pkt)

	for {
		if best != nil && packetLag(best.StartTime(), delay) <= maxMiss {
			return best, dropped
		}
		select {
		case next := <-vChan:
			consider(next)
		default:
			return best, dropped
		}
	}
}

// forceSkipAudio drops late audio until one is within maxMiss of the delay
// target, or returns the newest packet left in the channel.
func forceSkipAudio(pkt gomedia.AudioPacket, aChan <-chan gomedia.AudioPacket, delay, maxMiss time.Duration) (gomedia.AudioPacket, int) {
	dropped := 0
	cur := pkt
	for {
		if packetLag(cur.StartTime(), delay) <= maxMiss {
			return cur, dropped
		}
		select {
		case next := <-aChan:
			cur.Release()
			dropped++
			cur = next
		default:
			return cur, dropped
		}
	}
}

type peerURL struct {
	*peerTrack
	Token string
	URL   string
}

type dataChanReq struct {
	Token   string `json:"token"`
	Command string `json:"command"`
	Message string `json:"message"`
}

type codecReq struct {
	Token   string `json:"token"`
	Command string `json:"command"`
	Message codec  `json:"message"`
}

type codec struct {
	Type        string       `json:"type"`
	Resolutions []resolution `json:"resolutions"`
}

type resolution struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Codec  string `json:"codec"`
}

type resp struct {
	Token   string `json:"token"`
	Message string `json:"message"`
	Status  int    `json:"status"`
}

type peerTrack struct {
	*webrtc.PeerConnection
	log       logger.Logger
	vt        *webrtc.TrackLocalStaticSample
	at        *webrtc.TrackLocalStaticSample
	targetURL string
	aChan     chan gomedia.AudioPacket
	aBuf      buffer.Buffer
	vChan     chan gomedia.VideoPacket
	vBuf      buffer.Buffer
	vflush    chan struct{}
	aflush    chan struct{}
	delay     time.Duration
	done      chan struct{}
	*webrtc.DataChannel
}

func writeVideoPacketsToPeer(pt *peerTrack,
	vflush chan struct{},
	vChan chan gomedia.VideoPacket,
	vt *webrtc.TrackLocalStaticSample, vBuf buffer.Buffer, delay time.Duration) {
	last := time.Now()
	naluBuf := make([][]byte, 0, 8) //nolint:mnd // typical frame has 1-5 NALUs
	processPkt := func(pkt gomedia.VideoPacket) {
		miss := packetLag(pkt.StartTime(), delay)
		if needForceSkip(miss, len(vChan)) {
			var n int
			pkt, n = forceSkipVideo(pkt, vChan, delay, forceSkipTarget)
			if n > 0 {
				pt.log.Debugf(pt, "webrtc video force-skip: dropped %d packets (lag was %v, queue %d)", n, miss, len(vChan))
			}
			if pkt == nil {
				return
			}
			last = time.Now()
			miss = packetLag(pkt.StartTime(), delay)
		}
		defer pkt.Release()

		var nalus [][]byte
		var nalusSize int
		nalus, _ = nal.SplitNALUs(pkt.Data(), naluBuf)
		for _, nalu := range nalus {
			nalusSize += 4 + len(nalu) // 4-byte start code + NALU payload
		}

		// Single Resize per frame; codec params go in the same buffer so
		// keyframes don't incur an extra heap allocation.
		totalSize := nalusSize
		if pkt.IsKeyFrame() {
			totalSize += codecParametersSize(pkt.CodecParameters())
		}
		vBuf.Resize(totalSize)

		offset := 0
		if pkt.IsKeyFrame() {
			offset = writeCodecParameters(vBuf.Data(), pkt.CodecParameters())
		}
		for _, nalu := range nalus {
			offset += copy(vBuf.Data()[offset:], []byte{0, 0, 0, 1})
			offset += copy(vBuf.Data()[offset:], nalu)
		}

		if miss > 0 {
			pkt.SetDuration(pkt.Duration() - correctionStep)
		} else if miss < 0 {
			pkt.SetDuration(pkt.Duration() + correctionStep)
		}

		sample := media.Sample{
			Data:               vBuf.Data(),
			Timestamp:          pkt.StartTime(),
			Duration:           pkt.Duration(),
			PacketTimestamp:    0,
			PrevDroppedPackets: 0,
			Metadata:           nil,
		}

		if err := vt.WriteSample(sample); err != nil {
			pt.log.Errorf(pt, "Error writing video sample: %v", err)
		}

		if sleep := paceSleep(pkt.Duration(), time.Since(last), miss); sleep > 0 {
			time.Sleep(sleep)
		}

		last = time.Now()
	}
	var url string

	for {
		select {
		case <-pt.done:
			return
		case <-vflush:
		loop:
			for {
				select {
				case pkt := <-vChan:
					pktURL := pkt.SourceID()
					if url == "" {
						url = pktURL
					}
					if pktURL != url {
						// First packet after a stream switch — emit it, then exit
						// the drain so subsequent packets are rendered normally.
						processPkt(pkt)
						url = pktURL
						break loop
					}
					pkt.Release()
				default:
					break loop
				}
			}
		case pkt := <-vChan:
			pktURL := pkt.SourceID()
			if url == "" {
				url = pktURL
			} else if pktURL != url {
				url = pktURL
			}
			processPkt(pkt)
		}
	}
}

func writeAudioPacketsToPeer(pt *peerTrack, aflush chan struct{}, aChan chan gomedia.AudioPacket, at *webrtc.TrackLocalStaticSample, aBuf buffer.Buffer, delay time.Duration) {
	last := time.Now()
	processPkt := func(pkt gomedia.AudioPacket) {
		miss := packetLag(pkt.StartTime(), delay)
		if needForceSkip(miss, len(aChan)) {
			var n int
			pkt, n = forceSkipAudio(pkt, aChan, delay, forceSkipTarget)
			if n > 0 {
				pt.log.Debugf(pt, "webrtc audio force-skip: dropped %d packets (lag was %v, queue %d)", n, miss, len(aChan))
			}
			last = time.Now()
			miss = packetLag(pkt.StartTime(), delay)
		}
		defer pkt.Release()
		aBuf.Resize(pkt.Len())
		copy(aBuf.Data(), pkt.Data())

		if miss > 0 {
			pkt.SetDuration(pkt.Duration() - correctionStep)
		} else if miss < 0 {
			pkt.SetDuration(pkt.Duration() + correctionStep)
		}

		sample := media.Sample{
			Data:               aBuf.Data(),
			Timestamp:          pkt.StartTime(),
			Duration:           pkt.Duration(),
			PacketTimestamp:    0,
			PrevDroppedPackets: 0,
			Metadata:           nil,
		}

		err := at.WriteSample(sample)
		if err != nil {
			pt.log.Errorf(pt, "Error writing audio sample: %v", err)
		}

		if sleep := paceSleep(pkt.Duration(), time.Since(last), miss); sleep > 0 {
			time.Sleep(sleep)
		}

		last = time.Now()
	}

	var url string

	for {
		select {
		case <-pt.done:
			return
		case <-aflush:
		loop:
			for {
				select {
				case pkt := <-aChan:
					pktURL := pkt.SourceID()
					if url == "" {
						url = pktURL
					}
					if pktURL != url {
						processPkt(pkt)
						url = pktURL
						break loop
					}
					pkt.Release()
				default:
					break loop
				}
			}
		case pkt := <-aChan:
			pktURL := pkt.SourceID()
			if pktURL != url {
				url = pktURL
			}
			processPkt(pkt)
		}
	}
}

// codecParametersSize returns the exact number of bytes needed to write the
// SPS/PPS (H.264) or VPS/SPS/PPS (H.265) codec headers with start codes.
func codecParametersSize(codecPar gomedia.CodecParameters) int {
	switch p := codecPar.(type) {
	case *h264.CodecParameters:
		return 4 + len(p.SPS()) + 4 + len(p.PPS())
	case *h265.CodecParameters:
		return 4 + len(p.VPS()) + 4 + len(p.SPS()) + 4 + len(p.PPS())
	}
	return 0
}

// writeCodecParameters writes the SPS/PPS/VPS start-code-prefixed NALUs
// directly into dst without allocating. Returns the number of bytes written.
func writeCodecParameters(dst []byte, codecPar gomedia.CodecParameters) int {
	startCode := [4]byte{0, 0, 0, 1}
	off := 0
	write := func(nalu []byte) {
		off += copy(dst[off:], startCode[:])
		off += copy(dst[off:], nalu)
	}
	switch p := codecPar.(type) {
	case *h264.CodecParameters:
		write(p.SPS())
		write(p.PPS())
	case *h265.CodecParameters:
		write(p.VPS())
		write(p.SPS())
		write(p.PPS())
	}
	return off
}
