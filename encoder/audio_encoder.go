package encoder

//go:generate mockgen -source=audio_encoder.go -destination=../mocks/mock_audio_encoder.go -package=mocks

import (
	"fmt"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/pcm"
	"github.com/ugparu/gomedia/utils/lifecycle"
	"github.com/ugparu/gomedia/utils/logger"
)

type InnerAudioEncoder interface {
	Init(params *pcm.CodecParameters) error
	Encode(pkt *pcm.Packet) ([]gomedia.AudioPacket, error)
	Close()
}

// maxEncoderTSDrift bounds how far the evenly-spaced output clock may drift from
// the source timeline before it is re-anchored: comfortably above normal encoder
// buffering latency, far below any stream gap or reconnect (which are seconds).
const maxEncoderTSDrift = time.Second

type audioEncoder struct {
	lifecycle.AsyncManager[*audioEncoder]
	InnerAudioEncoder
	newEncoderFn func() InnerAudioEncoder
	ts           time.Duration
	inpSamples   chan gomedia.AudioPacket
	outSamples   chan gomedia.Packet
	inpCodecPar  *pcm.CodecParameters
	codecPar     gomedia.AudioCodecParameters
}

func NewAudioEncoder(chanSize int, newEncoderFn func() InnerAudioEncoder) gomedia.AudioEncoder {
	e := &audioEncoder{
		InnerAudioEncoder: newEncoderFn(),
		newEncoderFn:      newEncoderFn,
		inpSamples:        make(chan gomedia.AudioPacket, chanSize),
		outSamples:        make(chan gomedia.Packet, chanSize),
	}
	e.AsyncManager = lifecycle.NewFailSafeAsyncManager(e, logger.Default)

	return e
}

func (e *audioEncoder) Encode() {
	// FailSafeAsyncManager.Start never returns an error.
	_ = e.Start(func(_ *audioEncoder) error { return nil })
}

func (e *audioEncoder) Step(doneCh <-chan struct{}) error {
	select {
	case <-doneCh:
		return &lifecycle.BreakError{}
	case aPkt := <-e.inpSamples:
		defer aPkt.Release()
		pkt, ok := aPkt.(*pcm.Packet)
		if !ok {
			return fmt.Errorf("invalid packet type: %T", pkt)
		}

		if pkt.CodecParameters() != e.inpCodecPar {
			e.inpCodecPar, _ = pkt.CodecParameters().(*pcm.CodecParameters)
			e.InnerAudioEncoder.Close()
			e.InnerAudioEncoder = e.newEncoderFn()
			if err := e.InnerAudioEncoder.Init(e.inpCodecPar); err != nil {
				return err
			}
		}

		packets, err := e.InnerAudioEncoder.Encode(pkt)
		if err != nil {
			return err
		}

		for _, pkt := range packets {
			// e.ts spaces AAC frames evenly because one input PCM packet does
			// not map 1:1 to AAC frames (the encoder buffers samples and may
			// emit zero, one, or several frames per input packet). But the
			// counter must still follow the source timeline: on a stream gap or
			// camera restart the input timestamp jumps forward (the reader
			// bridges the outage) while a plain frame counter keeps the old pace
			// and would trail video by the whole outage forever. Re-anchor to
			// the source whenever they diverge beyond buffering jitter — a gap
			// or backward reset — otherwise keep ticking by frame duration so a
			// multi-frame batch stays evenly spaced.
			if src := pkt.Timestamp(); src-e.ts > maxEncoderTSDrift || e.ts-src > maxEncoderTSDrift {
				e.ts = src
			}
			pkt.SetTimestamp(e.ts)
			e.ts += pkt.Duration()
			select {
			case e.outSamples <- pkt:
			case <-doneCh:
				return &lifecycle.BreakError{}
			}
		}
	}
	return nil
}

func (e *audioEncoder) Release() { //nolint:revive // required by lifecycle.AsyncInstance interface
	e.InnerAudioEncoder.Close()
	// Drain remaining input samples to prevent leaks.
	for {
		select {
		case pkt, ok := <-e.inpSamples:
			if !ok {
				goto drained
			}
			pkt.Release()
		default:
			close(e.inpSamples)
			goto drained
		}
	}
drained:
	close(e.outSamples)
}

func (e *audioEncoder) String() string {
	return "AUDIO_ENCODER"
}

func (e *audioEncoder) Packets() <-chan gomedia.Packet {
	return e.outSamples
}

func (e *audioEncoder) Samples() chan<- gomedia.AudioPacket {
	return e.inpSamples
}
