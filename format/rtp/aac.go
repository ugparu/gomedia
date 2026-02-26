package rtp

import (
	"io"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/aac"
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
		d.packets = append(d.packets, aac.NewPacket(buf[hdrlen:], ts, "", time.Now(), d.CodecParameters, duration))
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
			frameBuf := make([]byte, len(frame))
			copy(frameBuf, frame)
			d.packets = append(d.packets, aac.NewPacket(frameBuf, ts, "", time.Now(), d.CodecParameters, duration))
			ts += duration
		}
	}

	if len(d.packets) > 0 {
		pkt = d.packets[0]
		d.packets = d.packets[1:]
	}

	return
}
