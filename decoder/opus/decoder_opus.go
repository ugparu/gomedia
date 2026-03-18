package opus

import (
	"unsafe"

	"github.com/hraban/opus"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/decoder"
	"github.com/ugparu/gomedia/utils/buffer"
)

type opusDecoder struct {
	*opus.Decoder
	codecPar gomedia.AudioCodecParameters
}

func NewOpusDecoder() decoder.InnerAudioDecoder {
	return &opusDecoder{
		Decoder:  nil,
		codecPar: nil,
	}
}

func (d *opusDecoder) Init(params gomedia.AudioCodecParameters) (err error) {
	// Sample rates in audio are typically well below MaxInt32, so this is a safe conversion
	// We add the nolint directive because this is audio code where these values are known to be reasonable
	//nolint:gosec // Audio sample rates are typically well below MaxInt32
	d.Decoder, err = opus.NewDecoder(int(params.SampleRate()), int(params.Channels()))
	d.codecPar = params
	return
}

func (d *opusDecoder) Decode(inData []byte, ring *buffer.GrowingRingAlloc) (outData []byte, slot *buffer.SlotHandle, err error) {
	const (
		bufSize        = 8096
		bytesPerSample = 2 // Size of int16 in bytes
	)
	channelCount := int(d.codecPar.Channels())

	if ring != nil {
		ringBytes, h := ring.Alloc(bufSize * bytesPerSample)
		if ringBytes != nil {
			buf16 := unsafe.Slice((*int16)(unsafe.Pointer(&ringBytes[0])), bufSize)
			var n int
			if n, err = d.Decoder.Decode(inData, buf16); err != nil {
				h.Release()
				return nil, nil, err
			}
			return ringBytes[:n*bytesPerSample*channelCount], h, nil
		}
		// ring full — fall through to heap
	}

	buf16 := make([]int16, bufSize)
	var n int
	if n, err = d.Decoder.Decode(inData, buf16); err != nil {
		return nil, nil, err
	}
	buf16 = buf16[:n]
	outData = unsafe.Slice(
		(*byte)(unsafe.Pointer(&buf16[0])),
		n*bytesPerSample*channelCount,
	)
	return outData, nil, nil
}

func (d *opusDecoder) Close() {
}
