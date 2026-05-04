package rtp

import (
	"encoding/binary"
	"fmt"
	"io"
	"math/rand/v2"
	"time"

	"github.com/ugparu/gomedia/utils/buffer"
	"github.com/ugparu/gomedia/utils/logger"
	"github.com/ugparu/gomedia/utils/sdp"
)

const rtpBufInitSize = 1500 //nolint:mnd // covers typical MTU-sized RTP packet with RTSP interleaved header

// baseMuxer provides low-level RTP packet construction and RTSP-interleaved
// framing over an io.Writer. It mirrors baseDemuxer but in the opposite
// (muxing) direction.
type baseMuxer struct {
	w           io.Writer
	log         logger.Logger
	buf         buffer.Buffer
	payloadType uint8
	clockRate   uint32
	ssrc        uint32
	sequence    uint16
	channel     uint8
	streamIndex uint8
}

// newBaseMuxer constructs a baseMuxer for a given SDP media description and
// RTSP interleaved channel.
func newBaseMuxer(w io.Writer, media sdp.Media, channel uint8, streamIndex uint8, log logger.Logger) *baseMuxer {
	m := &baseMuxer{
		w:           w,
		log:         log,
		buf:         buffer.Get(rtpBufInitSize),
		payloadType: uint8(media.PayloadType),
		clockRate:   uint32(media.TimeScale),
		channel:     channel,
		streamIndex: streamIndex,
	}

	// Randomize SSRC and sequence to avoid collisions with concurrent senders (RFC 3550 §5.1).
	m.ssrc = rand.Uint32()
	m.sequence = uint16(rand.UintN(1 << 16)) //nolint:gosec // non-crypto random is sufficient here

	if m.clockRate == 0 {
		m.clockRate = 90000 // standard video clock rate (RFC 3551) when SDP omits it
	}

	return m
}

// writeRTP frames one RTP packet inside an RTSP interleaved header and writes
// it to the underlying io.Writer. ts (presentation time) is converted into the
// media's RTP timestamp space using the SDP-declared clock rate.
func (m *baseMuxer) writeRTP(payload []byte, ts time.Duration, marker bool) error {
	rtpTimestamp := uint32(uint64(ts) * uint64(m.clockRate) / uint64(time.Second))

	rtpPacketLen := rtpHeaderSize + len(payload)
	size := rtspHeaderSize + rtpPacketLen
	m.buf.Resize(size)
	buf := m.buf.Data()

	// RTSP interleaved header (RFC 2326 §10.12): '$' | channel | length.
	buf[0] = 0x24
	buf[1] = m.channel
	binary.BigEndian.PutUint16(buf[2:4], uint16(rtpPacketLen))

	// RTP fixed header (no CSRC, no extension) — RFC 3550 §5.1.
	buf[4] = 0x80 // V=2, P=0, X=0, CC=0
	if marker {
		buf[5] = 0x80 | m.payloadType
	} else {
		buf[5] = m.payloadType
	}

	binary.BigEndian.PutUint16(buf[6:8], m.sequence)
	binary.BigEndian.PutUint32(buf[8:12], rtpTimestamp)
	binary.BigEndian.PutUint32(buf[12:16], m.ssrc)

	copy(buf[rtspHeaderSize+rtpHeaderSize:], payload)

	n, err := m.w.Write(buf[:size])
	if err != nil {
		return fmt.Errorf("rtp: write failed for stream %d: %w", m.streamIndex, err)
	}
	if n != size {
		m.log.Warningf(m, "short RTP write: wrote %d of %d bytes", n, size)
	}

	m.sequence++
	return nil
}

func (m *baseMuxer) String() string {
	return fmt.Sprintf("rtp.BaseMuxer{streamIndex=%d, pt=%d, ch=%d}", m.streamIndex, m.payloadType, m.channel)
}
