package aac

import (
	"bytes"
	"fmt"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec"
)

type CodecParameters struct {
	codec.BaseParameters
	ConfigBytes []byte
	Config      MPEG4AudioConfig
}

func NewCodecDataFromMPEG4AudioConfig(config MPEG4AudioConfig) (cod CodecParameters, err error) {
	b := new(bytes.Buffer)
	if err = WriteMPEG4AudioConfig(b, config); err != nil {
		return
	}

	return NewCodecDataFromMPEG4AudioConfigBytes(b.Bytes())
}

func NewCodecDataFromMPEG4AudioConfigBytes(config []byte) (cod CodecParameters, err error) {
	cod.ConfigBytes = config
	if cod.Config, err = ParseMPEG4AudioConfigBytes(config); err != nil {
		err = fmt.Errorf("aacparser: parse MPEG4AudioConfig failed(%w)", err)
		return
	}
	cod.CodecType = gomedia.AAC

	const bitsPerByte = 8
	cod.BRate = uint(cod.SampleRate()) * uint(cod.ChannelLayout().Count()) * uint(cod.SampleFormat().BytesPerSample()*bitsPerByte) //nolint:lll,gosec // integer overflow for bitrate is not possible

	return
}

func (cd CodecParameters) MPEG4AudioConfigBytes() []byte {
	return cd.ConfigBytes
}

func (cd CodecParameters) ChannelLayout() gomedia.ChannelLayout {
	return cd.Config.ChannelLayout
}

func (cd CodecParameters) SampleRate() uint64 {
	return uint64(cd.Config.SampleRate) //nolint:gosec // integer overflow for sample rate is not possible
}

func (cd CodecParameters) Channels() uint8 {
	return uint8(cd.Config.ChannelLayout.Count()) //nolint:gosec // integer overflow for channel is not possible
}

func (cd CodecParameters) SampleFormat() gomedia.SampleFormat {
	return gomedia.FLTP
}

func (cd CodecParameters) Tag() string {
	return fmt.Sprintf("mp4a.40.%d", cd.Config.ObjectType)
}
