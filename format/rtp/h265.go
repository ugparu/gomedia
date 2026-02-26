package rtp

import (
	"bytes"
	"io"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/h265"
	"github.com/ugparu/gomedia/utils/sdp"
)

type h265Demuxer struct {
	hxxxDemuxer
	sps, pps, vps   []byte
	codec           *h265.CodecParameters
	packets         []*h265.Packet
	BufferRTPPacket *bytes.Buffer
	bufferHasKey    bool
	slicedPacket    *h265.Packet
}

func (d *h265Demuxer) addPacket(nalU []byte, isKeyFrame bool) {
	var data []byte
	if d.ringBuffer != nil {
		if d.ringOffset+4+len(nalU) > d.ringBuffer.Len() {
			d.handleRingOverflow(4 + len(nalU))
		}
		start := d.ringOffset
		copy(d.ringBuffer.Data()[d.ringOffset:], binSize(len(nalU)))
		d.ringOffset += 4
		copy(d.ringBuffer.Data()[d.ringOffset:], nalU)
		d.ringOffset += len(nalU)
		data = d.ringBuffer.Data()[start:d.ringOffset]
	} else {
		data = append(binSize(len(nalU)), nalU...)
	}

	if d.slicedPacket != nil {
		d.packets = append(d.packets, d.slicedPacket)
	}

	d.slicedPacket = h265.NewPacket(
		isKeyFrame,
		time.Duration(d.timestamp/clockrate)*time.Millisecond,
		time.Now(),
		data,
		"",
		d.codec,
	)
}

func (d *h265Demuxer) appendToPacket(nalU []byte, isKeyFrame bool) {
	if d.slicedPacket == nil {
		return
	}

	needed := 4 + len(nalU)

	if d.ringBuffer != nil {
		if d.ringOffset+needed > d.ringBuffer.Len() {
			oldLen := d.slicedPacket.Len()
			oldBuf := make([]byte, oldLen)
			copy(oldBuf, d.slicedPacket.Buf[:oldLen])

			d.handleRingOverflow(oldLen + needed)

			start := d.ringOffset
			copy(d.ringBuffer.Data()[d.ringOffset:], oldBuf)
			d.ringOffset += oldLen
			copy(d.ringBuffer.Data()[d.ringOffset:], binSize(len(nalU)))
			d.ringOffset += 4
			copy(d.ringBuffer.Data()[d.ringOffset:], nalU)
			d.ringOffset += len(nalU)
			d.slicedPacket.Buf = d.ringBuffer.Data()[start:d.ringOffset]
		} else {
			copy(d.ringBuffer.Data()[d.ringOffset:], binSize(len(nalU)))
			d.ringOffset += 4
			copy(d.ringBuffer.Data()[d.ringOffset:], nalU)
			d.ringOffset += len(nalU)
			d.slicedPacket.Buf = d.slicedPacket.Buf[:d.slicedPacket.Len()+needed]
		}
	} else {
		oldLen := d.slicedPacket.Len()
		buf := d.slicedPacket.Buf[:oldLen]
		buf = append(buf, binSize(len(nalU))...)
		buf = append(buf, nalU...)
		d.slicedPacket.Buf = buf
	}

	d.slicedPacket.IsKeyFrm = d.slicedPacket.IsKeyFrm || isKeyFrame
}

func NewH265Demuxer(rdr io.Reader, sdp sdp.Media, index uint8, options ...DemuxerOption) gomedia.Demuxer {
	return &h265Demuxer{
		hxxxDemuxer:     *newHxxxDemuxer(rdr, sdp, index, options...),
		sps:             []byte{},
		pps:             []byte{},
		vps:             []byte{},
		packets:         []*h265.Packet{},
		codec:           nil,
		BufferRTPPacket: &bytes.Buffer{},
		bufferHasKey:    false,
		slicedPacket:    nil,
	}
}

func (d *h265Demuxer) Demux() (codecs gomedia.CodecParametersPair, err error) {
	var codecData h265.CodecParameters
	codecData, err = h265.NewCodecDataFromVPSAndSPSAndPPS(d.sdp.SpropVPS, d.sdp.SpropSPS, d.sdp.SpropPPS)
	if err != nil {
		return
	}
	d.vps = d.sdp.SpropVPS
	d.sps = d.sdp.SpropSPS
	d.pps = d.sdp.SpropPPS
	d.codec = &codecData

	codecs.VideoCodecParameters = d.codec

	return
}

// nolint: mnd
func (d *h265Demuxer) ReadPacket() (pkt gomedia.Packet, err error) {
	if len(d.packets) > 0 {
		pkt = d.packets[0]
		d.packets = d.packets[1:]
		return
	}

	if _, err = d.hxxxDemuxer.ReadPacket(); err != nil {
		return
	}

	for _, nal := range d.nals {
		naluType := (nal[0] >> 1) & 0x3f

		switch naluType {
		case h265.NalUnitVps:
			err = d.CodecUpdateVPS(nal)
		case h265.NalUnitSps:
			err = d.CodecUpdateSPS(nal)
		case h265.NalUnitPps:
			err = d.CodecUpdatePPS(nal)
		case 39:
		case 48:
		case h265.NalFU:
			fuNal := nal[2:]
			isStart := fuNal[0]&0x80 != 0 //nolint:mnd
			isEnd := fuNal[0]&0x40 != 0   //nolint:mnd
			fuNaluType := fuNal[0] & 0x3f

			d.bufferHasKey = d.bufferHasKey || h265.IsKey(fuNaluType)

			fuNal = fuNal[1:]

			switch {
			case isStart:
				d.BufferRTPPacket.Truncate(0)
				d.BufferRTPPacket.Reset()
				d.BufferRTPPacket.Write([]byte{(nal[0] & 0x81) | (fuNaluType << 1), nal[1]})
				d.BufferRTPPacket.Write(fuNal)
			case isEnd:
				d.BufferRTPPacket.Write(fuNal)
				pktData := d.BufferRTPPacket.Bytes()

				if pktData[2]>>7&1 == 1 {
					d.addPacket(pktData, d.bufferHasKey)
				} else {
					d.appendToPacket(pktData, d.bufferHasKey)
				}
				d.bufferHasKey = false
			default:
				d.BufferRTPPacket.Write(fuNal)
			}
		default:
			if nal[2]>>7&1 == 1 {
				d.addPacket(nal, h265.IsKey(naluType))
			} else {
				d.appendToPacket(nal, false)
			}
		}
	}

	if len(d.packets) > 0 {
		pkt = d.packets[0]
		d.packets = d.packets[1:]
	}

	return
}

func (d *h265Demuxer) CodecUpdateSPS(val []byte) (err error) {
	if bytes.Equal(val, d.sps) {
		return
	}
	d.sps = make([]byte, len(val))
	copy(d.sps, val)

	var codec h265.CodecParameters
	if codec, err = h265.NewCodecDataFromVPSAndSPSAndPPS(d.vps, d.sps, d.pps); err != nil {
		return
	}
	codec.SetStreamIndex(d.index)

	d.codec = &codec
	return
}

func (d *h265Demuxer) CodecUpdatePPS(val []byte) (err error) {
	if bytes.Equal(val, d.pps) {
		return
	}
	d.pps = make([]byte, len(val))
	copy(d.pps, val)

	var codec h265.CodecParameters
	if codec, err = h265.NewCodecDataFromVPSAndSPSAndPPS(d.vps, d.sps, d.pps); err != nil {
		return
	}
	codec.SetStreamIndex(d.index)

	d.codec = &codec
	return
}

func (d *h265Demuxer) CodecUpdateVPS(val []byte) (err error) {
	if bytes.Equal(val, d.vps) {
		return
	}
	d.vps = make([]byte, len(val))
	copy(d.vps, val)

	var codec h265.CodecParameters
	if codec, err = h265.NewCodecDataFromVPSAndSPSAndPPS(d.vps, d.sps, d.pps); err != nil {
		return
	}
	codec.SetStreamIndex(d.index)

	d.codec = &codec
	return
}
