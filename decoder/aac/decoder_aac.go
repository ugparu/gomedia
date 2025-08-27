package aac

//#cgo CFLAGS: -I/usr/include/fdk-aac
//#cgo LDFLAGS: -L/usr/include/fdk-aac -lfdk-aac -Wl,-rpath=/usr/include/fdk-aac
//#include "aacdecoder_lib.h"
//#include <stdlib.h>
import "C"
import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/aac"
	"github.com/ugparu/gomedia/decoder"
)

const (
	aacSampleBits = 16 // For lib-fdkaac, always use 16bits sample
	bitsPerByte   = 8  // Number of bits in a byte

	// FDK-AAC error code ranges (from aacdecoder_lib.h)
	aacDecodeErrorStart = 0x4000
	aacDecodeErrorEnd   = 0x4FFF

	maxSamplesPerFrame = 2048
	maxChannels        = 8
	bytesPerSample     = 2
	maxPossibleSize    = maxSamplesPerFrame * maxChannels * bytesPerSample
)

// isOutputValid implements the IS_OUTPUT_VALID macro from FDK-AAC
// Output buffer is valid if err == AAC_DEC_OK or it's a decode error (concealed but valid)
func isOutputValid(err C.AAC_DECODER_ERROR) bool {
	return err == C.AAC_DEC_OK || (err >= aacDecodeErrorStart && err <= aacDecodeErrorEnd)
}

type aacDecoder struct {
	dec         C.HANDLE_AACDECODER
	isAdts      C.int
	info        *C.CStreamInfo
	sampleBits  C.int
	filledBytes C.UINT

	// Separate buffers for input and output to avoid conflicts
	inputBuffer  []byte
	outputBuffer []byte
}

func NewAacDecoder() decoder.InnerAudioDecoder {
	aacDec := &aacDecoder{
		dec:          nil,
		isAdts:       0,
		info:         nil,
		sampleBits:   aacSampleBits,
		filledBytes:  0,
		inputBuffer:  make([]byte, maxPossibleSize),
		outputBuffer: make([]byte, maxPossibleSize),
	}

	return aacDec
}

func (d *aacDecoder) Init(param gomedia.AudioCodecParameters) error {
	aacParam, ok := param.(*aac.CodecParameters)
	if !ok {
		return fmt.Errorf("expected *aac.CodecParameters, got %T", param)
	}

	asc := aacParam.ConfigBytes

	// Open the decoder
	d.dec = C.aacDecoder_Open(C.TT_MP4_RAW, 1)
	if d.dec == nil {
		return errors.New("failed to open AAC decoder")
	}

	var pinner runtime.Pinner
	defer pinner.Unpin()

	pinner.Pin(&asc[0])
	uasc := (*C.UCHAR)(unsafe.Pointer(&asc[0]))
	unbAsc := C.UINT(len(asc))
	err := C.aacDecoder_ConfigRaw(d.dec, &uasc, &unbAsc)
	if err != C.AAC_DEC_OK {
		return fmt.Errorf("init RAW decoder failed, code is %d", int(err))
	}

	// Try to get stream info early if possible after configuration
	d.info = C.aacDecoder_GetStreamInfo(d.dec)

	return nil
}

// ensureInputBufferSize safely reallocates input buffer if needed
func (d *aacDecoder) ensureInputBufferSize(requiredSize int) {
	if cap(d.inputBuffer) < requiredSize {
		d.inputBuffer = make([]byte, requiredSize)
	}
	d.inputBuffer = d.inputBuffer[:requiredSize]
}

// ensureOutputBufferSize safely reallocates output buffer if needed
func (d *aacDecoder) ensureOutputBufferSize(requiredSize int) {
	if cap(d.outputBuffer) < requiredSize {
		d.outputBuffer = make([]byte, requiredSize)
	}
	d.outputBuffer = d.outputBuffer[:requiredSize]
}

func (d *aacDecoder) Decode(inData []byte) (outData []byte, err error) {
	d.filledBytes += C.UINT(len(inData))

	// Ensure input buffer is large enough and copy input data
	d.ensureInputBufferSize(len(inData))
	copy(d.inputBuffer, inData)

	// Pin input buffer for C interop
	var inputPinner runtime.Pinner
	defer inputPinner.Unpin()
	inputPinner.Pin(&d.inputBuffer[0])
	cInPcmData := (*C.UCHAR)(unsafe.Pointer(&d.inputBuffer[0]))

	unbData := C.UINT(len(inData))
	unbLeft := unbData

	fillErr := C.aacDecoder_Fill(d.dec, &cInPcmData, &unbData, &unbLeft)
	if fillErr != C.AAC_DEC_OK {
		return nil, fmt.Errorf("fill aac decoder failed, code is %d", int(fillErr))
	}

	if int(unbLeft) > 0 {
		return nil, fmt.Errorf("decoder left %v bytes", int(unbLeft))
	}

	// Calculate PCM output buffer size
	var nbPcm int
	if d.info != nil {
		nbPcm = int(d.info.numChannels * d.info.frameSize * d.sampleBits / bitsPerByte)
	}

	// Calculate a more appropriate buffer size based on typical AAC frame parameters if size is unknown
	if nbPcm == 0 {
		// Maximum AAC frame size (2048 samples) * max 8 channels * 4 bytes per sample (worst case)
		// Start with a reasonable default that can handle most cases
		nbPcm = maxPossibleSize
	}

	// Ensure output buffer is large enough
	d.ensureOutputBufferSize(nbPcm)

	// Pin output buffer for C interop
	var outputPinner runtime.Pinner
	defer outputPinner.Unpin()
	outputPinner.Pin(&d.outputBuffer[0])
	cOutPcmData := (*C.INT_PCM)(unsafe.Pointer(&d.outputBuffer[0]))

	// Decode the frame using the separate output buffer
	unbPcm := C.INT(nbPcm)
	decodeErr := C.aacDecoder_DecodeFrame(d.dec, cOutPcmData, unbPcm, 0)

	if decodeErr == C.AAC_DEC_NOT_ENOUGH_BITS {
		return nil, nil
	}

	// Use FDK-AAC's output validation logic for proper error handling
	if !isOutputValid(decodeErr) {
		return nil, fmt.Errorf("decode produced invalid output, code is %d", int(decodeErr))
	}

	// Get stream info after decode (successful or with concealed output)
	if d.info == nil {
		d.info = C.aacDecoder_GetStreamInfo(d.dec)
	}

	// Calculate actual valid size
	var validSize int
	if d.info != nil {
		validSize = int(d.info.numChannels * d.info.frameSize * d.sampleBits / bitsPerByte)
	} else {
		validSize = nbPcm
	}

	resp := make([]byte, validSize)
	copy(resp, d.outputBuffer[:validSize])

	return resp, nil
}

// Flush remaining audio data from decoder internal buffers
func (d *aacDecoder) Flush() ([]byte, error) {
	if d.dec == nil {
		return nil, errors.New("decoder not initialized")
	}

	// Calculate PCM buffer size
	var nbPcm int
	if d.info != nil {
		nbPcm = int(d.info.numChannels * d.info.frameSize * d.sampleBits / bitsPerByte)
	} else {
		nbPcm = maxSamplesPerFrame * maxChannels * bytesPerSample
	}

	// Ensure output buffer is large enough for flush operation
	d.ensureOutputBufferSize(nbPcm)

	// Pin output buffer for C interop
	var outputPinner runtime.Pinner
	defer outputPinner.Unpin()
	outputPinner.Pin(&d.outputBuffer[0])
	cOutPcmData := (*C.INT_PCM)(unsafe.Pointer(&d.outputBuffer[0]))

	unbPcm := C.INT(nbPcm)

	// Decode with FLUSH flag to get remaining delayed audio
	decodeErr := C.aacDecoder_DecodeFrame(d.dec, cOutPcmData, unbPcm, C.AACDEC_FLUSH)

	if decodeErr == C.AAC_DEC_NOT_ENOUGH_BITS {
		return nil, nil
	}

	if !isOutputValid(decodeErr) {
		return nil, fmt.Errorf("flush produced invalid output, code is %d", int(decodeErr))
	}

	// Calculate actual valid size
	var validSize int
	if d.info != nil {
		validSize = int(d.info.numChannels * d.info.frameSize * d.sampleBits / bitsPerByte)
	} else {
		validSize = nbPcm
	}

	return d.outputBuffer[:validSize], nil
}

func (d *aacDecoder) Close() {
	if d.dec != nil {
		C.aacDecoder_Close(d.dec)
		d.dec = nil
	}
}
