package rtp

import (
	"fmt"
	"io"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/opus"
	"github.com/ugparu/gomedia/utils/logger"
	"github.com/ugparu/gomedia/utils/sdp"
)

// opusMuxer performs RTP packetization of Opus audio (RFC 7587):
// one Opus packet per RTP packet, payload is the raw Opus frame.
type opusMuxer struct {
	*baseMuxer
}

// NewOpusMuxer constructs an RTP muxer for Opus audio.
func NewOpusMuxer(w io.Writer, media sdp.Media, channel uint8, log logger.Logger) *opusMuxer {
	return &opusMuxer{baseMuxer: newBaseMuxer(w, media, channel, 0, log)}
}

// WritePacket writes a single Opus packet as one RTP packet.
func (m *opusMuxer) WritePacket(pkt gomedia.AudioPacket) error {
	op, ok := pkt.(*opus.Packet)
	if !ok {
		return fmt.Errorf("rtp: expected *opus.Packet, got %T", pkt)
	}

	data := op.Data()
	if len(data) == 0 {
		return nil
	}

	return m.writeRTP(data, op.Timestamp(), true)
}
