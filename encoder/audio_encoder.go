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
