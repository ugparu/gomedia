package rtp

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/aac"
	"github.com/ugparu/gomedia/utils/logger"
	"github.com/ugparu/gomedia/utils/sdp"
)

// aacMuxer performs RTP packetization of AAC audio (RFC 3640 mpeg4-generic
// with SizeLength=13, IndexLength=3) on top of baseMuxer.
type aacMuxer struct {
	*baseMuxer
	codec *aac.CodecParameters
	buf   []byte
}

// NewAACMuxer constructs an RTP muxer for AAC audio.
func NewAACMuxer(w io.Writer, media sdp.Media, channel uint8, codec *aac.CodecParameters, log logger.Logger) *aacMuxer {
	return &aacMuxer{
		baseMuxer: newBaseMuxer(w, media, channel, 0, log),
		codec:     codec,
		buf:       make([]byte, 0, 512), //nolint:mnd // typical AAC frame fits well under 512B with AU headers
	}
}

// WritePacket writes a single AAC access unit as one RTP packet.
func (m *aacMuxer) WritePacket(pkt gomedia.AudioPacket) error {
	ap, ok := pkt.(*aac.Packet)
	if !ok {
		return fmt.Errorf("rtp: expected *aac.Packet, got %T", pkt)
	}

	data := ap.Data()
	if len(data) == 0 {
		return nil
	}

	// Accept ADTS-framed payloads defensively; strip the header if present.
	if _, hdrlen, _, _, err := aac.ParseADTSHeader(data); err == nil {
		data = data[hdrlen:]
		if len(data) == 0 {
			return nil
		}
	}

	const maxAUSize = 1<<13 - 1 // 13-bit AU-size field (RFC 3640 §3.3.6)
	if len(data) > maxAUSize {
		return fmt.Errorf("rtp: AAC frame size %d exceeds 13-bit AU-size limit", len(data))
	}

	// AU-headers-length (16 bits, in bits) + one 16-bit AU-header + payload.
	need := 4 + len(data)
	if cap(m.buf) < need {
		m.buf = make([]byte, need)
	} else {
		m.buf = m.buf[:need]
	}

	binary.BigEndian.PutUint16(m.buf[0:2], 16)                   //nolint:mnd // one AU-header = 16 bits
	binary.BigEndian.PutUint16(m.buf[2:4], uint16(len(data)<<3)) //nolint:mnd // size<<3 | index=0
	copy(m.buf[4:], data)

	return m.writeRTP(m.buf, ap.Timestamp(), true)
}
