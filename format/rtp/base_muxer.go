package rtp

import (
	"encoding/binary"
	"fmt"
	"io"
	"math/rand/v2"
	"time"

	"github.com/ugparu/gomedia/utils/logger"
	"github.com/ugparu/gomedia/utils/sdp"
)

// baseMuxer provides low-level RTP packet construction and RTSP-interleaved
// framing over an io.Writer. It mirrors baseDemuxer but in the opposite
// (muxing) direction.
type baseMuxer struct {
	w           io.Writer
	payloadType uint8
	clockRate   uint32
	ssrc        uint32
	sequence    uint16
	channel     uint8
	streamIndex uint8
}

// newBaseMuxer constructs a baseMuxer for a given SDP media description and
// RTSP interleaved channel.
func newBaseMuxer(w io.Writer, media sdp.Media, channel uint8, streamIndex uint8) *baseMuxer {
	m := &baseMuxer{
		w:           w,
		payloadType: uint8(media.PayloadType),
		clockRate:   uint32(media.TimeScale),
		channel:     channel,
		streamIndex: streamIndex,
	}

	// Initialize SSRC and sequence with pseudo-random values to avoid collisions.
	// This is inexpensive and good enough for our purposes.
	m.ssrc = rand.Uint32()
	m.sequence = uint16(rand.UintN(1 << 16)) //nolint:gosec // non-crypto random is sufficient here

	// Fallback to default video clockrate if SDP is incomplete.
	if m.clockRate == 0 {
		m.clockRate = 90000
	}

	return m
}

// writeRTP builds and writes a single RTP packet with the given payload and
// presentation timestamp. The timestamp is expressed as time.Duration and
// converted to the RTP timestamp space using the media clock rate.
func (m *baseMuxer) writeRTP(payload []byte, ts time.Duration, marker bool) error {
	// Convert PTS to RTP timestamp according to clock rate.
	rtpTimestamp := uint32(uint64(ts) * uint64(m.clockRate) / uint64(time.Second))

	// Build RTP header.
	rtpPacketLen := rtpHeaderSize + len(payload)
	buf := make([]byte, rtspHeaderSize+rtpPacketLen)

	// RTSP interleaved header.
	buf[0] = 0x24
	buf[1] = m.channel
	binary.BigEndian.PutUint16(buf[2:4], uint16(rtpPacketLen))

	// RTP fixed header (no CSRC, no extension).
	buf[4] = 0x80 // Version 2, no padding, no extension, CC=0.
	if marker {
		buf[5] = 0x80 | m.payloadType
	} else {
		buf[5] = m.payloadType
	}

	binary.BigEndian.PutUint16(buf[6:8], m.sequence)
	binary.BigEndian.PutUint32(buf[8:12], rtpTimestamp)
	binary.BigEndian.PutUint32(buf[12:16], m.ssrc)

	copy(buf[rtspHeaderSize+rtpHeaderSize:], payload)

	n, err := m.w.Write(buf)
	if err != nil {
		return fmt.Errorf("rtp: write failed for stream %d: %w", m.streamIndex, err)
	}
	if n != len(buf) {
		logger.Warningf(m, "short RTP write: wrote %d of %d bytes", n, len(buf))
	}

	m.sequence++
	return nil
}

func (m *baseMuxer) String() string {
	return fmt.Sprintf("rtp.BaseMuxer{streamIndex=%d, pt=%d, ch=%d}", m.streamIndex, m.payloadType, m.channel)
}
