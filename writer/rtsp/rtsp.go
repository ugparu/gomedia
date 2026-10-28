package rtsp

import (
	"fmt"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/aac"
	"github.com/ugparu/gomedia/codec/opus"
	"github.com/ugparu/gomedia/codec/pcm"
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
// AAC audio is included when its codec parameters are known at init time;
// if audio arrives after a video-only announce, the muxer is re-announced
// with both tracks.
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
			if err = w.handleVideoPacket(p); err != nil {
				pkt.Release()
				return err
			}
			pkt.Release()

		case gomedia.AudioPacket:
			if err = w.handleAudioPacket(p); err != nil {
				pkt.Release()
				return err
			}
			pkt.Release()

		default:
			pkt.Release()
			return nil
		}
	}

	return nil
}

func (w *rtspWriter) handleVideoPacket(vp gomedia.VideoPacket) error {
	w.codecPar.SourceID = w.srcURL
	w.codecPar.VideoCodecParameters = vp.CodecParameters()

	if !w.started {
		// MediaMTX / players decode garbage (green frames) if the publish
		// session opens on a non-IDR. Drop until the first keyframe after
		// start or after resetMuxer (reconnect / AddSource).
		if !vp.IsKeyFrame() {
			return nil
		}
		if err := w.initMuxer(); err != nil {
			return err
		}
	}

	if w.muxer == nil {
		return fmt.Errorf("rtsp writer muxer is not initialized")
	}

	if err := w.muxer.WritePacket(vp); err != nil {
		w.closeMuxer()
		return err
	}
	return nil
}

func (w *rtspWriter) handleAudioPacket(ap gomedia.AudioPacket) error {
	cp := ap.CodecParameters()
	if !isSupportedRTSPAudio(cp) {
		return nil
	}

	w.codecPar.SourceID = w.srcURL

	if w.codecPar.AudioCodecParameters != nil {
		if w.codecPar.AudioCodecParameters.Type() != cp.Type() {
			// Stick to the codec announced for this publish session.
			return nil
		}
	} else {
		needReannounce := w.started
		w.codecPar.AudioCodecParameters = cp

		if !w.started {
			// Wait for video before announcing; audio-only publish is not used.
			return nil
		}

		if needReannounce {
			w.log.Infof(w, "Re-announcing RTSP muxer with %v audio dst=%s src=%s",
				cp.Type(), w.dstURL, w.srcURL)
			w.closeMuxer()
			if err := w.initMuxer(); err != nil {
				return err
			}
		}
	}

	if !w.started {
		return nil
	}

	if w.muxer == nil {
		return fmt.Errorf("rtsp writer muxer is not initialized")
	}

	if err := w.muxer.WritePacket(ap); err != nil {
		w.closeMuxer()
		return err
	}
	return nil
}

func isSupportedRTSPAudio(cp gomedia.AudioCodecParameters) bool {
	switch cp.(type) {
	case *aac.CodecParameters, *opus.CodecParameters, *pcm.CodecParameters:
		return true
	default:
		return false
	}
}

func (w *rtspWriter) initMuxer() error {
	if w.dstURL == "" {
		return fmt.Errorf("rtsp writer destination URL is empty")
	}
	if w.codecPar.VideoCodecParameters == nil {
		return fmt.Errorf("rtsp writer video codec parameters are empty")
	}

	w.log.Infof(w, "Initializing RTSP muxer for dst=%s src=%s audio=%v",
		w.dstURL, w.srcURL, w.codecPar.AudioCodecParameters != nil)

	mx := formatrtsp.NewMuxer(w.dstURL, w.log)
	if err := mx.Mux(w.codecPar); err != nil {
		return err
	}

	w.muxer = mx
	w.started = true
	return nil
}

func (w *rtspWriter) closeMuxer() {
	if w.muxer != nil {
		w.muxer.Close()
		w.muxer = nil
	}
	w.started = false
}

func (w *rtspWriter) resetMuxer() {
	w.closeMuxer()
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
