package rtp

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/utils/buffer"
	"github.com/ugparu/gomedia/utils/sdp"
)

const (
	headerSize         = 4
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
	rdr       io.Reader
	sdp       sdp.Media
	payload   buffer.PooledBuffer
	offset    int
	end       int
	timestamp uint32
	index     uint8
}

func newBaseDemuxer(rdr io.Reader, sdp sdp.Media, index uint8) *baseDemuxer {
	return &baseDemuxer{
		rdr:       rdr,
		sdp:       sdp,
		payload:   buffer.Get(headerSize),
		offset:    0,
		end:       0,
		timestamp: 0,
		index:     index,
	}
}

func (d *baseDemuxer) Demux() (codecs gomedia.CodecParametersPair, err error) {
	return
}

func (d *baseDemuxer) ReadPacket() (pkt gomedia.Packet, err error) {
	var n int
	if n, err = d.rdr.Read(d.payload.Data()[:4]); err != nil {
		return
	}

	if n < headerSize-1 {
		err = io.EOF
		return
	}

	length := int32(binary.BigEndian.Uint16(d.payload.Data()[2:]))
	if length > 65535 || length < 12 {
		return nil, fmt.Errorf("RTP incorrect packet size %v", length)
	}
	length += headerSize

	d.payload.Resize(int(length))
	if n, err = d.rdr.Read(d.payload.Data()[headerSize:]); err != nil {
		return nil, err
	}
	if n < headerSize {
		err = io.EOF
		return
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
	d.payload.Release()
}

func (d *baseDemuxer) isRTCPPacket() bool {
	rtcpPacketType := d.payload.Data()[5]
	return rtcpPacketType == rtcpSenderReport || rtcpPacketType == rtcpReceiverReport
}
