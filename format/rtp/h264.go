package rtp

import (
	"bytes"
	"errors"
	"io"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/h264"
	"github.com/ugparu/gomedia/utils/logger"
	"github.com/ugparu/gomedia/utils/nal"
	"github.com/ugparu/gomedia/utils/sdp"
)

type h264Demuxer struct {
	hxxxDemuxer
	sps             []byte
	pps             []byte
	codec           *h264.CodecParameters
	packets         []*h264.Packet
	fuStarted       bool
	BufferRTPPacket *bytes.Buffer
}

func NewH264Demuxer(rdr io.Reader, sdp sdp.Media, index uint8, options ...DemuxerOption) gomedia.Demuxer {
	return &h264Demuxer{
		hxxxDemuxer:     *newHxxxDemuxer(rdr, sdp, index, options...),
		sps:             []byte{},
		pps:             []byte{},
		codec:           nil,
		packets:         []*h264.Packet{},
		fuStarted:       false,
		BufferRTPPacket: &bytes.Buffer{},
	}
}

func (d *h264Demuxer) Demux() (codecs gomedia.CodecParametersPair, err error) {
	if len(d.sdp.SpropParameterSets) <= 1 {
		err = errors.New("no valid h264 params found")
		return
	}
	codecData, err := h264.NewCodecDataFromSPSAndPPS(d.sdp.SpropParameterSets[0], d.sdp.SpropParameterSets[1])
	if err != nil {
		return
	}
	codecData.SetStreamIndex(d.index)

	d.sps = d.sdp.SpropParameterSets[0]
	d.pps = d.sdp.SpropParameterSets[1]
	d.codec = &codecData

	codecs.VideoCodecParameters = d.codec

	return
}

// processNALUnit handles a single NAL unit based on its type
func (d *h264Demuxer) processNALUnit(nalU []byte) error {
	naluType := nalU[0] & control1
	switch {
	case naluType >= nalnoIDR && naluType <= 5:
		d.addPacket(nalU, naluType == nalIDR)
	case naluType == nalSPS:
		return d.CodecUpdateSPS(nalU)
	case naluType == nalPPS:
		return d.CodecUpdatePPS(nalU)
	case naluType <= nalReserved:
		logger.Tracef(d, "Unimplemented non-VCL nal type %d", naluType)
	case naluType == nalSTAPA:
		return d.processSTAPA(nalU)
	case naluType == nalLFUA:
		return d.processLFUA()
	case naluType == nalUnitDel:
		// No operation needed
	default:
		logger.Debugf(d, "Currently unsupported NAL type %v", naluType)
	}
	return nil
}

// processSTAPA handles STAP-A NAL units
func (d *h264Demuxer) processSTAPA(nalU []byte) error {
	packet := nalU[1:]
	for len(packet) >= 2 {
		size := int(packet[0])<<8 | int(packet[1]) //nolint:mnd
		if size+2 > len(packet) {
			break
		}
		naluTypefs := packet[2] & control1
		switch {
		case naluTypefs >= nalnoIDR && naluTypefs <= nalIDR:
			d.addPacket(packet[2:size+2], naluTypefs == nalIDR)
		case naluTypefs == nalSPS:
			if err := d.CodecUpdateSPS(packet[2 : size+2]); err != nil {
				return err
			}
		case naluTypefs == nalPPS:
			if err := d.CodecUpdatePPS(packet[2 : size+2]); err != nil {
				return err
			}
		}
		packet = packet[size+2:]
	}
	return nil
}

// processLFUA handles FU-A NAL units
func (d *h264Demuxer) processLFUA() error {
	fuIndicator := d.payload.Data()[d.offset]
	fuHeader := d.payload.Data()[d.offset+1]
	isStart := fuHeader&0x80 != 0 //nolint:mnd
	isEnd := fuHeader&0x40 != 0   //nolint:mnd

	if isStart {
		d.fuStarted = true
		d.BufferRTPPacket.Truncate(0)
		d.BufferRTPPacket.Reset()
		d.BufferRTPPacket.Write([]byte{fuIndicator&0xe0 | fuHeader&0x1f})
	}

	if d.fuStarted {
		d.BufferRTPPacket.Write(d.payload.Data()[d.offset+2 : d.end])
		if isEnd {
			return d.finalizeFUAPacket()
		}
	}
	return nil
}

// finalizeFUAPacket processes the completed FU-A packet
func (d *h264Demuxer) finalizeFUAPacket() error {
	d.fuStarted = false
	naluTypef := d.BufferRTPPacket.Bytes()[0] & control1

	if naluTypef == 7 || naluTypef == 9 {
		bufered, _ := nal.SplitNALUs(append([]byte{0, 0, 0, 1}, d.BufferRTPPacket.Bytes()...))
		for _, v := range bufered {
			naluTypefs := v[0] & control1
			switch {
			case naluTypefs == nalIDR:
				d.BufferRTPPacket.Reset()
				d.BufferRTPPacket.Write(v)
				naluTypef = nalIDR
			case naluTypefs == nalSPS:
				if err := d.CodecUpdateSPS(v); err != nil {
					return err
				}
			case naluTypefs == nalPPS:
				if err := d.CodecUpdatePPS(v); err != nil {
					return err
				}
			}
		}
	}

	d.addPacket(d.BufferRTPPacket.Bytes(), naluTypef == nalIDR)
	return nil
}

// addPacket creates and adds a new packet to the packet queue
func (d *h264Demuxer) addPacket(nalU []byte, isKeyFrame bool) {
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

	pkt := h264.NewPacket(isKeyFrame,
		time.Duration(d.timestamp)*time.Millisecond/time.Duration(clockrate), time.Now(),
		data, "", d.codec)
	d.packets = append(d.packets, pkt)
}

func (d *h264Demuxer) ReadPacket() (pkt gomedia.Packet, err error) {
	if len(d.packets) > 0 {
		pkt = d.packets[0]
		d.packets = d.packets[1:]
		return
	}

	if _, err = d.hxxxDemuxer.ReadPacket(); err != nil {
		return
	}

	for _, nalU := range d.nals {
		if err = d.processNALUnit(nalU); err != nil {
			return
		}
	}

	if len(d.packets) > 0 {
		pkt = d.packets[0]
		d.packets = d.packets[1:]
	}

	return
}

func (d *h264Demuxer) CodecUpdateSPS(val []byte) (err error) {
	if bytes.Equal(val, d.sps) {
		return
	}

	copy(d.sps, val)

	var codec h264.CodecParameters
	if codec, err = h264.NewCodecDataFromSPSAndPPS(d.sps, d.pps); err != nil {
		return
	}
	codec.SetStreamIndex(d.index)

	d.codec = &codec
	return
}

func (d *h264Demuxer) CodecUpdatePPS(val []byte) (err error) {
	if bytes.Equal(val, d.pps) {
		return
	}

	copy(d.pps, val)

	var codec h264.CodecParameters
	if codec, err = h264.NewCodecDataFromSPSAndPPS(d.sps, d.pps); err != nil {
		return
	}
	codec.SetStreamIndex(d.index)

	d.codec = &codec
	return
}
