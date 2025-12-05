//nolint:mnd // This file contains audio-specific magic numbers
package opus

import (
	"time"
	"unsafe"

	"github.com/hraban/opus"
	"github.com/ugparu/gomedia"
	goopus "github.com/ugparu/gomedia/codec/opus"
	"github.com/ugparu/gomedia/codec/pcm"
	"github.com/ugparu/gomedia/encoder"
	"github.com/ugparu/gomedia/utils"
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
}

func NewOpusEncoder() encoder.InnerAudioEncoder {
	return new(opusEncoder)
}

func (e *opusEncoder) Init(params *pcm.CodecParameters) error {
	var err error
	e.Encoder, err = opus.NewEncoder(opusSampleRate, int(params.Channels()), opus.AppVoIP)
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

	// Safe type conversion for duration calculation
	safeRate := uint32(sampleRate)
	e.frameDuration = time.Duration(40*opusSampleRate/1000) * time.Second / time.Duration(safeRate)

	channels := int(params.Channels())
	// Using a safer type to convert to int
	e.r, err = utils.NewPcmS16leResampler(channels, int(safeRate), opusSampleRate)
	if err != nil {
		return err
	}

	cl := gomedia.ChMono
	if channels == 2 {
		cl = gomedia.ChStereo
	}

	e.codecPar = goopus.NewCodecParameters(params.StreamIndex(), cl, opusSampleRate)

	return err
}

func (e *opusEncoder) Encode(pkt *pcm.Packet) (resp []gomedia.AudioPacket, err error) {
	pkt.View(func(data []byte) {
		buf16 := unsafe.Slice((*int16)(unsafe.Pointer(&data[0])), len(data)/2)
		e.buf = append(e.buf, buf16...)
	})

	for {
		if len(e.buf) < e.frameSize {
			return resp, nil
		}
		pcm := e.buf[:e.frameSize]
		e.buf = e.buf[e.frameSize:]

		const bufSize = 1000
		outData := make([]byte, bufSize)

		var n int
		if n, err = e.Encoder.Encode(pcm, outData); err != nil {
			return nil, err
		}

		resp = append(resp, goopus.NewPacket(outData[:n], 0, pkt.URL(), pkt.StartTime(), e.codecPar, e.frameDuration))
	}
}

func (e *opusEncoder) Close() {
}
