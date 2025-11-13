package webrtc

import (
	"bytes"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/h264"
	"github.com/ugparu/gomedia/codec/h265"
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

func writeVideoPacketsToPeer(peer *peerTrack) {
	last := time.Now()
	for {
		select {
		case <-peer.done:
			logger.Infof(peer, "Video packets to peer done")
			return
		case <-peer.flush:
		loop:
			for {
				select {
				case <-peer.vBuf:
				case <-peer.aBuf:
				default:
					break loop
				}
			}
		case pkt := <-peer.vBuf:
			sample := createSampleFromPacket(pkt)
			if pkt.IsKeyFrame() {
				sample.Data = appendCodecParameters(pkt.CodecParameters())
			}

			nalus, _ := nal.SplitNALUs(pkt.Data())
			for _, nalu := range nalus {
				sample.Data = append(sample.Data, append([]byte{0, 0, 0, 1}, nalu...)...)
			}

			if err := peer.vt.WriteSample(sample); err != nil {
				logger.Errorf(peer, "Error writing video sample: %v", err)
			}

			sleep := pkt.Duration() - time.Since(last) - time.Millisecond
			if len(peer.vBuf) > bufLen {
				sleep -= time.Millisecond * bufCorStep
			} else if len(peer.vBuf) < bufLen {
				sleep += time.Millisecond * bufCorStep
			}

			if sleep > 0 {
				time.Sleep(sleep)
			} else {
				logger.Warningf(peer, "Buffer sleep time is negative: %v", sleep)
			}

			last = time.Now()
		}
	}
}

func writeAudioPacketsToPeer(peer *peerTrack) {
	for {
		select {
		case <-peer.done:
			logger.Infof(peer, "Audio packets to peer done")
			return
		case pkt := <-peer.aBuf:
			sample := createSampleFromPacket(pkt)
			if err := peer.at.WriteSample(sample); err != nil {
				logger.Errorf(peer, "Error writing audio sample: %v", err)
			}
		}
	}
}

func createSampleFromPacket(pkt gomedia.Packet) media.Sample {
	return media.Sample{
		Data:               pkt.Data(),
		Timestamp:          pkt.StartTime(),
		Duration:           pkt.Duration(),
		PacketTimestamp:    uint32(pkt.Timestamp()), //nolint:gosec
		PrevDroppedPackets: 0,
		Metadata:           nil,
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
