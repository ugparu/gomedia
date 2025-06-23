package opus

import (
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec"
)

const (
	bitPerSample  = 16
	maxUint8Value = 255 // Maximum value for uint8
	bitsInByte    = 8   // Number of bits in a byte, used for bitrate calculation
)

type CodecParameters struct {
	codec.BaseParameters
	gomedia.ChannelLayout
	sampleRate uint64
}

func NewCodecParameters(index uint8, cl gomedia.ChannelLayout, sr uint64) *CodecParameters {
	// Calculate bitrate, using intermediate uint64 to avoid integer overflow
	// bits per sample * channels * sample rate / 8 = bytes per second
	// Using uint64 for intermediate calculation to prevent overflow
	// gosec warns about potential overflow, but we've mitigated it
	bitrate := uint(uint64(bitPerSample) * uint64(cl.Count()) * sr / bitsInByte) //nolint:gosec // Prevents overflow

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
	return uint8(count) //nolint:gosec // Safe conversion as we check for overflow above
}

func (p *CodecParameters) Tag() string {
	return "opus"
}
