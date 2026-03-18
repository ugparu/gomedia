package decoder

import (
	"errors"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/pcm"
	"github.com/ugparu/gomedia/utils/buffer"
	"github.com/ugparu/gomedia/utils/lifecycle"
	"github.com/ugparu/gomedia/utils/logger"
)

type AudioDecoderParam func(*audioDecoder)

func AudioWithName(name string) AudioDecoderParam {
	return func(dec *audioDecoder) { dec.name = name }
}

func AudioWithLogger(l logger.Logger) AudioDecoderParam {
	return func(dec *audioDecoder) { dec.log = l }
}

type InnerAudioDecoder interface {
	Init(params gomedia.AudioCodecParameters) error
	Decode([]byte) ([]byte, error)
	Close()
}

type audioDecoder struct {
	lifecycle.AsyncManager[*audioDecoder]
	InnerAudioDecoder
	factory    map[gomedia.CodecType]func() InnerAudioDecoder
	inpPackets chan gomedia.AudioPacket
	outPackets chan gomedia.AudioPacket
	codecPar   gomedia.AudioCodecParameters
	pcmPar     *pcm.CodecParameters
	inBuf      buffer.PooledBuffer
	name       string
	log        logger.Logger
}

func NewAudioDecoder(chanSize int, factory map[gomedia.CodecType]func() InnerAudioDecoder, params ...AudioDecoderParam) gomedia.AudioDecoder {
	d := &audioDecoder{
		AsyncManager:      nil,
		InnerAudioDecoder: nil,
		factory:           factory,
		inpPackets:        make(chan gomedia.AudioPacket, chanSize),
		outPackets:        make(chan gomedia.AudioPacket, chanSize),
		codecPar:          nil,
		pcmPar:            nil,
		inBuf:             buffer.Get(0),
		name:              "AUDIO_DECODER",
		log:               logger.Default,
	}

	for _, param := range params {
		param(d)
	}

	d.AsyncManager = lifecycle.NewFailSafeAsyncManager(d, d.log)
	return d
}

func (d *audioDecoder) Decode() {
	startFunc := func(_ *audioDecoder) error {
		return nil
	}
	// Ignoring error as there's no handling needed
	_ = d.Start(startFunc)
}

func (d *audioDecoder) updateCodecPar(inpPar gomedia.AudioCodecParameters) (err error) {
	d.pcmPar = pcm.NewCodecParameters(inpPar.StreamIndex(), gomedia.PCM, inpPar.Channels(), inpPar.SampleRate())
	d.codecPar = inpPar

	decoderFn, ok := d.factory[inpPar.Type()]
	if !ok {
		return errors.New("unsupported audio codec")
	}

	d.InnerAudioDecoder = decoderFn()
	return d.InnerAudioDecoder.Init(inpPar)
}

func (d *audioDecoder) Step(stopCh <-chan struct{}) (err error) {
	select {
	case <-stopCh:
		return &lifecycle.BreakError{}
	case p := <-d.inpPackets:
		defer p.Release()
		if p.CodecParameters() != d.codecPar {
			if err = d.updateCodecPar(p.CodecParameters()); err != nil {
				return
			}
		}

		d.inBuf.Resize(p.Len())
		copy(d.inBuf.Data(), p.Data())

		var dPCM []byte
		if dPCM, err = d.InnerAudioDecoder.Decode(d.inBuf.Data()); err != nil || len(dPCM) == 0 {
			return
		}
		select {
		case d.outPackets <- pcm.NewPacket(
			dPCM,
			p.Timestamp(),
			p.SourceID(),
			p.StartTime(),
			d.pcmPar,
			p.Duration(),
		):
		case <-stopCh:
			return &lifecycle.BreakError{}
		}
	}
	return
}

func (d *audioDecoder) Packets() chan<- gomedia.AudioPacket {
	return d.inpPackets
}

func (d *audioDecoder) Samples() <-chan gomedia.AudioPacket {
	return d.outPackets
}

func (d *audioDecoder) Close() {
	d.AsyncManager.Close()
}

func (d *audioDecoder) Release() { //nolint:revive // required by lifecycle.AsyncInstance interface
	if d.InnerAudioDecoder != nil {
		d.InnerAudioDecoder.Close()
	}
	// Drain remaining packets from the channel to prevent leaks.
	for {
		select {
		case pkt, ok := <-d.inpPackets:
			if !ok {
				goto drained
			}
			pkt.Release()
		default:
			close(d.inpPackets)
			goto drained
		}
	}
drained:
	close(d.outPackets)
	d.inBuf.Release()
}

func (d *audioDecoder) String() string {
	return "ADECODER"
}
