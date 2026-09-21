package rtp

import (
	"fmt"
	"io"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/pcm"
	"github.com/ugparu/gomedia/utils/logger"
	"github.com/ugparu/gomedia/utils/sdp"
)

// pcmMuxer performs RTP packetization of PCM / PCMA / PCMU audio.
// The payload is the raw sample bytes (RFC 3551 for PCMA/PCMU, RFC 3551 §4.5.11 for L16).
type pcmMuxer struct {
	*baseMuxer
}

// NewPCMMuxer constructs an RTP muxer for PCM family audio codecs.
func NewPCMMuxer(w io.Writer, media sdp.Media, channel uint8, log logger.Logger) *pcmMuxer {
	return &pcmMuxer{baseMuxer: newBaseMuxer(w, media, channel, 0, log)}
}

// WritePacket writes a single PCM access unit as one RTP packet.
func (m *pcmMuxer) WritePacket(pkt gomedia.AudioPacket) error {
	pp, ok := pkt.(*pcm.Packet)
	if !ok {
		return fmt.Errorf("rtp: expected *pcm.Packet, got %T", pkt)
	}

	data := pp.Data()
	if len(data) == 0 {
		return nil
	}

	return m.writeRTP(data, pp.Timestamp(), true)
}
