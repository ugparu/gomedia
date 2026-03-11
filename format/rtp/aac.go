package rtp

import (
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
	packets []*aac.Packet
}

func NewAACDemuxer(rdr io.Reader, sdp sdp.Media, index uint8, options ...DemuxerOption) gomedia.Demuxer {
	par, _ := aac.NewCodecDataFromMPEG4AudioConfigBytes(sdp.Config)
	par.SetStreamIndex(index)
	return &aacDemuxer{
		baseDemuxer:     *newBaseDemuxer(rdr, sdp, index, options...),
		CodecParameters: &par,
		packets:         []*aac.Packet{},
	}
}

func (d *aacDemuxer) Demux() (codecs gomedia.CodecParametersPair, err error) {
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
		auHeadersLength := uint16(0) | (uint16(buf[0]) << 8) | uint16(buf[1])
		auHeadersCount := auHeadersLength >> 4
		framesPayloadOffset := 2 + int(auHeadersCount)<<1
		auHeaders := buf[2:framesPayloadOffset]
		framesPayload := buf[framesPayloadOffset:]

		for range auHeadersCount {
			auHeader := uint16(0) | (uint16(auHeaders[0]) << 8) | uint16(auHeaders[1])
			frameSize := auHeader >> 3
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
