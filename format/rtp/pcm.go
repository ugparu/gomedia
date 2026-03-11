package rtp

import (
	"io"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/pcm"
	"github.com/ugparu/gomedia/utils/buffer"
	"github.com/ugparu/gomedia/utils/sdp"
)

type alawDemuxer struct {
	baseDemuxer
	*pcm.CodecParameters
}

func NewPCMDemuxer(rdr io.Reader, sdp sdp.Media, index uint8, ct gomedia.CodecType, options ...DemuxerOption) gomedia.Demuxer {
	return &alawDemuxer{
		baseDemuxer: *newBaseDemuxer(rdr, sdp, index, options...),
		CodecParameters: pcm.NewCodecParameters(index, ct,
			uint8(sdp.ChannelCount), uint64(sdp.TimeScale)), //nolint:gosec
	}
}

func (d *alawDemuxer) Demux() (codecs gomedia.CodecParametersPair, err error) {
	codecs.AudioCodecParameters = d.CodecParameters
	return
}

func (d *alawDemuxer) ReadPacket() (pkt gomedia.Packet, err error) {
	if _, err = d.baseDemuxer.ReadPacket(); err != nil {
		return
	}

	needed := d.end - d.offset
	var buf []byte
	var handle *buffer.SlotHandle
	if d.ring != nil {
		buf, handle = d.ring.Alloc(needed)
	}
	if buf == nil {
		buf = make([]byte, needed)
	}
	copy(buf, d.payload.Data()[d.offset:d.end])
	p := pcm.NewPacket(buf, (time.Duration(d.timestamp)*time.Second)/time.Duration(d.sdp.TimeScale),
		"", time.Now(), d.CodecParameters,
		(time.Duration(len(buf))*time.Second)/time.Duration(d.sdp.TimeScale)) //nolint:mnd
	p.Slot = handle
	pkt = p
	return
}
