package rtp

import (
	"bytes"
	"io"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/h265"
	"github.com/ugparu/gomedia/utils/buffer"
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
	slicedHandle    *buffer.SlotHandle // ring handle for slicedPacket; nil = heap
}

// addPacket allocates [4-byte size | nalU] from the ring (or heap), creates a
// new sliced packet for the NAL unit, and moves the previous sliced packet into
// the ready queue.
func (d *h265Demuxer) addPacket(nalU []byte, isKeyFrame bool) {
	needed := 4 + len(nalU)

	var data []byte
	var handle *buffer.SlotHandle

	if d.ring != nil {
		data, handle = d.ring.Alloc(needed)
	}
	if data == nil {
		data = make([]byte, needed)
	}

	writeSizePrefix(data, 0, len(nalU))
	copy(data[4:], nalU)

	if d.slicedPacket != nil {
		d.packets = append(d.packets, d.slicedPacket)
	}

	pkt := h265.NewPacket(
		isKeyFrame,
		time.Duration(d.timestamp/clockrate)*time.Millisecond,
		time.Now(),
		data,
		"",
		d.codec,
	)
	pkt.Slot = handle

	d.slicedPacket = pkt
	d.slicedHandle = handle
}

// appendToPacket appends [4-byte size | nalU] to the current sliced packet.
//
// Ring path: if the write cursor is still at the end of the current slot
// (no other allocation happened since addPacket), Extend grows the slot
// in-place — zero extra allocation, zero copy.
//
// Fallback: if the slot cannot be extended (ring full, or another allocation
// intervened), the existing data is copied into a fresh heap buffer, the old
// slot is released, and the new data is appended.
func (d *h265Demuxer) appendToPacket(nalU []byte, isKeyFrame bool) {
	if d.slicedPacket == nil {
		return
	}

	needed := 4 + len(nalU)

	if d.ring != nil && d.slicedHandle != nil {
		if newBuf, ok := d.ring.Extend(d.slicedHandle, needed); ok {
			// In-place extension — update size prefix for the new NAL unit and
			// copy data into the freshly reserved tail bytes.
			offset := len(d.slicedPacket.Buf)
			writeSizePrefix(newBuf, offset, len(nalU))
			copy(newBuf[offset+4:], nalU)
			d.slicedPacket.Buf = newBuf
			d.slicedPacket.IsKeyFrm = d.slicedPacket.IsKeyFrm || isKeyFrame
			return
		}

		// Cannot extend in-place: allocate a new contiguous region and copy.
		oldData := d.slicedPacket.Buf
		newNeeded := len(oldData) + needed

		var newData []byte
		var newHandle *buffer.SlotHandle
		newData, newHandle = d.ring.Alloc(newNeeded)

		if newData == nil {
			newData = make([]byte, newNeeded)
		}
		copy(newData, oldData)
		writeSizePrefix(newData, len(oldData), len(nalU))
		copy(newData[len(oldData)+4:], nalU)

		// Release the old slot now that its data has been copied.
		d.slicedHandle.Release()

		d.slicedPacket.Buf = newData
		d.slicedPacket.Slot = newHandle
		d.slicedHandle = newHandle
		d.slicedPacket.IsKeyFrm = d.slicedPacket.IsKeyFrm || isKeyFrame
		return
	}

	// Heap path (no ring or no handle on sliced packet).
	oldLen := len(d.slicedPacket.Buf)
	buf := make([]byte, oldLen+needed)
	copy(buf, d.slicedPacket.Buf)
	writeSizePrefix(buf, oldLen, len(nalU))
	copy(buf[oldLen+4:], nalU)
	d.slicedPacket.Buf = buf
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
		slicedHandle:    nil,
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
