package pcm

import (
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/pcm"
	"github.com/ugparu/gomedia/encoder"
	"github.com/ugparu/gomedia/utils"
	"github.com/ugparu/gomedia/utils/buffer"
	"github.com/winlinvip/go-aresample/aresample"
	"github.com/zaf/g711"
)

const ALAWSampleRate = 8000
const AlawChannels = 1
const AlawFrameSize = ALAWSampleRate * AlawChannels * 2 / 10

type alawEncoder struct {
	r             aresample.ResampleSampleRate
	inpChannels   uint8
	inpFrameSize  int
	frameDuration time.Duration
	buf           []uint8
	codecPar      *pcm.CodecParameters
	ring          *buffer.GrowingRingAlloc
}

func NewAlawEncoder() encoder.InnerAudioEncoder {
	return new(alawEncoder)
}

func (e *alawEncoder) Init(params *pcm.CodecParameters) error {
	var err error
	e.inpChannels = params.Channels()

	// Convert sample rate to int32 to prevent overflow
	sampleRate := params.SampleRate()
	// Use a safer max value comparison
	const maxInt32 = 1<<31 - 1 // maximum value for int32
	if sampleRate > uint64(maxInt32) {
		sampleRate = uint64(maxInt32)
	}

	// Input frame size scales with channel count so we consume a full duration of multi-channel audio
	e.inpFrameSize = AlawFrameSize * int(e.inpChannels)

	// Resampler operates on mono data — multi-channel input is downmixed before resampling
	e.r, err = utils.NewPcmS16leResampler(AlawChannels, int(uint32(sampleRate)), ALAWSampleRate)

	// Division by 10 is used to create 100ms frame duration
	const frameDurationDivisor = 10 // creates 100ms frame duration
	e.frameDuration = time.Duration(ALAWSampleRate/frameDurationDivisor) * time.Second / time.Duration(ALAWSampleRate)

	e.codecPar = pcm.NewCodecParameters(params.StreamIndex(), gomedia.PCMAlaw, 1, ALAWSampleRate)
	e.ring = buffer.NewGrowingRingAlloc(16 * 1024)

	return err
}

func (e *alawEncoder) Encode(pkt *pcm.Packet) (resp []gomedia.AudioPacket, err error) {
	if len(pkt.Data()) < 2 {
		return nil, nil
	}
	e.buf = append(e.buf, pkt.Data()...)

	consumed := 0
	for len(e.buf)-consumed >= e.inpFrameSize {
		inData := e.buf[consumed : consumed+e.inpFrameSize]
		consumed += e.inpFrameSize

		var inBuf []byte
		var poolBuf buffer.Buffer
		if e.inpChannels == 1 {
			inBuf = inData
		} else {
			// For multi-channel 16-bit PCM, properly extract the first channel
			// Each sample is 2 bytes, and samples are interleaved by channel
			bytesPerSample := 2
			totalChannels := int(e.inpChannels)
			monoSize := len(inData) / totalChannels
			poolBuf = buffer.Get(monoSize)
			inBuf = poolBuf.Data()[:0]
			for i := 0; i < len(inData); i += bytesPerSample * totalChannels {
				// Take the 2 bytes of the first channel's sample
				if i+1 < len(inData) {
					inBuf = append(inBuf, inData[i], inData[i+1])
				}
			}
		}

		if inBuf, err = e.r.Resample(inBuf); err != nil {
			return
		}
		encoded := g711.EncodeAlaw(inBuf)
		var outData []byte
		var handle *buffer.SlotHandle
		if e.ring != nil {
			outData, handle = e.ring.Alloc(len(encoded))
		}
		if outData == nil {
			outData = encoded
		} else {
			copy(outData, encoded)
		}
		p := pcm.NewPacket(outData, 0, pkt.SourceID(), pkt.StartTime(), e.codecPar, e.frameDuration)
		p.Slot = handle
		resp = append(resp, p)
	}

	// Compact: copy remaining samples to the front to prevent unbounded backing-array growth
	remaining := len(e.buf) - consumed
	copy(e.buf, e.buf[consumed:])
	e.buf = e.buf[:remaining]

	return
}

func (e *alawEncoder) Close() {
	e.buf = nil
	e.ring = nil
	e.r = nil
}
