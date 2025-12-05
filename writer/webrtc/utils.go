package webrtc

import (
	"bytes"
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

const bufLen = 10
const bufCorStep = 5

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
	vt                     *webrtc.TrackLocalStaticSample
	at                     *webrtc.TrackLocalStaticSample
	aBuf                   chan gomedia.AudioPacket
	vBuf                   chan gomedia.VideoPacket
	flush                  chan struct{}
	delay                  time.Duration
	done                   chan struct{}
	*webrtc.DataChannel    // Data channel associated with the peer.
}

func writeVideoPacketsToPeer(done chan struct{},
	flush chan struct{},
	vBuf chan gomedia.VideoPacket, aBuf chan gomedia.AudioPacket,
	vt *webrtc.TrackLocalStaticSample) {
	last := time.Now()
	for {
		select {
		case <-done:
			logger.Infof(done, "Video packets to peer done")
			return
		case <-flush:
		loop:
			for {
				select {
				case <-vBuf:
				case <-aBuf:
				default:
					break loop
				}
			}
		case pkt := <-vBuf:
			buf := buffer.Get(pkt.Len())
			defer buf.Release()
			pkt.View(func(data []byte) {
				copy(buf.Data(), data)
			})

			sample := media.Sample{
				Data:               buf.Data(),
				Timestamp:          pkt.StartTime(),
				Duration:           pkt.Duration(),
				PacketTimestamp:    uint32(pkt.Timestamp()), //nolint:gosec
				PrevDroppedPackets: 0,
				Metadata:           nil,
			}

			if pkt.IsKeyFrame() {
				sample.Data = appendCodecParameters(pkt.CodecParameters())
			}

			var nalus [][]byte
			pkt.View(func(data []byte) {
				nalus, _ = nal.SplitNALUs(data)
			})
			for _, nalu := range nalus {
				sample.Data = append(sample.Data, append([]byte{0, 0, 0, 1}, nalu...)...)
			}

			if err := vt.WriteSample(sample); err != nil {
				logger.Errorf(done, "Error writing video sample: %v", err)
			}

			sleep := pkt.Duration() - time.Since(last) - time.Millisecond
			if len(vBuf) > bufLen {
				sleep -= time.Millisecond * bufCorStep
			} else if len(vBuf) < bufLen {
				sleep += time.Millisecond * bufCorStep
			}

			if sleep > 0 {
				time.Sleep(sleep)
			} else {
				logger.Warningf(done, "Buffer sleep time is negative: %v", sleep)
			}

			last = time.Now()
		}
	}
}

func writeAudioPacketsToPeer(done chan struct{}, flush chan struct{}, aBuf chan gomedia.AudioPacket, at *webrtc.TrackLocalStaticSample) {
	for {
		select {
		case <-done:
			logger.Infof(done, "Audio packets to peer done")
			return
		case <-flush:
		case pkt := <-aBuf:
			buf := buffer.Get(pkt.Len())
			pkt.View(func(data []byte) {
				copy(buf.Data(), data)
			})

			sample := media.Sample{
				Data:               buf.Data(),
				Timestamp:          pkt.StartTime(),
				Duration:           pkt.Duration(),
				PacketTimestamp:    uint32(pkt.Timestamp()), //nolint:gosec
				PrevDroppedPackets: 0,
				Metadata:           nil,
			}

			err := at.WriteSample(sample)
			buf.Release()
			if err != nil {
				logger.Errorf(done, "Error writing audio sample: %v", err)
			}
		}
	}
}

func appendCodecParameters(codecPar gomedia.CodecParameters) []byte {
	var data []byte

	switch codecPar := codecPar.(type) {
	case *h264.CodecParameters:
		data = append([]byte{0, 0, 0, 1}, bytes.Join([][]byte{codecPar.SPS(), codecPar.PPS()}, []byte{0, 0, 0, 1})...)
	case *h265.CodecParameters:
		data = append([]byte{0, 0, 0, 1},
			bytes.Join([][]byte{codecPar.VPS(), codecPar.SPS(), codecPar.PPS()}, []byte{0, 0, 0, 1})...)
	}

	return data
}
