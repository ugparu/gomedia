package rtsp

import (
	"fmt"

	"github.com/ugparu/gomedia"
	formatrtsp "github.com/ugparu/gomedia/format/rtsp"
	"github.com/ugparu/gomedia/utils"
	"github.com/ugparu/gomedia/utils/lifecycle"
	"github.com/ugparu/gomedia/utils/logger"
)

type Option func(*rtspWriter)

func WithLogger(l logger.Logger) Option {
	return func(w *rtspWriter) { w.log = l }
}

// rtspWriter republishes a single source to one remote RTSP destination,
// lazily building the muxer from the first video packet's codec parameters
// so upstream ordering between Demux and packet delivery doesn't matter.
type rtspWriter struct {
	lifecycle.AsyncManager[*rtspWriter]
	log logger.Logger

	srcURL   string
	dstURL   string
	muxer    gomedia.Muxer
	codecPar gomedia.CodecParametersPair

	inpPktCh chan gomedia.Packet
	rmSrcCh  chan string
	addSrcCh chan string

	started bool
}

func New(srcURL, dstURL string, chanSize int, opts ...Option) gomedia.Writer {
	w := &rtspWriter{
		log:      logger.Default,
		srcURL:   srcURL,
		dstURL:   dstURL,
		muxer:    nil,
		codecPar: gomedia.CodecParametersPair{},

		inpPktCh: make(chan gomedia.Packet, chanSize),
		rmSrcCh:  make(chan string, chanSize),
		addSrcCh: make(chan string, chanSize),

		started: false,
	}

	for _, o := range opts {
		o(w)
	}

	w.AsyncManager = lifecycle.NewFailSafeAsyncManager(w, w.log)
	return w
}

func (w *rtspWriter) Write() {
	startFunc := func(*rtspWriter) error {
		return nil
	}
	_ = w.Start(startFunc)
}

func (w *rtspWriter) Step(stopCh <-chan struct{}) (err error) {
	select {
	case <-stopCh:
		return &lifecycle.BreakError{}

	case url := <-w.addSrcCh:
		if w.srcURL != "" && w.srcURL != url {
			w.log.Warningf(w, "RTSP writer already has source %s, replacing with %s", w.srcURL, url)
		}
		w.srcURL = url
		w.resetMuxer()
	case url := <-w.rmSrcCh:
		if url == w.srcURL {
			w.log.Infof(w, "Removing RTSP source %s", url)
			w.resetMuxer()
		}

	case pkt := <-w.inpPktCh:
		if pkt == nil {
			return &utils.NilPacketError{}
		}

		if w.srcURL != "" && pkt.SourceID() != w.srcURL {
			pkt.Release()
			return nil
		}

		switch p := pkt.(type) {
		case gomedia.VideoPacket:
			if !w.started {
				if err = w.initMuxerFromVideoPacket(p); err != nil {
					pkt.Release()
					return err
				}
			}

			if w.muxer == nil {
				pkt.Release()
				return fmt.Errorf("rtsp writer muxer is not initialized")
			}

			if err = w.muxer.WritePacket(pkt); err != nil {
				pkt.Release()
				w.resetMuxer()
				return err
			}
			pkt.Release()

		default:
			// Audio is not yet plumbed through format/rtsp.Muxer; drop for now.
			pkt.Release()
			return nil
		}
	}

	return nil
}

func (w *rtspWriter) initMuxerFromVideoPacket(vp gomedia.VideoPacket) error {
	if w.dstURL == "" {
		return fmt.Errorf("rtsp writer destination URL is empty")
	}

	w.codecPar = gomedia.CodecParametersPair{
		SourceID:             w.srcURL,
		AudioCodecParameters: nil,
		VideoCodecParameters: vp.CodecParameters(),
	}

	w.log.Infof(w, "Initializing RTSP muxer for dst=%s src=%s", w.dstURL, w.srcURL)

	mx := formatrtsp.NewMuxer(w.dstURL, w.log)
	if err := mx.Mux(w.codecPar); err != nil {
		return err
	}

	w.muxer = mx
	w.started = true
	return nil
}

func (w *rtspWriter) resetMuxer() {
	if w.muxer != nil {
		w.muxer.Close()
		w.muxer = nil
	}
	w.started = false
	w.codecPar = gomedia.CodecParametersPair{}
}

func (w *rtspWriter) Release() { //nolint:revive
	w.resetMuxer()
	for {
		select {
		case pkt, ok := <-w.inpPktCh:
			if !ok {
				return
			}
			if pkt != nil {
				pkt.Release()
			}
		default:
			close(w.inpPktCh)
			return
		}
	}
}

func (w *rtspWriter) Packets() chan<- gomedia.Packet {
	return w.inpPktCh
}

func (w *rtspWriter) RemoveSource() chan<- string {
	return w.rmSrcCh
}

func (w *rtspWriter) AddSource() chan<- string {
	return w.addSrcCh
}

func (w *rtspWriter) String() string {
	return fmt.Sprintf("RTSP_WRITER src=%s dst=%s", w.srcURL, w.dstURL)
}
