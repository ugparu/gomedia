package decoder

//go:generate mockgen -source=video_decoder.go -destination=../mocks/mock_video_decoder.go -package=mocks

import (
	"errors"
	"fmt"
	"runtime"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/frame/rgb"
	"github.com/ugparu/gomedia/utils/lifecycle"
	"github.com/ugparu/gomedia/utils/logger"
)

var ErrNeedMoreData = errors.New("need more data to decode frame")

type InnerVideoDecoder interface {
	Init(codecPar gomedia.VideoCodecParameters) (err error)
	Feed(pkt gomedia.VideoPacket) error
	Decode(pkt gomedia.VideoPacket) (rgb.ReleasableImage, error)
	Close()
}

// videoDecoder is the async wrapper around an InnerVideoDecoder. It applies
// FPS throttling, waits for the first key frame before decoding, and swaps
// out the inner decoder when the stream's codec parameters change.
type videoDecoder struct {
	lifecycle.AsyncManager[*videoDecoder]
	InnerVideoDecoder
	factory       map[gomedia.CodecType]func() InnerVideoDecoder
	inpPktCh      chan gomedia.VideoPacket
	outFrmCh      chan rgb.ReleasableImage
	codecPar      gomedia.VideoCodecParameters
	fpsChan       chan int
	targetFPS     int
	frameDuration time.Duration // 1/targetFPS; 0 disables throttling.
	lastFrameTime time.Time
	running       bool
	hasKey        bool
	name          string
	log           logger.Logger
	outBufSize    int
}

type VideoDecoderParam func(*videoDecoder)

func VideoWithName(name string) VideoDecoderParam {
	return func(dec *videoDecoder) { dec.name = name }
}

func VideoWithLogger(l logger.Logger) VideoDecoderParam {
	return func(dec *videoDecoder) { dec.log = l }
}

// VideoWithOutputBufferSize overrides the capacity of the decoder's output
// frame channel. Decoded RGB frames are large (3*W*H bytes), so callers may
// want a smaller output buffer than the input/fps buffers to bound memory
// when a downstream consumer stalls. Defaults to chanSize when unset.
func VideoWithOutputBufferSize(n int) VideoDecoderParam {
	return func(dec *videoDecoder) { dec.outBufSize = n }
}

func NewVideo(chanSize int, fps int, factory map[gomedia.CodecType]func() InnerVideoDecoder, params ...VideoDecoderParam) gomedia.VideoDecoder {
	dec := &videoDecoder{
		AsyncManager:      nil,
		InnerVideoDecoder: nil,
		factory:           factory,
		inpPktCh:          nil,
		outFrmCh:          nil,
		codecPar:          nil,
		fpsChan:           nil,
		targetFPS:         fps,
		frameDuration:     DurationFromFPS(fps),
		running:           false,
		hasKey:            false,
		log:               logger.Default,
		outBufSize:        chanSize,
	}
	for _, param := range params {
		param(dec)
	}
	dec.inpPktCh = make(chan gomedia.VideoPacket, chanSize)
	dec.outFrmCh = make(chan rgb.ReleasableImage, dec.outBufSize)
	dec.fpsChan = make(chan int, chanSize)
	dec.AsyncManager = lifecycle.NewFailSafeAsyncManager(dec, dec.log)
	runtime.SetFinalizer(dec, func(dcd *videoDecoder) { dcd.Close() })
	return dec
}

// processPacket decodes one packet into a frame and forwards it downstream.
// Codec-parameter changes trigger an inner-decoder restart. Packets before the
// first key frame are fed (so the decoder can build reference state) but no
// frame is emitted until the first key frame has been processed.
func (dec *videoDecoder) processPacket(inpPkt gomedia.VideoPacket, stopCh <-chan struct{}) (err error) {
	dec.log.Tracef(dec, "Processing packet %v", inpPkt)

	if inpPkt.CodecParameters() != dec.codecPar {
		dec.log.Infof(dec, "Changing codec parameters from %v to %v", dec.codecPar, inpPkt.CodecParameters())

		dec.codecPar = inpPkt.CodecParameters()
		dec.stopDecoder()
		if err = dec.startDecoder(); err != nil {
			dec.stopDecoder()
			return err
		}
	}

	if !dec.running {
		return errors.New("attempt to process packet on not inizialized decoder")
	}

	if !dec.hasKey && !inpPkt.IsKeyFrame() {
		dec.log.Tracef(dec, "Skipping non-key frame %v", inpPkt)
		return
	}
	dec.hasKey = true

	const delta = time.Millisecond * 10
	if dec.frameDuration > 0 && time.Since(dec.lastFrameTime) < dec.frameDuration-delta {
		dec.log.Tracef(dec, "Skipping frame due to fps limit %v", inpPkt)
		return dec.InnerVideoDecoder.Feed(inpPkt)
	}

	var img rgb.ReleasableImage
	if img, err = dec.InnerVideoDecoder.Decode(inpPkt); err != nil {
		if err.Error() == ErrNeedMoreData.Error() {
			return nil
		}
		return err
	}

	dec.lastFrameTime = time.Now()

	select {
	case <-stopCh:
		return &lifecycle.BreakError{}
	case dec.outFrmCh <- img:
		dec.log.Tracef(dec, "Sent frame %v", inpPkt)
		return
	}
}

// startDecoder spins up a fresh inner decoder for the current codec parameters.
func (dec *videoDecoder) startDecoder() (err error) {
	dec.log.Debugf(dec, "Starting decoder with codec parameters %v", dec.codecPar)
	if dec.codecPar == nil {
		return errors.New("can not start with empty video codec parameters")
	}

	decoderFn, ok := dec.factory[dec.codecPar.Type()]
	if !ok {
		return errors.New("unsupported video codec")
	}
	dec.InnerVideoDecoder = decoderFn()

	if err = dec.InnerVideoDecoder.Init(dec.codecPar); err != nil {
		return
	}

	dec.running = true
	dec.hasKey = false
	return nil
}

// stopDecoder stops the inner decoder.
func (dec *videoDecoder) stopDecoder() {
	dec.log.Debugf(dec, "Stopping decoder")
	dec.running = false
	if dec.InnerVideoDecoder == nil {
		return
	}
	dec.InnerVideoDecoder.Close()
	dec.InnerVideoDecoder = nil
}

// Decode launches the async Step loop.
func (dec *videoDecoder) Decode() {
	startFunc := func(dec *videoDecoder) error {
		dec.frameDuration = DurationFromFPS(dec.targetFPS)
		return nil
	}
	_ = dec.Start(startFunc)
}

// Step services one of: close, FPS change (including pause via fps==0), or a
// new incoming packet. A non-nil err is wrapped via startDecoder/stopDecoder
// so the lifecycle manager can recover with a clean inner decoder state.
func (dec *videoDecoder) Step(stopCh <-chan struct{}) (err error) {
	select {
	case <-stopCh:
		dec.log.Debug(dec, "Close signal detected. Breaking decoding...")
		return &lifecycle.BreakError{}
	case fps := <-dec.fpsChan:
		if fps == dec.targetFPS {
			return
		}
		defer func() { dec.targetFPS = fps }()

		dec.frameDuration = DurationFromFPS(fps)

		if fps == 0 && dec.targetFPS != 0 {
			dec.stopDecoder()
			for {
				select {
				case <-stopCh:
					return &lifecycle.BreakError{}
				case drainPkt := <-dec.inpPktCh:
					drainPkt.Release()
				default:
					return
				}
			}
		}
		if fps != 0 && dec.targetFPS == 0 {
			if err = dec.startDecoder(); err != nil {
				dec.stopDecoder()
				return err
			}
		}
	case inpPkt := <-dec.inpPktCh:
		defer inpPkt.Release()
		if dec.targetFPS == 0 {
			return errors.New("attempt to process packet on zero fps decoder")
		}
		if err = dec.processPacket(inpPkt, stopCh); err != nil {
			dec.stopDecoder()
			if err2 := dec.startDecoder(); err2 != nil {
				dec.stopDecoder()
				return errors.Join(err, err2)
			}
			return err
		}
	}
	return nil
}

func (dec *videoDecoder) Close() {
	dec.AsyncManager.Close()
}

// Release tears down the inner decoder and drains both channels so no ring
// buffer slots or decoded frames leak after shutdown.
func (dec *videoDecoder) Release() { //nolint:revive // required by lifecycle.AsyncInstance interface
	dec.stopDecoder()
	for {
		select {
		case pkt, ok := <-dec.inpPktCh:
			if !ok {
				goto drained
			}
			pkt.Release()
		default:
			close(dec.inpPktCh)
			goto drained
		}
	}
drained:
	for {
		select {
		case img, ok := <-dec.outFrmCh:
			if !ok {
				goto framesDrained
			}
			img.Release()
		default:
			close(dec.outFrmCh)
			goto framesDrained
		}
	}
framesDrained:
	close(dec.fpsChan)
}

func (dec *videoDecoder) String() string {
	return fmt.Sprintf("VDECODER %s", dec.name)
}

func (dec *videoDecoder) FPS() chan<- int {
	return dec.fpsChan
}

func (dec *videoDecoder) Packets() chan<- gomedia.VideoPacket {
	return dec.inpPktCh
}

func (dec *videoDecoder) Images() <-chan rgb.ReleasableImage {
	return dec.outFrmCh
}

func DurationFromFPS(fps int) time.Duration {
	if fps > 0 {
		return time.Duration(1000/fps) * time.Millisecond //nolint:mnd // 1000ms in a second
	}
	return 0
}
