package rtp

import (
	"io"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/opus"
	"github.com/ugparu/gomedia/utils/buffer"
	"github.com/ugparu/gomedia/utils/sdp"
)

type opusDemuxer struct {
	baseDemuxer
	*opus.CodecParameters
}

// nolint: mnd
func NewOPUSDemuxer(rdr io.Reader, sdp sdp.Media, index uint8, options ...DemuxerOption) gomedia.Demuxer {
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
		baseDemuxer:     *newBaseDemuxer(rdr, sdp, index, options...),
		CodecParameters: par,
	}
}

func (d *opusDemuxer) Demux() (codecs gomedia.CodecParametersPair, err error) {
	codecs.AudioCodecParameters = d.CodecParameters
	return
}

// nolint: mnd
func (d *opusDemuxer) ReadPacket() (pkt gomedia.Packet, err error) {
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
	duration, durErr := opus.PacketDuration(buf)
	if durErr != nil {
		duration = 20 * time.Millisecond //nolint:mnd // fallback to default Opus frame duration per RFC 6716 §2.1.4
	}
	p := opus.NewPacket(buf, (time.Duration(d.timestamp)*time.Second)/time.Duration(d.sdp.TimeScale), "", time.Now(),
		d.CodecParameters, duration)
	p.Slot = handle
	pkt = p
	return
}
