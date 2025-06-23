package pcm

import (
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/decoder"
	"github.com/zaf/g711"
)

type lawDecoder struct {
	decodeF func([]byte) []byte
}

func NewALAWDecoder() decoder.InnerAudioDecoder {
	return &lawDecoder{
		decodeF: g711.DecodeAlaw,
	}
}
func NewULAWDecoder() decoder.InnerAudioDecoder {
	return &lawDecoder{
		decodeF: g711.DecodeUlaw,
	}
}

func (d *lawDecoder) Init(_ gomedia.AudioCodecParameters) error {
	return nil
}
func (d *lawDecoder) Decode(inData []byte) (outData []byte, err error) {
	return d.decodeF(inData), nil
}

func (d *lawDecoder) Close() {
}
