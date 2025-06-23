package pcm

import (
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec"
)

const bitPerSample = 16

type CodecParameters struct {
	codec.BaseParameters
	chCount    uint8
	sampleRate uint64
}

func NewCodecParameters(index uint8, ct gomedia.CodecType, channelCount uint8, sr uint64) *CodecParameters {
	return &CodecParameters{
		BaseParameters: codec.BaseParameters{
			Index:     index,
			BRate:     uint(sr) * bitPerSample * uint(channelCount),
			CodecType: ct,
		},
		sampleRate: sr,
		chCount:    channelCount,
	}
}

func (p *CodecParameters) SampleFormat() gomedia.SampleFormat {
	return gomedia.S16
}

func (p *CodecParameters) SampleRate() uint64 {
	return p.sampleRate
}

func (p *CodecParameters) Channels() uint8 {
	return p.chCount
}

func (p *CodecParameters) Tag() string {
	return "pcm"
}
