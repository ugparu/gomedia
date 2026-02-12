package decoder

import (
	"errors"
	"fmt"
	"image"
	"runtime"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/utils/lifecycle"
	"github.com/ugparu/gomedia/utils/logger"
)

var ErrNeedMoreData = errors.New("need more data to decode frame")

type InnerVideoDecoder interface {
	Init(codecPar gomedia.VideoCodecParameters) (err error)
	Feed(pkt gomedia.VideoPacket) error
	Decode(pkt gomedia.VideoPacket) (image.Image, error)
	Close()
}

// videoDecoder represents the inner layer of the video decoder.
type videoDecoder struct {
	lifecycle.AsyncManager[*videoDecoder]
	InnerVideoDecoder
	newDecoderFn  func() InnerVideoDecoder
	inpPktCh      chan gomedia.VideoPacket     // Channel for receiving multimedia packets.
	outFrmCh      chan image.Image             // Channel for sending decoded video frames.
	codecPar      gomedia.VideoCodecParameters // Video codec parameters.
	fpsChan       chan int                     // Channel for sending frames per second.
	targetFPS     int                          // Target frames per second.
	frameDuration time.Duration                // Duration between frames, computed from FPS.
	lastFrameTime time.Time                    // Time of the last decoded frame.
	running       bool                         // Flag indicating whether the decoder is running.
	hasKey        bool
}

func NewVideo(chanSize int, fps int, newDecoderFn func() InnerVideoDecoder) gomedia.VideoDecoder {
	dec := &videoDecoder{
		AsyncManager:      nil,
		InnerVideoDecoder: nil,
		newDecoderFn:      newDecoderFn,
		inpPktCh:          make(chan gomedia.VideoPacket, chanSize),
		outFrmCh:          make(chan image.Image, chanSize),
		codecPar:          nil,
		fpsChan:           make(chan int, chanSize),
		targetFPS:         fps,
		frameDuration:     DurationFromFPS(fps),
		running:           false,
		hasKey:            false,
	}
	dec.AsyncManager = lifecycle.NewFailSafeAsyncManager(dec)
	runtime.SetFinalizer(dec, func(dcd *videoDecoder) { dcd.Close() })

	return dec
}

// processPacket processes the given multimedia packet.
// It sends the packet for decoding, processes the resulting frames,
// and sends the decoded frames to the output channel.
func (dec *videoDecoder) processPacket(inpPkt gomedia.VideoPacket, stopCh <-chan struct{}) (err error) {
	logger.Tracef(dec, "Processing packet %v", inpPkt)

	if inpPkt.CodecParameters() != dec.codecPar {
		if inpPkt.CodecParameters().Type().String() == "UNKNOWN" {
			return errors.New("unknown codec type")
		}

		logger.Infof(dec, "Changing codec parameters from %v to %v", dec.codecPar, inpPkt.CodecParameters())

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
		logger.Tracef(dec, "Skipping non-key frame %v", inpPkt)
		return
	}
	dec.hasKey = true

	const delta = time.Millisecond * 10
	if dec.frameDuration > 0 && time.Since(dec.lastFrameTime) < dec.frameDuration-delta {
		logger.Tracef(dec, "Skipping frame due to fps limit %v", inpPkt)
		return dec.InnerVideoDecoder.Feed(inpPkt)
	}

	var img image.Image
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
		logger.Tracef(dec, "Sent frame %v", inpPkt)
		return
	}
}

// processPacket processes the given multimedia packet.
// It sends the packet for decoding, processes the resulting frames,
// and sends the decoded frames to the output channel.
func (dec *videoDecoder) startDecoder() (err error) {
	logger.Debugf(dec, "Starting decoder with codec parameters %v", dec.codecPar)
	if dec.codecPar == nil {
		return errors.New("can not start with empty video codec parameters")
	}

	dec.InnerVideoDecoder = dec.newDecoderFn()

	if err = dec.InnerVideoDecoder.Init(dec.codecPar); err != nil {
		return
	}

	dec.running = true
	dec.hasKey = false
	return nil
}

// stopDecoder stops the inner decoder.
func (dec *videoDecoder) stopDecoder() {
	logger.Debugf(dec, "Stopping decoder")
	dec.running = false
	if dec.InnerVideoDecoder == nil {
		return
	}
	dec.InnerVideoDecoder.Close()
	dec.InnerVideoDecoder = nil
}

// Decode initializes the inner decoder.
func (dec *videoDecoder) Decode() {
	startFunc := func(dec *videoDecoder) error {
		dec.frameDuration = DurationFromFPS(dec.targetFPS)
		return nil
	}
	_ = dec.Start(startFunc)
}

// Step takes a step in the video decoding process based on signals received from channels.
func (dec *videoDecoder) Step(stopCh <-chan struct{}) (err error) {
	select {
	case <-stopCh:
		logger.Debug(dec, "Close signal detected. Breaking decoding...")
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
				case inpPkt := <-dec.inpPktCh:
					inpPkt.Close()
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
		defer inpPkt.Close()

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

// Close_ stops the inner decoder and closes associated channels.
func (dec *videoDecoder) Close_() { //nolint:revive // required by lifecycle.AsyncInstance interface
	dec.stopDecoder()
	close(dec.inpPktCh)
	close(dec.outFrmCh)
	close(dec.fpsChan)
}

// String returns a string representation of the inner video decoder.
func (dec *videoDecoder) String() string {
	return fmt.Sprintf("VIDEO_DECODER par=%v", dec.codecPar)
}

func (dec *videoDecoder) FPS() chan<- int {
	return dec.fpsChan
}

func (dec *videoDecoder) Packets() chan<- gomedia.VideoPacket {
	return dec.inpPktCh
}

func (dec *videoDecoder) Images() <-chan image.Image {
	return dec.outFrmCh
}

func DurationFromFPS(fps int) time.Duration {
	if fps > 0 {
		return time.Duration(1000/fps) * time.Millisecond //nolint:mnd // 1000ms in a second
	}
	return 0
}
