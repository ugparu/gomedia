// Package reader provides functionality for reading media content
// and extracting packets and parameters.
package reader

import (
	"net/url"
	"sync"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/format/rtsp"
	"github.com/ugparu/gomedia/utils/lifecycle"
	"github.com/ugparu/gomedia/utils/logger"
)

const (
	maxReconnectInterval = time.Second * 8 // exponential backoff cap for reconnect
)

type Option func(*reader)

func WithLogger(l logger.Logger) Option {
	return func(r *reader) { r.log = l }
}

func WithRTSPParams(params ...rtsp.DemuxerOption) Option {
	return func(r *reader) { r.opts = params }
}

// reader fans packets from many RTSP demuxers (one per URL) into a single
// channel. Each demuxer runs in its own goroutine; Step only handles URL
// add/remove.
type reader struct {
	lifecycle.AsyncManager[*reader]
	log         logger.Logger
	newDmx      func(string, ...rtsp.DemuxerOption) gomedia.Demuxer
	packets     chan gomedia.Packet
	addURLCh    chan string
	removeURLCh chan string
	dmxStoppers map[string]chan struct{}
	name        string
	mu          sync.Mutex
	opts        []rtsp.DemuxerOption
}

func NewRTSP(chanSize int, opts ...Option) gomedia.Reader {
	rdr := &reader{
		AsyncManager: nil,
		log:          logger.Default,
		newDmx:       rtsp.New,
		packets:      make(chan gomedia.Packet, chanSize),
		addURLCh:     make(chan string, chanSize),
		removeURLCh:  make(chan string, chanSize),
		dmxStoppers:  make(map[string]chan struct{}),
		name:         "READER",
		mu:           sync.Mutex{},
		opts:         nil,
	}

	for _, o := range opts {
		o(rdr)
	}

	rdr.AsyncManager = lifecycle.NewFailSafeAsyncManager(rdr, rdr.log)
	return rdr
}

func (rdr *reader) repackPackets(src string, stopCh <-chan struct{}) {
	defer func() {
		if r := recover(); r != nil {
			rdr.log.Errorf(rdr, "Panic in repackPackets: %v", r)
		}
	}()

	recInterval := time.Second
	videoOffsetHandler := new(offsetHandler)
	audioOffsetHandler := new(offsetHandler)

	opts := append([]rtsp.DemuxerOption{rtsp.WithLogger(rdr.log)}, rdr.opts...)
	dmx := rdr.newDmx(src, opts...)
	pars, err := dmx.Demux()
	if err != nil {
		rdr.log.Warningf(rdr, "Failed to start demuxer: %s", err.Error())
	} else {
		rdr.log.Infof(rdr, "Demuxer started. Video: %t, Audio: %t",
			pars.VideoCodecParameters != nil, pars.AudioCodecParameters != nil)
	}

	var pktCnt float64
	for {
		select {
		case <-stopCh:
			videoOffsetHandler.releaseLastPacket()
			audioOffsetHandler.releaseLastPacket()
			return
		default:
		}

		rdr.log.Trace(rdr, "Trying to read new packet")

		var pkt gomedia.Packet
		pkt, err = dmx.ReadPacket()
		if err != nil {
			recInterval, dmx = rdr.handleReadError(dmx, src, recInterval, stopCh, videoOffsetHandler, audioOffsetHandler, err)
			continue
		}

		if pkt == nil {
			continue
		}
		rdr.log.Tracef(rdr, "Read new packet %v", pkt)

		pktCnt++

		switch pkt.(type) {
		case gomedia.VideoPacket:
			if videoOffsetHandler.CheckEmptyPacket(pkt) {
				continue
			}
			videoOffsetHandler.CheckTSWrap(pkt)
			if !videoOffsetHandler.applyToPkt(pkt) {
				pkt.Release()
				continue
			}

			videoOffsetHandler.lastPacket.SetDuration(pkt.Timestamp() - videoOffsetHandler.lastPacket.Timestamp())
			videoOffsetHandler.lastDuration = videoOffsetHandler.lastPacket.Duration()

			select {
			case rdr.packets <- videoOffsetHandler.lastPacket:
				videoOffsetHandler.lastPacket = pkt
			case <-stopCh:
				pkt.Release()
				videoOffsetHandler.releaseLastPacket()
				audioOffsetHandler.releaseLastPacket()
				return
			}
		case gomedia.AudioPacket:
			if audioOffsetHandler.CheckEmptyPacket(pkt) {
				continue
			}
			audioOffsetHandler.CheckTSWrap(pkt)
			if !audioOffsetHandler.applyToPkt(pkt) {
				pkt.Release()
				continue
			}
			audioOffsetHandler.lastDuration = audioOffsetHandler.lastPacket.Duration()

			select {
			case rdr.packets <- audioOffsetHandler.lastPacket:
				audioOffsetHandler.lastPacket = pkt
			case <-stopCh:
				pkt.Release()
				videoOffsetHandler.releaseLastPacket()
				audioOffsetHandler.releaseLastPacket()
				return
			}
		default:
			pkt.Release()
		}
	}
}

// handleReadError tears down the failing demuxer, sleeps for recInterval, and
// opens a new one. On repeated failure the interval doubles up to
// maxReconnectInterval and further log messages are silenced to avoid spam.
// Offset handlers are rewound on success so timestamps stay monotonic across
// the reconnection boundary.
func (rdr *reader) handleReadError(dmx gomedia.Demuxer, src string, recInterval time.Duration,
	stopCh <-chan struct{}, videoHandler, audioHandler *offsetHandler, readErr error) (time.Duration, gomedia.Demuxer) {
	if recInterval < maxReconnectInterval {
		rdr.log.Warningf(rdr, "Packet read error: %s", readErr.Error())
		rdr.log.Infof(rdr, "Restarting demuxer with %.fs interval", recInterval.Seconds())
	}

	rdr.log.Debug(rdr, "Closing demuxer")
	dmx.Close()

	select {
	case <-time.After(recInterval):
	case <-stopCh:
		return recInterval, dmx
	}

	rdr.log.Debug(rdr, "Creating new demuxer")
	reconOpts := append([]rtsp.DemuxerOption{rtsp.WithLogger(rdr.log)}, rdr.opts...)
	dmx = rdr.newDmx(src, reconOpts...)

	rdr.log.Debug(rdr, "Starting demuxing")
	par, err := dmx.Demux()
	if err != nil {
		if recInterval < maxReconnectInterval {
			rdr.log.Warningf(rdr, "Failed to start demuxer: %s", err.Error())
		}
		return rdr.updateReconnectInterval(recInterval), dmx
	}

	rdr.log.Infof(rdr, "Demuxer started. Video: %t, Audio: %t",
		par.VideoCodecParameters != nil, par.AudioCodecParameters != nil)

	videoHandler.RecalcForGap()
	audioHandler.RecalcForGap()

	return time.Second, dmx
}

func (rdr *reader) updateReconnectInterval(current time.Duration) time.Duration {
	if current >= maxReconnectInterval {
		return current
	}

	const scaleFactor = 2
	newInterval := current * scaleFactor

	if newInterval >= maxReconnectInterval {
		newInterval = maxReconnectInterval
		rdr.log.Infof(rdr, "Max reconnect interval reached. Further attempts will be silent")
	}

	return newInterval
}

func (rdr *reader) Step(stopCh <-chan struct{}) (err error) {
	rdr.log.Trace(rdr, "Running reader step")
	select {
	case <-stopCh:
		return &lifecycle.BreakError{}
	case src := <-rdr.addURLCh:
		rdr.log.Infof(rdr, "Adding new URL %s", src)

		parsedURL, err := url.Parse(src)
		if err != nil {
			rdr.log.Errorf(rdr, "Failed to parse URL %s: %s", src, err.Error())
			return err
		}
		rdr.name = "READER " + parsedURL.Hostname()

		rStopCh := make(chan struct{})
		rdr.mu.Lock()
		rdr.dmxStoppers[src] = rStopCh
		rdr.mu.Unlock()
		go rdr.repackPackets(src, rStopCh)
	case src := <-rdr.removeURLCh:
		rdr.log.Infof(rdr, "Removing URL %s", src)
		rdr.mu.Lock()
		if dmxStopCh, ok := rdr.dmxStoppers[src]; ok {
			close(dmxStopCh)
			delete(rdr.dmxStoppers, src)
		}
		rdr.mu.Unlock()
	}
	return
}

func (rdr *reader) Read() {
	startFunc := func(*reader) error {
		return nil
	}
	_ = rdr.Start(startFunc)
}

// Release signals every demuxer goroutine to stop and drains the packet
// channel so ring-buffer slot handles are released instead of leaked.
func (rdr *reader) Release() { //nolint: revive
	rdr.mu.Lock()
	defer rdr.mu.Unlock()

	rdr.log.Infof(rdr, "Closing reader")

	for src, stopCh := range rdr.dmxStoppers {
		close(stopCh)
		delete(rdr.dmxStoppers, src)
	}

	for {
		select {
		case pkt := <-rdr.packets:
			if pkt != nil {
				pkt.Release()
			}
		default:
			return
		}
	}
}

func (rdr *reader) Packets() <-chan gomedia.Packet {
	return rdr.packets
}

func (rdr *reader) String() string {
	return rdr.name
}

func (rdr *reader) AddURL() chan<- string {
	return rdr.addURLCh
}

func (rdr *reader) RemoveURL() chan<- string {
	return rdr.removeURLCh
}
