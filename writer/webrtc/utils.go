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
	aChan                  chan gomedia.AudioPacket
	aBuf                   buffer.PooledBuffer
	vChan                  chan gomedia.VideoPacket
	vBuf                   buffer.PooledBuffer
	flush                  chan struct{}
	delay                  time.Duration
	done                   chan struct{}
	*webrtc.DataChannel    // Data channel associated with the peer.
}

func writeVideoPacketsToPeer(done chan struct{},
	flush chan struct{},
	vChan chan gomedia.VideoPacket, aChan chan gomedia.AudioPacket,
	vt *webrtc.TrackLocalStaticSample, vBuf buffer.PooledBuffer) {
	last := time.Now()
	for {
		select {
		case <-done:
			return
		case <-flush:
		loop:
			for {
				select {
				case <-vChan:
				case <-aChan:
				default:
					break loop
				}
			}
		case pkt := <-vChan:
			defer pkt.Close()

			// Get codec parameters for keyframes
			var codecParams []byte
			if pkt.IsKeyFrame() {
				codecParams = appendCodecParameters(pkt.CodecParameters())
			}

			// Split NALUs and calculate total size
			var nalus [][]byte
			var nalusSize int
			pkt.View(func(data buffer.PooledBuffer) {
				nalus, _ = nal.SplitNALUs(data.Data())
				for _, nalu := range nalus {
					nalusSize += 4 + len(nalu) // start code (4 bytes) + nalu data
				}
			})

			// Resize vBuf once to fit all data
			totalSize := len(codecParams) + nalusSize
			vBuf.Resize(totalSize)

			// Copy codec params and NALUs into vBuf
			offset := copy(vBuf.Data(), codecParams)
			for _, nalu := range nalus {
				offset += copy(vBuf.Data()[offset:], []byte{0, 0, 0, 1})
				offset += copy(vBuf.Data()[offset:], nalu)
			}

			sample := media.Sample{
				Data:               vBuf.Data(),
				Timestamp:          pkt.StartTime(),
				Duration:           pkt.Duration(),
				PacketTimestamp:    uint32(pkt.Timestamp()), //nolint:gosec
				PrevDroppedPackets: 0,
				Metadata:           nil,
			}

			if err := vt.WriteSample(sample); err != nil {
				logger.Errorf(done, "Error writing video sample: %v", err)
			}

			sleep := pkt.Duration() - time.Since(last) - time.Millisecond
			if len(vChan) > bufLen {
				sleep -= time.Millisecond * bufCorStep
			} else if len(vChan) < bufLen {
				sleep += time.Millisecond * bufCorStep
			}

			if sleep > 0 {
				time.Sleep(sleep)
			}

			last = time.Now()
		}
	}
}

func writeAudioPacketsToPeer(done chan struct{}, flush chan struct{}, aChan chan gomedia.AudioPacket, at *webrtc.TrackLocalStaticSample, aBuf buffer.PooledBuffer) {
	for {
		select {
		case <-done:
			return
		case <-flush:
		case pkt := <-aChan:
			defer pkt.Close()

			pkt.View(func(data buffer.PooledBuffer) {
				aBuf.Resize(data.Len())
				copy(aBuf.Data(), data.Data())
			})

			sample := media.Sample{
				Data:               aBuf.Data(),
				Timestamp:          pkt.StartTime(),
				Duration:           pkt.Duration(),
				PacketTimestamp:    uint32(pkt.Timestamp()), //nolint:gosec
				PrevDroppedPackets: 0,
				Metadata:           nil,
			}

			err := at.WriteSample(sample)
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
