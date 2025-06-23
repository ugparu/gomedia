package opus

import (
	"unsafe"

	"github.com/hraban/opus"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/decoder"
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

func (d *opusDecoder) Decode(inData []byte) (outData []byte, err error) {
	const bufSize = 8096
	buf16 := make([]int16, bufSize)

	var n int
	if n, err = d.Decoder.Decode(inData, buf16); err != nil {
		return nil, err
	}

	buf16 = buf16[:n]
	// 2 is the size of int16 in bytes
	const bytesPerSample = 2 // Size of int16 in bytes
	channelCount := int(d.codecPar.Channels())
	outData = unsafe.Slice(
		(*byte)(unsafe.Pointer(&buf16[0])),
		n*bytesPerSample*channelCount,
	)

	return outData, nil
}

func (d *opusDecoder) Close() {
}
