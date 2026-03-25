//nolint:mnd // This file contains audio-specific magic numbers
package opus

import (
	"fmt"
	"time"
	"unsafe"

	"github.com/hraban/opus"
	"github.com/ugparu/gomedia"
	goopus "github.com/ugparu/gomedia/codec/opus"
	"github.com/ugparu/gomedia/codec/pcm"
	"github.com/ugparu/gomedia/encoder"
	"github.com/ugparu/gomedia/utils"
	"github.com/ugparu/gomedia/utils/buffer"
	"github.com/winlinvip/go-aresample/aresample"
)

const opusSampleRate = 48000

type opusEncoder struct {
	*opus.Encoder
	frameSize     int
	frameDuration time.Duration
	buf           []int16
	r             aresample.ResampleSampleRate
	codecPar      *goopus.CodecParameters
	ring          *buffer.GrowingRingAlloc
}

func NewOpusEncoder() encoder.InnerAudioEncoder {
	return new(opusEncoder)
}

func (e *opusEncoder) Init(params *pcm.CodecParameters) error {
	var err error
	e.Encoder, err = opus.NewEncoder(opusSampleRate, int(params.Channels()), opus.Application(opus.AppAudio))
	if err != nil {
		return err
	}

	// Convert sample rate to int32 to prevent overflow
	sampleRate := params.SampleRate()
	const maxInt32 = 1<<31 - 1 // maximum value for int32
	if sampleRate > uint64(maxInt32) {
		sampleRate = uint64(maxInt32)
	}

	e.frameSize = int(params.Channels()) * 40 * opusSampleRate / 1000

	safeRate := uint32(sampleRate)
	// Frame duration is always relative to 48kHz (the Opus internal rate)
	e.frameDuration = time.Duration(40*opusSampleRate/1000) * time.Second / time.Duration(opusSampleRate)

	channels := int(params.Channels())
	// Using a safer type to convert to int
	e.r, err = utils.NewPcmS16leResampler(channels, int(safeRate), opusSampleRate)
	if err != nil {
		return err
	}

	var cl gomedia.ChannelLayout
	switch channels {
	case 1:
		cl = gomedia.ChMono
	case 2:
		cl = gomedia.ChStereo
	default:
		return fmt.Errorf("opus encoder: unsupported channel count %d (only mono and stereo are supported)", channels)
	}

	e.codecPar = goopus.NewCodecParameters(params.StreamIndex(), cl, opusSampleRate)
	e.ring = buffer.NewGrowingRingAlloc(64 * 1024)

	return err
}

func (e *opusEncoder) Encode(pkt *pcm.Packet) (resp []gomedia.AudioPacket, err error) {
	if len(pkt.Data()) < 2 {
		return nil, nil
	}

	// Resample input PCM to 48kHz before encoding
	resampled, err := e.r.Resample(pkt.Data())
	if err != nil {
		return nil, err
	}

	if len(resampled) < 2 {
		return nil, nil
	}

	buf16 := unsafe.Slice((*int16)(unsafe.Pointer(&resampled[0])), len(resampled)/2)
	e.buf = append(e.buf, buf16...)

	consumed := 0
	for len(e.buf)-consumed >= e.frameSize {
		pcm := e.buf[consumed : consumed+e.frameSize]
		consumed += e.frameSize

		const bufSize = 1000
		var outData []byte
		var handle *buffer.SlotHandle
		if e.ring != nil {
			outData, handle = e.ring.Alloc(bufSize)
		}
		if outData == nil {
			outData = make([]byte, bufSize)
		}

		var n int
		if n, err = e.Encoder.Encode(pcm, outData); err != nil {
			handle.Release()
			return nil, err
		}

		p := goopus.NewPacket(outData[:n], 0, pkt.SourceID(), pkt.StartTime(), e.codecPar, e.frameDuration)
		p.Slot = handle
		resp = append(resp, p)
	}

	// Compact: copy remaining samples to the front to prevent unbounded backing-array growth
	remaining := len(e.buf) - consumed
	copy(e.buf, e.buf[consumed:])
	e.buf = e.buf[:remaining]

	return resp, nil
}

func (e *opusEncoder) Close() {
	e.buf = nil
	e.ring.Close()
	e.ring = nil
	e.r = nil
	e.Encoder = nil
}
