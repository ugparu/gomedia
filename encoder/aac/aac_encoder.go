package aac

// #cgo CFLAGS: -I/usr/include/fdk-aac
//#cgo LDFLAGS: -L/usr/include/fdk-aac -lfdk-aac -Wl,-rpath=/usr/include/fdk-aac
//#include "aac_encoder.h"
import "C"
import (
	"fmt"
	"time"
	"unsafe"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/aac"
	"github.com/ugparu/gomedia/codec/pcm"
	"github.com/ugparu/gomedia/encoder"
)

type aacEncoder struct {
	m             C.aacenc_t
	channels      int
	frameSize     int
	frameDuration time.Duration
	buf           []uint8
	param         *aac.CodecParameters
}

func NewAacEncoder() encoder.InnerAudioEncoder {
	return new(aacEncoder)
}

// Initialize the encoder in LC profile.
// @remark the encoder use sampleRate and channels, user should resample the PCM to fit it,
//
//	that is, the channels and sampleRate of PCM should always equals to encoder's.
//
// @remark for the fdkaac always use 16bits sample, so the bits of pcm always 16,
//
//	which must be: [SHORT PCM] [SHORT PCM] ... ...
func (v *aacEncoder) Init(codecPar *pcm.CodecParameters) (err error) {
	v.channels = int(codecPar.Channels())

	// Initialize AAC encoder with specific parameters
	//nolint:gocritic // CGO function call
	r := C.aacenc_init(
		&v.m,
		2,
		C.int(v.channels),
		C.int(codecPar.SampleRate()),
		C.int(32*v.channels*1000), //nolint:mnd // bitrate calculation (32kbps per channel in Hz)
	)

	if int(r) != 0 {
		return fmt.Errorf("initialize encoder failed, code=%v", int(r))
	}

	// Size of int16 in bytes is 2
	const bytesPerInt16 = 2 // size of int16 in bytes
	v.frameSize = bytesPerInt16 * v.channels * v.FrameSize()
	v.frameDuration = time.Duration(v.FrameSize()) * time.Second / time.Duration(codecPar.SampleRate())

	sampleRate := codecPar.SampleRate()
	const maxInt32 = 1<<31 - 1 // maximum value for int32
	if sampleRate > uint64(maxInt32) {
		sampleRate = uint64(maxInt32)
	}

	cl := gomedia.ChMono
	if codecPar.Channels() == 2 { //nolint:mnd // 2 represents stereo audio
		cl = gomedia.ChStereo
	}

	aacPar, err := aac.NewCodecDataFromMPEG4AudioConfig(aac.MPEG4AudioConfig{
		SampleRate:      int(uint32(sampleRate)),
		ChannelLayout:   cl,
		ObjectType:      2, //nolint:mnd // AAC LC audio object type
		SampleRateIndex: 0,
		ChannelConfig:   0,
	})
	aacPar.SetStreamIndex(codecPar.StreamIndex())
	if err != nil {
		return err
	}
	v.param = &aacPar

	return
}

// Encode the pcm to aac, pcm must contains bytes for one aac frame,
//
//	that is the bytes must be NbBytesPerFrame().
//
// @remark fdkaac always use 16bits pcm, so the bits of pcm always 16.
// @remark user should resample the pcm to fit the encoder, so the channels of pcm equals to encoder's.
// @remark user should resample the pcm to fit the encoder, so the sampleRate of pcm equals to encoder's.
// @return when aac is nil, encoded completed(the Flush() return nil also),
//
//	because we will flush the encoder automatically to got the last frames.
func (v *aacEncoder) Encode(pkt *pcm.Packet) (resp []gomedia.AudioPacket, err error) {
	pcm := pkt.Data()
	v.buf = append(v.buf, pcm...)

	for len(v.buf) >= v.frameSize {
		pcm = v.buf[:v.frameSize]
		v.buf = v.buf[v.frameSize:]

		// The maximum packet size is 8KB aka 768 bytes per channel.
		nbAac := int(C.aacenc_max_output_buffer_size(&v.m)) //nolint:gocritic // CGO function call
		aacBuf := make([]byte, nbAac)

		pAac := (*C.char)(unsafe.Pointer(&aacBuf[0]))
		pAacSize := C.int(nbAac)

		pPcm := (*C.char)(unsafe.Pointer(&pcm[0]))
		pPcmSize := C.int(len(pcm))
		pNbSamples := C.int(v.FrameSize())

		r := C.aacenc_encode(&v.m, pPcm, pPcmSize, pNbSamples, pAac, &pAacSize) //nolint:gocritic // CGO function call
		if int(r) != 0 {
			return nil, fmt.Errorf("Encode failed, code=%v", int(r))
		}

		valid := int(pAacSize)

		// when got nil packet, flush encoder.
		if valid == 0 {
			aacBuf, err = v.Flush()
			if err == nil {
				resp = append(resp, aac.NewPacket(aacBuf, 0, pkt.URL(), pkt.StartTime(), v.param, v.frameDuration))
			}
			break
		}

		resp = append(resp, aac.NewPacket(aacBuf[0:valid], pkt.Timestamp(), pkt.URL(), pkt.StartTime(), v.param, v.frameDuration))
	}
	return
}

// Flush the encoder to get the cached aac frames.
// @return when aac is nil, flush ok, should never flush anymore.
func (v *aacEncoder) Flush() (aac []byte, err error) {
	// The maximum packet size is 8KB aka 768 bytes per channel.
	nbAac := int(C.aacenc_max_output_buffer_size(&v.m)) //nolint:gocritic // CGO function call
	aac = make([]byte, nbAac)

	pAac := (*C.char)(unsafe.Pointer(&aac[0]))
	pAacSize := C.int(nbAac)

	r := C.aacenc_encode(&v.m, nil, 0, 0, pAac, &pAacSize) //nolint:gocritic // CGO function call
	if int(r) != 0 {
		return nil, fmt.Errorf("Flush failed, code=%v", int(r))
	}

	valid := int(pAacSize)
	if valid == 0 {
		return nil, nil
	}

	return aac[0:valid], nil
}

// Get the channels of encoder.
func (v *aacEncoder) Channels() int {
	return v.channels
}

// Get the frame size of encoder.
func (v *aacEncoder) FrameSize() int {
	return int(C.aacenc_frame_size(&v.m)) //nolint:gocritic // CGO function call
}

// Get the number of bytes per a aac frame.
func (v *aacEncoder) NbBytesPerFrame() int {
	return 2 * v.channels * v.FrameSize()
}

func (v *aacEncoder) Close() {
	C.aacenc_close(&v.m) //nolint:gocritic // CGO function call
}
