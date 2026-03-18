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
	"github.com/ugparu/gomedia/utils/buffer"
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

	// Close existing decoder on re-initialization to prevent handle leak.
	C.aacdec_close(&d.m) //nolint:gocritic

	asc := aacParam.ConfigBytes
	if len(asc) == 0 {
		return fmt.Errorf("aac init: empty AudioSpecificConfig")
	}
	p := (*C.char)(unsafe.Pointer(&asc[0]))
	pSize := C.int(len(asc))

	r := C.aacdec_init_raw(&d.m, p, pSize) //nolint:gocritic // CGO function call

	if int(r) != 0 {
		return fmt.Errorf("init RAW decoder failed, code is %d", int(r))
	}
	return nil
}

func (d *aacDecoder) Decode(inData []byte, ring *buffer.GrowingRingAlloc) (outData []byte, slot *buffer.SlotHandle, err error) {
	if len(inData) == 0 {
		return nil, nil, nil
	}
	p := (*C.char)(unsafe.Pointer(&inData[0]))
	pSize := C.int(len(inData))
	leftSize := C.int(0)

	r := C.aacdec_fill(&d.m, p, pSize, &leftSize) //nolint:gocritic // CGO function call

	if int(r) != 0 {
		return nil, nil, fmt.Errorf("fill aac decoder failed, code is %d", int(r))
	}

	// leftSize > 0 means FDK-AAC's internal ring buffer was full and could not
	// accept all input bytes (e.g. multiple AUs per RFC 3640 payload). Proceed
	// with decoding the buffered data; losing the leftover bytes for this call
	// is preferable to stopping the pipeline with an error.

	nbPcm := int(C.aacdec_pcm_size(&d.m)) //nolint:gocritic // CGO function call
	if nbPcm == 0 {
		// Maximum HE-AAC frame size (2048 samples) * max 8 channels * 2 bytes per
		// sample (FDK-AAC always outputs 16-bit PCM).
		const (
			maxSamplesPerFrame = 2048
			maxChannels        = 8
			bytesPerSample     = 2 // FDK-AAC always outputs 16-bit samples
			maxPossibleSize    = maxSamplesPerFrame * maxChannels * bytesPerSample
		)
		nbPcm = maxPossibleSize
	}

	var pcmData []byte
	if ring != nil {
		if pcmData, slot = ring.Alloc(nbPcm); pcmData == nil {
			// ring full — fall back to heap
			pcmData = make([]byte, nbPcm)
		}
	} else {
		pcmData = make([]byte, nbPcm)
	}

	p = (*C.char)(unsafe.Pointer(&pcmData[0]))
	pSize = C.int(nbPcm)
	validSize := C.int(0)

	ret := C.aacdec_decode_frame(&d.m, p, pSize, &validSize) //nolint:gocritic // CGO function call

	if int(ret) == aacDecNotEnoughBits {
		slot.Release()
		return nil, nil, nil
	}

	if int(ret) != 0 {
		slot.Release()
		return nil, nil, fmt.Errorf("decode aac frame failed, code is %d", int(ret))
	}

	return pcmData[:validSize], slot, nil
}

func (d *aacDecoder) Close() {
	C.aacdec_close(&d.m) //nolint:gocritic // CGO function call
}
