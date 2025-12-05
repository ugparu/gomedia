package decoder

import (
	"errors"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/pcm"
	"github.com/ugparu/gomedia/utils/buffer"
	"github.com/ugparu/gomedia/utils/lifecycle"
)

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
}

func NewAudioDecoder(chanSize int, factory map[gomedia.CodecType]func() InnerAudioDecoder) gomedia.AudioDecoder {
	d := &audioDecoder{
		AsyncManager:      nil,
		InnerAudioDecoder: nil,
		factory:           factory,
		inpPackets:        make(chan gomedia.AudioPacket, chanSize),
		outPackets:        make(chan gomedia.AudioPacket, chanSize),
		codecPar:          nil,
		pcmPar:            nil,
		inBuf:             buffer.Get(0),
	}

	d.AsyncManager = lifecycle.NewFailSafeAsyncManager(d)
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
		defer p.Close()

		if p.CodecParameters() != d.codecPar {
			if err = d.updateCodecPar(p.CodecParameters()); err != nil {
				return
			}
		}

		d.inBuf.Resize(len(p.Data()))
		copy(d.inBuf.Data(), p.Data())

		var dPCM []byte
		if dPCM, err = d.InnerAudioDecoder.Decode(d.inBuf.Data()); err != nil || len(dPCM) == 0 {
			return
		}
		select {
		case d.outPackets <- pcm.NewPacket(
			dPCM,
			p.Timestamp(),
			p.URL(),
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

func (d *audioDecoder) Close_() { //nolint:revive // required by lifecycle.AsyncInstance interface
	if d.InnerAudioDecoder != nil {
		d.InnerAudioDecoder.Close()
	}
	close(d.outPackets)
	d.inBuf.Release()
}

func (d *audioDecoder) String() string {
	return "LAW_DECODER"
}
