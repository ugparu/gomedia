package opus

import (
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec"
)

const (
	bitPerSample  = 16
	maxUint8Value = 255
	bitsInByte    = 8
)

type CodecParameters struct {
	codec.BaseParameters
	gomedia.ChannelLayout
	sampleRate uint64
}

func NewCodecParameters(index uint8, cl gomedia.ChannelLayout, sr uint64) *CodecParameters {
	// uint64 intermediate keeps bitPerSample × channels × sampleRate from overflowing uint.
	bitrate := uint(uint64(bitPerSample) * uint64(cl.Count()) * sr / bitsInByte) //nolint:gosec // computed in uint64

	return &CodecParameters{
		ChannelLayout: cl,
		BaseParameters: codec.BaseParameters{
			Index:     index,
			BRate:     bitrate,
			CodecType: gomedia.OPUS,
		},
		sampleRate: sr,
	}
}

func (p *CodecParameters) SampleFormat() gomedia.SampleFormat {
	return gomedia.S16
}

func (p *CodecParameters) SampleRate() uint64 {
	return p.sampleRate
}

func (p *CodecParameters) Channels() uint8 {
	count := p.ChannelLayout.Count()
	if count > maxUint8Value {
		return maxUint8Value
	}
	return uint8(count) //nolint:gosec // clamped to maxUint8Value above
}

func (p *CodecParameters) Tag() string {
	return "opus"
}
