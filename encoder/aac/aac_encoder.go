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
	pcmBuf        buffer.Buffer
	pcmLen        int
	aacBuf        buffer.Buffer
	param         *aac.CodecParameters
	ring          *buffer.GrowingRingAlloc
}

func NewAacEncoder() encoder.InnerAudioEncoder {
	return &aacEncoder{
		pcmBuf: buffer.Get(0),
		aacBuf: buffer.Get(0),
	}
}

// Init configures an fdk-aac encoder for AAC-LC at 32 kbps/channel. Input PCM
// must already match the codec's sample rate, channel count, and 16-bit
// interleaved layout — the encoder does not resample.
func (v *aacEncoder) Init(codecPar *pcm.CodecParameters) (err error) {
	v.channels = int(codecPar.Channels())

	//nolint:gocritic // CGO function call
	r := C.aacenc_init(
		&v.m,
		2,
		C.int(v.channels),
		C.int(codecPar.SampleRate()),
		C.int(32*v.channels*1000), //nolint:mnd // 32 kbps per channel
	)

	if int(r) != 0 {
		return fmt.Errorf("initialize encoder failed, code=%v", int(r))
	}

	const bytesPerInt16 = 2
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
	v.ring = buffer.NewGrowingRingAlloc(80 * 1024)

	return
}

// Encode buffers incoming S16 PCM until at least NbBytesPerFrame bytes are
// available, then emits one AAC packet per full frame. When the encoder returns
// an empty output it is drained via flush so the last partial frame is not lost.
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

		v.pcmLen -= v.frameSize
		copy(v.pcmBuf.Data(), v.pcmBuf.Data()[v.frameSize:v.frameSize+v.pcmLen])

		// Zero-byte output signals that fdkaac has a frame withheld; drain it.
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

func (v *aacEncoder) Channels() int {
	return v.channels
}

// FrameSize reports the encoder's native frame length in samples per channel
// (1024 for AAC-LC).
func (v *aacEncoder) FrameSize() int {
	return int(C.aacenc_frame_size(&v.m)) //nolint:gocritic // CGO function call
}

// NbBytesPerFrame is the exact PCM byte count Encode needs to produce one
// AAC output frame (2 bytes × channels × samples).
func (v *aacEncoder) NbBytesPerFrame() int {
	return 2 * v.channels * v.FrameSize()
}

func (v *aacEncoder) Close() {
	C.aacenc_close(&v.m) //nolint:gocritic // CGO function call
	v.ring = nil
}
