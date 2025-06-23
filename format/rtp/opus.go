package rtp

import (
	"io"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/opus"
	"github.com/ugparu/gomedia/utils/sdp"
)

type opusDemuxer struct {
	audioDemuxer
	*opus.CodecParameters
}

// nolint: mnd
func NewOPUSDemuxer(rdr io.Reader, sdp sdp.Media, index uint8) gomedia.Demuxer {
	var cl gomedia.ChannelLayout
	switch sdp.ChannelCount {
	case 1:
		cl = gomedia.ChMono
	case 2:
		cl = gomedia.ChStereo
	default:
		cl = gomedia.ChMono
	}
	par := opus.NewCodecParameters(index, cl, uint64(sdp.TimeScale)) //nolint:gosec
	par.SetStreamIndex(index)

	return &opusDemuxer{
		audioDemuxer:    *newAudioDemuxer(rdr, sdp, index),
		CodecParameters: par,
	}
}

func (d *opusDemuxer) Demux() (codecs gomedia.CodecParametersPair, err error) {
	codecs.AudioCodecParameters = d.CodecParameters
	return
}

// nolint: mnd
func (d *opusDemuxer) ReadPacket() (pkt gomedia.Packet, err error) {
	if _, err = d.audioDemuxer.ReadPacket(); err != nil {
		return
	}

	buf := make([]byte, d.end-d.offset)
	copy(buf, d.payload[d.offset:d.end])
	pkt = opus.NewPacket(buf, (time.Duration(d.timestamp)*time.Second)/time.Duration(d.sdp.TimeScale), "", time.Now(),
		d.CodecParameters, 20*time.Millisecond)
	return
}
