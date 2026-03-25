package rtp

import (
	"fmt"
	"io"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/aac"
	"github.com/ugparu/gomedia/utils/buffer"
	"github.com/ugparu/gomedia/utils/sdp"
)

type aacDemuxer struct {
	baseDemuxer
	*aac.CodecParameters
	packets  []*aac.Packet
	initErr  error
}

func NewAACDemuxer(rdr io.Reader, sdp sdp.Media, index uint8, options ...DemuxerOption) gomedia.Demuxer {
	par, err := aac.NewCodecDataFromMPEG4AudioConfigBytes(sdp.Config)
	par.SetStreamIndex(index)
	return &aacDemuxer{
		baseDemuxer:     *newBaseDemuxer(rdr, sdp, index, options...),
		CodecParameters: &par,
		packets:         []*aac.Packet{},
		initErr:         err,
	}
}

func (d *aacDemuxer) Demux() (codecs gomedia.CodecParametersPair, err error) {
	if d.initErr != nil {
		err = fmt.Errorf("invalid AAC config: %w", d.initErr)
		return
	}
	codecs.AudioCodecParameters = d.CodecParameters
	return
}

// allocBuf copies src into ring-backed memory when a ring is configured,
// falling back to a heap allocation. The returned handle is nil for heap-backed
// buffers; consumers must call Release() on ring-backed packets.
func (d *aacDemuxer) allocBuf(src []byte) ([]byte, *buffer.SlotHandle) {
	var data []byte
	var handle *buffer.SlotHandle
	if d.ring != nil {
		data, handle = d.ring.Alloc(len(src))
	}
	if data == nil {
		data = make([]byte, len(src))
	}
	copy(data, src)
	return data, handle
}

func (d *aacDemuxer) Close() {
	for _, pkt := range d.packets {
		if pkt.Slot != nil {
			pkt.Slot.Release()
		}
	}
	d.packets = nil
}

// nolint: mnd
func (d *aacDemuxer) ReadPacket() (pkt gomedia.Packet, err error) {
	if len(d.packets) > 0 {
		pkt = d.packets[0]
		d.packets = d.packets[1:]
		return
	}

	if _, err = d.baseDemuxer.ReadPacket(); err != nil {
		return
	}

	buf := d.payload.Data()[d.offset:d.end]

	ts := (time.Duration(d.timestamp) * time.Second) / time.Duration(d.sdp.TimeScale)
	duration := (1024 * time.Second / time.Duration(d.sdp.TimeScale))

	if c, hdrlen, _, _, adtsErr := aac.ParseADTSHeader(buf); adtsErr == nil {
		if d.CodecParameters.Config != c {
			if *d.CodecParameters, err = aac.NewCodecDataFromMPEG4AudioConfig(c); err != nil {
				return
			}
		}
		data, handle := d.allocBuf(buf[hdrlen:])
		p := aac.NewPacket(data, ts, "", time.Now(), d.CodecParameters, duration)
		p.Slot = handle
		d.packets = append(d.packets, p)
	} else {
		if len(buf) < 2 { //nolint:mnd // AU-header section requires at least 2 bytes (RFC 3640 §3.2.1)
			return nil, fmt.Errorf("AAC RTP payload too short for AU-header section: %d bytes", len(buf))
		}
		auHeadersLength := uint16(0) | (uint16(buf[0]) << 8) | uint16(buf[1])
		auHeadersCount := auHeadersLength >> 4
		framesPayloadOffset := 2 + int(auHeadersCount)<<1 //nolint:mnd // 2-byte AU-header section length + 2 bytes per AU-header
		if framesPayloadOffset > len(buf) {
			return nil, fmt.Errorf("AAC AU-header count %d exceeds payload size %d", auHeadersCount, len(buf))
		}
		auHeaders := buf[2:framesPayloadOffset]
		framesPayload := buf[framesPayloadOffset:]

		for range auHeadersCount {
			if len(auHeaders) < 2 { //nolint:mnd // each AU-header is 2 bytes
				break
			}
			auHeader := uint16(0) | (uint16(auHeaders[0]) << 8) | uint16(auHeaders[1])
			frameSize := int(auHeader >> 3) //nolint:mnd // 13-bit AU-size field per RFC 3640 §3.3.6
			if frameSize > len(framesPayload) {
				break
			}
			frame := framesPayload[:frameSize]

			auHeaders = auHeaders[2:]
			framesPayload = framesPayload[frameSize:]
			data, handle := d.allocBuf(frame)
			p := aac.NewPacket(data, ts, "", time.Now(), d.CodecParameters, duration)
			p.Slot = handle
			d.packets = append(d.packets, p)
			ts += duration
		}
	}

	if len(d.packets) > 0 {
		pkt = d.packets[0]
		d.packets = d.packets[1:]
	}

	return
}
