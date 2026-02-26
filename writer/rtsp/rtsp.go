package rtsp

import (
	"fmt"

	"github.com/ugparu/gomedia"
	formatrtsp "github.com/ugparu/gomedia/format/rtsp"
	"github.com/ugparu/gomedia/utils"
	"github.com/ugparu/gomedia/utils/lifecycle"
	"github.com/ugparu/gomedia/utils/logger"
)

// rtspWriter is a Writer implementation that publishes a single video stream
// to a remote RTSP server using format/rtsp.Muxer.
//
// It is intentionally single-stream (one source URL, one destination URL)
// to mirror the existing rtsp-to-rtsp example while keeping the same Writer
// interface as other writers (HLS, WebRTC, Segmenter).
type rtspWriter struct {
	lifecycle.AsyncManager[*rtspWriter]

	srcURL   string
	dstURL   string
	muxer    gomedia.Muxer
	codecPar gomedia.CodecParametersPair

	inpPktCh chan gomedia.Packet
	rmSrcCh  chan string
	addSrcCh chan string

	started bool
}

// New creates a new RTSP writer that will publish packets from srcURL to dstURL.
// chanSize controls the size of internal channels.
func New(srcURL, dstURL string, chanSize int) gomedia.Writer {
	w := &rtspWriter{
		srcURL:   srcURL,
		dstURL:   dstURL,
		muxer:    nil,
		codecPar: gomedia.CodecParametersPair{},

		inpPktCh: make(chan gomedia.Packet, chanSize),
		rmSrcCh:  make(chan string, chanSize),
		addSrcCh: make(chan string, chanSize),

		started: false,
	}

	w.AsyncManager = lifecycle.NewFailSafeAsyncManager(w)
	return w
}

// Write starts the writer processing loop.
func (w *rtspWriter) Write() {
	startFunc := func(*rtspWriter) error {
		return nil
	}
	_ = w.Start(startFunc)
}

// Step processes one unit of work for the writer.
func (w *rtspWriter) Step(stopCh <-chan struct{}) (err error) {
	select {
	case <-stopCh:
		return &lifecycle.BreakError{}

	case url := <-w.addSrcCh:
		// Configure or reconfigure the source URL.
		if w.srcURL != "" && w.srcURL != url {
			logger.Warningf(w, "RTSP writer already has source %s, replacing with %s", w.srcURL, url)
		}
		w.srcURL = url
		w.resetMuxer()
	case url := <-w.rmSrcCh:
		if url == w.srcURL {
			logger.Infof(w, "Removing RTSP source %s", url)
			w.resetMuxer()
		}

	case pkt := <-w.inpPktCh:
		if pkt == nil {
			return &utils.NilPacketError{}
		}

		// If source URL is set, drop packets from other sources.
		if w.srcURL != "" && pkt.URL() != w.srcURL {
			return nil
		}

		switch p := pkt.(type) {
		case gomedia.VideoPacket:
			// Initialize muxer on first video packet if not started yet.
			if !w.started {
				if err = w.initMuxerFromVideoPacket(p); err != nil {
					return err
				}
			}

			if w.muxer == nil {
				return fmt.Errorf("rtsp writer muxer is not initialized")
			}

			if err = w.muxer.WritePacket(pkt); err != nil {
				w.resetMuxer()
				return err
			}

		default:
			// Ignore non-video packets for now, as RTSP muxer currently
			// only supports video packets. Close to free resources.
			return nil
		}
	}

	return nil
}

// initMuxerFromVideoPacket initializes the RTSP muxer using codec parameters
// from the first video packet.
func (w *rtspWriter) initMuxerFromVideoPacket(vp gomedia.VideoPacket) error {
	if w.dstURL == "" {
		return fmt.Errorf("rtsp writer destination URL is empty")
	}

	// Prepare codec parameters pair with only video for now.
	w.codecPar = gomedia.CodecParametersPair{
		URL:                  w.srcURL,
		AudioCodecParameters: nil,
		VideoCodecParameters: vp.CodecParameters(),
	}

	logger.Infof(w, "Initializing RTSP muxer for dst=%s src=%s", w.dstURL, w.srcURL)

	mx := formatrtsp.NewMuxer(w.dstURL)
	if err := mx.Mux(w.codecPar); err != nil {
		return err
	}

	w.muxer = mx
	w.started = true
	return nil
}

// resetMuxer closes and clears the current muxer and state.
func (w *rtspWriter) resetMuxer() {
	if w.muxer != nil {
		w.muxer.Close()
		w.muxer = nil
	}
	w.started = false
	w.codecPar = gomedia.CodecParametersPair{}
}

// Close_ is called by AsyncManager to gracefully stop the writer.
func (w *rtspWriter) Close_() { //nolint:revive
	w.resetMuxer()
	close(w.inpPktCh)
}

// Packets returns the input packet channel for the writer.
func (w *rtspWriter) Packets() chan<- gomedia.Packet {
	return w.inpPktCh
}

// RemoveSource returns the channel to remove a source URL.
func (w *rtspWriter) RemoveSource() chan<- string {
	return w.rmSrcCh
}

// AddSource returns the channel to add/configure a source URL.
func (w *rtspWriter) AddSource() chan<- string {
	return w.addSrcCh
}

// String implements fmt.Stringer for logging.
func (w *rtspWriter) String() string {
	return fmt.Sprintf("RTSP_WRITER src=%s dst=%s", w.srcURL, w.dstURL)
}
