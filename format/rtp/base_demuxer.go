package rtp

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/utils/buffer"
	"github.com/ugparu/gomedia/utils/logger"
	"github.com/ugparu/gomedia/utils/sdp"
)

const (
	rtspHeaderSize     = 4
	paddingBit         = 5
	extensionBit       = 4
	control0           = 0x0f
	control1           = 0x1f
	rtpHeaderSize      = 12
	rtcpSenderReport   = 200
	rtcpReceiverReport = 201
	nalnoIDR           = 1
	nalIDR             = 5
	nalSPS             = 7
	nalPPS             = 8
	nalUnitDel         = 9
	nalReserved        = 23
	nalSTAPA           = 24
	nalLFUA            = 28
	clockrate          = 90
)

type baseDemuxer struct {
	rdr     io.Reader
	sdp     sdp.Media
	payload buffer.Buffer
	offset  int
	end     int

	timestamp uint32
	index     uint8
	log       logger.Logger

	// ring is non-nil when WithRingBuffer or WithCalculatedRingBuffer is used.
	// Packet data is carved directly from the current slab; when the slab is
	// full a new one is created automatically and the old one is GC'd once all
	// outstanding SlotHandles have been released by consumers.
	ring *buffer.GrowingRingAlloc
}

type DemuxerOption func(*baseDemuxer)

// WithLogger sets the logger for the demuxer.
func WithLogger(l logger.Logger) DemuxerOption {
	return func(d *baseDemuxer) { d.log = l }
}

// WithRingBuffer enables the growing ring allocator with the given initial byte
// capacity. The ring grows automatically when full; the initial size is a hint,
// not a hard limit.
func WithRingBuffer(size int, opts ...buffer.RingAllocOption) DemuxerOption {
	return func(d *baseDemuxer) {
		d.ring = buffer.NewGrowingRingAlloc(size, opts...)
	}
}

func newBaseDemuxer(rdr io.Reader, sdp sdp.Media, index uint8, opts ...DemuxerOption) *baseDemuxer {
	bd := &baseDemuxer{
		rdr:     rdr,
		sdp:     sdp,
		payload: buffer.Get(rtspHeaderSize),
		index:   index,
		log:     logger.Default,
	}
	for _, opt := range opts {
		opt(bd)
	}
	return bd
}

func (d *baseDemuxer) Demux() (codecs gomedia.CodecParametersPair, err error) {
	return
}

func (d *baseDemuxer) ReadPacket() (pkt gomedia.Packet, err error) {
	if _, err = io.ReadFull(d.rdr, d.payload.Data()[:rtspHeaderSize]); err != nil {
		return
	}

	length := int(binary.BigEndian.Uint16(d.payload.Data()[2:])) //nolint:mnd // RTSP interleaved frame length field at bytes 2-3 per RFC 2326 §10.12
	if length < rtpHeaderSize {
		return nil, fmt.Errorf("RTP incorrect packet size %v", length)
	}
	length += rtspHeaderSize

	d.payload.Resize(int(length))
	if _, err = io.ReadFull(d.rdr, d.payload.Data()[rtspHeaderSize:]); err != nil {
		return nil, err
	}

	if d.isRTCPPacket() {
		err = io.EOF
		return
	}

	firstByte := d.payload.Data()[4]
	padding := (firstByte>>paddingBit)&1 == 1
	extension := (firstByte>>extensionBit)&1 == 1
	csrcCnt := int(firstByte & control0)
	d.timestamp = binary.BigEndian.Uint32(d.payload.Data()[8:12])

	d.offset = rtpHeaderSize
	d.end = len(d.payload.Data())

	if d.end-d.offset >= 4*csrcCnt {
		d.offset += 4 * csrcCnt //nolint:mnd
	}

	if extension && len(d.payload.Data()) < 4+d.offset+2+2 {
		return
	}
	if extension && d.end-d.offset >= 4 {
		extLen := 4 * int(binary.BigEndian.Uint16(d.payload.Data()[4+d.offset+2:])) //nolint:mnd
		d.offset += 4
		if d.end-d.offset >= extLen {
			d.offset += extLen
		}
	}

	if padding && d.end-d.offset > 0 {
		paddingLen := int(d.payload.Data()[d.end-1])
		if d.end-d.offset >= paddingLen {
			d.end -= paddingLen
		}
	}

	d.offset += 4

	return
}

func (d *baseDemuxer) Close() {
}

func (d *baseDemuxer) isRTCPPacket() bool {
	rtcpPacketType := d.payload.Data()[5]
	return rtcpPacketType == rtcpSenderReport || rtcpPacketType == rtcpReceiverReport
}
