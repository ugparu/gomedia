package aac

//#cgo CFLAGS: -I/usr/include/fdk-aac
//#cgo LDFLAGS: -L/usr/include/fdk-aac -lfdk-aac -Wl,-rpath=/usr/include/fdk-aac
//#include "aac_decoder.h"
import "C"
import (
	"fmt"
	"unsafe"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/aac"
	"github.com/ugparu/gomedia/decoder"
)

const (
	aacDecNotEnoughBits = 0x1002
)

type aacDecoder struct {
	m C.aacdec_t
}

func NewAacDecoder() decoder.InnerAudioDecoder {
	return &aacDecoder{
		m: C.aacdec_t{
			dec:          nil,
			is_adts:      0,
			info:         nil,
			sample_bits:  0,
			filled_bytes: 0,
		},
	}
}

func (d *aacDecoder) Init(param gomedia.AudioCodecParameters) error {
	aacParam, ok := param.(*aac.CodecParameters)
	if !ok {
		return fmt.Errorf("expected *aac.CodecParameters, got %T", param)
	}

	asc := aacParam.ConfigBytes
	p := (*C.char)(unsafe.Pointer(&asc[0]))
	pSize := C.int(len(asc))

	r := C.aacdec_init_raw(&d.m, p, pSize) //nolint:gocritic // CGO function call

	if int(r) != 0 {
		return fmt.Errorf("init RAW decoder failed, code is %d", int(r))
	}
	return nil
}

func (d *aacDecoder) Decode(inData []byte) (outData []byte, err error) {
	p := (*C.char)(unsafe.Pointer(&inData[0]))
	pSize := C.int(len(inData))
	leftSize := C.int(0)

	r := C.aacdec_fill(&d.m, p, pSize, &leftSize) //nolint:gocritic // CGO function call

	if int(r) != 0 {
		return nil, fmt.Errorf("fill aac decoder failed, code is %d", int(r))
	}

	if int(leftSize) > 0 {
		return nil, fmt.Errorf("decoder left %v bytes", int(leftSize))
	}

	nbPcm := int(C.aacdec_pcm_size(&d.m)) //nolint:gocritic // CGO function call
	// Calculate a more appropriate buffer size based on typical AAC frame parameters if size is unknown
	if nbPcm == 0 {
		// Maximum AAC frame size (2048 samples) * max 8 channels * 4 bytes per sample (worst case)
		const (
			maxSamplesPerFrame = 2048
			maxChannels        = 8
			bytesPerSample     = 4
			maxPossibleSize    = maxSamplesPerFrame * maxChannels * bytesPerSample
		)
		// Start with a reasonable default that can handle most cases
		nbPcm = maxPossibleSize
	}
	pcmData := make([]byte, nbPcm)

	p = (*C.char)(unsafe.Pointer(&pcmData[0]))
	pSize = C.int(nbPcm)
	validSize := C.int(0)

	ret := C.aacdec_decode_frame(&d.m, p, pSize, &validSize) //nolint:gocritic // CGO function call

	if int(ret) == aacDecNotEnoughBits {
		return nil, nil
	}

	if int(r) != 0 {
		return nil, fmt.Errorf("decode aac frame failed, code is %d", int(r))
	}

	return pcmData[:validSize], nil
}

func (d *aacDecoder) Close() {
	C.aacdec_close(&d.m) //nolint:gocritic // CGO function call
}
