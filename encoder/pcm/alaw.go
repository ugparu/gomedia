package pcm

import (
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/pcm"
	"github.com/ugparu/gomedia/encoder"
	"github.com/ugparu/gomedia/utils"
	"github.com/winlinvip/go-aresample/aresample"
	"github.com/zaf/g711"
)

const ALAWSampleRate = 8000
const AlawChannels = 1
const AlawFrameSize = ALAWSampleRate * AlawChannels * 2 / 10

type alawEncoder struct {
	r             aresample.ResampleSampleRate
	inpChannels   uint8
	frameDuration time.Duration
	buf           []uint8
	codecPar      *pcm.CodecParameters
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

	// Use safe conversion function
	e.r, err = utils.NewPcmS16leResampler(AlawChannels, int(uint32(sampleRate)), ALAWSampleRate)

	// Division by 10 is used to create 100ms frame duration
	const frameDurationDivisor = 10 // creates 100ms frame duration
	e.frameDuration = time.Duration(ALAWSampleRate/frameDurationDivisor) * time.Second / time.Duration(ALAWSampleRate)

	e.codecPar = pcm.NewCodecParameters(params.StreamIndex(), gomedia.PCMAlaw, 1, ALAWSampleRate)

	return err
}

func (e *alawEncoder) Encode(pkt *pcm.Packet) (resp []gomedia.AudioPacket, err error) {
	e.buf = append(e.buf, pkt.Data()...)

	for len(e.buf) >= AlawFrameSize {
		inData := e.buf[:AlawFrameSize]
		e.buf = e.buf[AlawFrameSize:]

		var inBuf []byte
		if e.inpChannels == 1 {
			inBuf = inData
		} else {
			// For multi-channel 16-bit PCM, properly extract the first channel
			// Each sample is 2 bytes, and samples are interleaved by channel
			bytesPerSample := 2
			totalChannels := int(e.inpChannels)
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
		resp = append(resp, pcm.NewPacket(g711.EncodeAlaw(inBuf), 0, pkt.URL(), pkt.StartTime(), e.codecPar, e.frameDuration))
	}
	return
}

func (e *alawEncoder) Close() {
}
