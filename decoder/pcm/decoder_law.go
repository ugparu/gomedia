package pcm

import (
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/decoder"
	"github.com/ugparu/gomedia/utils/buffer"
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
func (d *lawDecoder) Decode(inData []byte, ring *buffer.GrowingRingAlloc) (outData []byte, slot *buffer.SlotHandle, err error) {
	decoded := d.decodeF(inData)
	if ring != nil {
		if ringBytes, h := ring.Alloc(len(decoded)); ringBytes != nil {
			copy(ringBytes, decoded)
			return ringBytes, h, nil
		}
		// ring full — return g711 output as-is
	}
	return decoded, nil, nil
}

func (d *lawDecoder) Close() {
}
