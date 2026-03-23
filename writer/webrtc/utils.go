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

// minPktSz is the minimum packet size constant.
const minPktSz = 5
const correctionStep = time.Millisecond * 3

// peerURL represents the information associated with a peer track, including token and URL.
type peerURL struct {
	*peerTrack        // Peer track information.
	Token      string // Token associated with the peer.
	URL        string // URL associated with the peer track.
}

// dataChanReq represents a request for changing settings or state.
type dataChanReq struct {
	Token   string `json:"token"`   // Token associated with the request.
	Command string `json:"command"` // Command indicating the type of change.
	Message string `json:"message"` // Additional message or information associated with the change.
}

// codecReq represents a request related to video codec settings.
type codecReq struct {
	Token   string `json:"token"`   // Token associated with the request.
	Command string `json:"command"` // Command indicating the type of codec-related change.
	Message codec  `json:"message"` // Codec-specific information or settings associated with the change.
}

// codec represents information about a video codec and its associated resolutions.
type codec struct {
	Type        string       `json:"type"`        // Type of the video codec.
	Resolutions []resolution `json:"resolutions"` // Resolutions supported by the codec.
}

// resolution represents the width, height, and URL of a video resolution.
type resolution struct {
	URL    string `json:"url"`    // URL associated with the resolution.
	Width  int    `json:"width"`  // Width of the video resolution.
	Height int    `json:"height"` // Height of the video resolution.
	Codec  string `json:"codec"`  // Codec identifier string.
}

// resp represents a response with a token, message, and status.
type resp struct {
	Token   string `json:"token"`   // Token associated with the response.
	Message string `json:"message"` // Message content of the response.
	Status  int    `json:"status"`  // Status code of the response.
}

// peerTrack represents a combination of a WebRTC peer connection, a track, and a data channel.
type peerTrack struct {
	*webrtc.PeerConnection // WebRTC peer connection.
	log                    logger.Logger
	vt                     *webrtc.TrackLocalStaticSample
	at                     *webrtc.TrackLocalStaticSample
	targetURL              string
	aChan                  chan gomedia.AudioPacket
	aBuf                   buffer.PooledBuffer
	vChan                  chan gomedia.VideoPacket
	vBuf                   buffer.PooledBuffer
	vflush                 chan struct{}
	aflush                 chan struct{}
	delay                  time.Duration
	done                   chan struct{}
	*webrtc.DataChannel    // Data channel associated with the peer.
}

func writeVideoPacketsToPeer(pt *peerTrack,
	vflush chan struct{},
	vChan chan gomedia.VideoPacket,
	vt *webrtc.TrackLocalStaticSample, vBuf buffer.PooledBuffer, delay time.Duration) {
	defer vBuf.Release()
	last := time.Now()
	naluBuf := make([][]byte, 0, 8) //nolint:mnd // typical frame has 1-5 NALUs
	processPkt := func(pkt gomedia.VideoPacket) {
		defer pkt.Release()

		// Split NALUs and calculate total size
		var nalus [][]byte
		var nalusSize int
		nalus, _ = nal.SplitNALUs(pkt.Data(), naluBuf)
		for _, nalu := range nalus {
			nalusSize += 4 + len(nalu) // start code (4 bytes) + nalu data
		}

		// Resize vBuf once to fit all data; write codec headers directly into it
		// to avoid an intermediate heap allocation on every keyframe.
		totalSize := nalusSize
		if pkt.IsKeyFrame() {
			totalSize += codecParametersSize(pkt.CodecParameters())
		}
		vBuf.Resize(totalSize)

		// Write codec params (SPS/PPS/VPS) then NALUs directly into vBuf.
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
						// Packet with new URL is being sent to WebRTC
						processPkt(pkt)
						url = pktURL
						break loop
					}
					pkt.Release() // stale same-URL packet drained during flush
				default:
					break loop
				}
			}
		case pkt := <-vChan:
			pktURL := pkt.SourceID()
			if url == "" {
				url = pktURL
			} else if pktURL != url {
				// Packet with new URL is being sent to WebRTC
				url = pktURL
			}
			processPkt(pkt)
		}
	}
}

func writeAudioPacketsToPeer(pt *peerTrack, aflush chan struct{}, aChan chan gomedia.AudioPacket, at *webrtc.TrackLocalStaticSample, aBuf buffer.PooledBuffer, delay time.Duration) {
	defer aBuf.Release()
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
						// Packet with new URL is being sent to WebRTC
						processPkt(pkt)
						url = pktURL
						break loop
					}
					pkt.Release() // stale same-URL packet drained during flush
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
