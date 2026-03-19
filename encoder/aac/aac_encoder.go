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
	"github.com/ugparu/gomedia/utils/buffer"
)

type aacEncoder struct {
	m             C.aacenc_t
	channels      int
	frameSize     int
	frameDuration time.Duration
	pcmBuf        buffer.PooledBuffer
	pcmLen        int
	aacBuf        buffer.PooledBuffer
	param         *aac.CodecParameters
	ring          *buffer.GrowingRingAlloc
}

func NewAacEncoder() encoder.InnerAudioEncoder {
	return &aacEncoder{
		pcmBuf: buffer.Get(0),
		aacBuf: buffer.Get(0),
	}
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

	channelConfig := uint(v.channels) //nolint:mnd // AAC channel config matches channel count for 1-6
	cl := aac.ChannelLayoutForConfig(channelConfig)

	sampleRateIndex := aac.SampleRateIndex(int(uint32(sampleRate)))

	aacPar, err := aac.NewCodecDataFromMPEG4AudioConfig(aac.MPEG4AudioConfig{
		SampleRate:      int(uint32(sampleRate)),
		ChannelLayout:   cl,
		ObjectType:      2, //nolint:mnd // AAC LC audio object type
		SampleRateIndex: sampleRateIndex,
		ChannelConfig:   channelConfig,
	})
	if err != nil {
		return err
	}
	aacPar.SetStreamIndex(codecPar.StreamIndex())
	v.param = &aacPar
	v.ring = buffer.NewGrowingRingAlloc(256 * 1024)

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
	data := pkt.Data()
	needed := v.pcmLen + len(data)
	if needed > v.pcmBuf.Len() {
		v.pcmBuf.Resize(needed)
	}
	copy(v.pcmBuf.Data()[v.pcmLen:needed], data)
	v.pcmLen = needed

	for v.pcmLen >= v.frameSize {
		pcm := v.pcmBuf.Data()[:v.frameSize]

		// The maximum packet size is 8KB aka 768 bytes per channel.
		nbAac := int(C.aacenc_max_output_buffer_size(&v.m)) //nolint:gocritic // CGO function call
		v.aacBuf.Resize(nbAac)

		pAac := (*C.char)(unsafe.Pointer(&v.aacBuf.Data()[0]))
		pAacSize := C.int(nbAac)

		pPcm := (*C.char)(unsafe.Pointer(&pcm[0]))
		pPcmSize := C.int(len(pcm))
		pNbSamples := C.int(v.FrameSize())

		r := C.aacenc_encode(&v.m, pPcm, pPcmSize, pNbSamples, pAac, &pAacSize) //nolint:gocritic // CGO function call
		if int(r) != 0 {
			return nil, fmt.Errorf("Encode failed, code=%v", int(r))
		}

		valid := int(pAacSize)

		// Shift consumed frame out of the PCM buffer.
		v.pcmLen -= v.frameSize
		copy(v.pcmBuf.Data(), v.pcmBuf.Data()[v.frameSize:v.frameSize+v.pcmLen])

		// when got nil packet, flush encoder.
		if valid == 0 {
			flushed, flushErr := v.flush(pkt.Timestamp(), pkt.SourceID(), pkt.StartTime())
			if flushErr != nil {
				err = flushErr
			} else {
				resp = append(resp, flushed...)
			}
			break
		}

		var outData []byte
		var handle *buffer.SlotHandle
		if v.ring != nil {
			outData, handle = v.ring.Alloc(valid)
		}
		if outData == nil {
			outData = make([]byte, valid)
		}
		copy(outData, v.aacBuf.Data()[:valid])
		p := aac.NewPacket(outData, pkt.Timestamp(), pkt.SourceID(), pkt.StartTime(), v.param, v.frameDuration)
		p.Slot = handle
		resp = append(resp, p)
	}
	return
}

// flush drains any remaining frames from the encoder and returns them as packets.
func (v *aacEncoder) flush(ts time.Duration, sourceID string, startTime time.Time) (resp []gomedia.AudioPacket, err error) {
	for {
		nbAac := int(C.aacenc_max_output_buffer_size(&v.m)) //nolint:gocritic // CGO function call
		v.aacBuf.Resize(nbAac)

		pAac := (*C.char)(unsafe.Pointer(&v.aacBuf.Data()[0]))
		pAacSize := C.int(nbAac)

		r := C.aacenc_encode(&v.m, nil, 0, 0, pAac, &pAacSize) //nolint:gocritic // CGO function call
		if int(r) != 0 {
			return nil, fmt.Errorf("Flush failed, code=%v", int(r))
		}

		valid := int(pAacSize)
		if valid == 0 {
			return
		}

		var outData []byte
		var handle *buffer.SlotHandle
		if v.ring != nil {
			outData, handle = v.ring.Alloc(valid)
		}
		if outData == nil {
			outData = make([]byte, valid)
		}
		copy(outData, v.aacBuf.Data()[:valid])
		p := aac.NewPacket(outData, ts, sourceID, startTime, v.param, v.frameDuration)
		p.Slot = handle
		resp = append(resp, p)
	}
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
	v.pcmBuf.Release()
}
