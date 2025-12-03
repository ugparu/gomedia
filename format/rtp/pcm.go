package rtp

import (
	"io"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/pcm"
	"github.com/ugparu/gomedia/utils/sdp"
)

type alawDemuxer struct {
	baseDemuxer
	*pcm.CodecParameters
}

func NewPCMDemuxer(rdr io.Reader, sdp sdp.Media, index uint8, ct gomedia.CodecType) gomedia.Demuxer {
	return &alawDemuxer{
		baseDemuxer: *newBaseDemuxer(rdr, sdp, index),
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

	buf := make([]byte, d.end-d.offset)
	copy(buf, d.payload.Data()[d.offset:d.end])
	pkt = pcm.NewPacket(buf, (time.Duration(d.timestamp)*time.Second)/time.Duration(d.sdp.TimeScale),
		"", time.Now(), d.CodecParameters,
		(time.Duration(len(buf))*time.Second)/time.Duration(d.sdp.TimeScale)) //nolint:mnd
	return
}
