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
// steer playback back toward the configured delay target without audible jumps.
const correctionStep = time.Millisecond * 3

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

		miss := time.Since(pkt.StartTime()) - delay

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

		sleep := pkt.Duration() - time.Since(last)

		if sleep > 0 {
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
		defer pkt.Release()
		aBuf.Resize(pkt.Len())
		copy(aBuf.Data(), pkt.Data())

		miss := time.Since(pkt.StartTime()) - delay

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

		sleep := pkt.Duration() - time.Since(last)

		if sleep > 0 {
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
