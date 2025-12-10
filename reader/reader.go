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

// Constants for configuring the reader.
const (
	maxReconnectInternval = time.Second * 8
)

// reader is an internal structure implementing the gomedia.Reader interface.
type reader struct {
	lifecycle.AsyncManager[*reader] // Embedding an AsyncManager for asynchronous operations.
	newDmx                          func(string, ...gomedia.InputParameter) gomedia.Demuxer
	packets                         chan gomedia.Packet
	addURLCh                        chan string
	removeURLCh                     chan string
	dmxStoppers                     map[string]chan struct{}
	name                            string
	mu                              sync.Mutex
}

// NewRTSP creates a new RTSP reader with the specified URL and channel size.
func NewRTSP(chanSize int) gomedia.Reader {
	rdr := &reader{
		AsyncManager: nil,
		newDmx:       rtsp.New,
		packets:      make(chan gomedia.Packet, chanSize),
		addURLCh:     make(chan string, chanSize),
		removeURLCh:  make(chan string, chanSize),
		dmxStoppers:  make(map[string]chan struct{}),
		name:         "READER",
		mu:           sync.Mutex{},
	}

	rdr.AsyncManager = lifecycle.NewFailSafeAsyncManager(rdr)
	return rdr
}

func (rdr *reader) repackPackets(src string, stopCh <-chan struct{}) {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf(rdr, "Panic in repackPackets: %v", r)
		}
	}()

	recInterval := time.Second
	videoOffsetHandler := new(offsetHandler)
	audioOffsetHandler := new(offsetHandler)

	dmx := rdr.newDmx(src)
	pars, err := dmx.Demux()
	if err != nil {
		logger.Warningf(rdr, "Failed to start demuxer: %s", err.Error())
	} else {
		logger.Infof(rdr, "Demuxer started. Video: %t, Audio: %t",
			pars.VideoCodecParameters != nil, pars.AudioCodecParameters != nil)
	}

	var pktCnt float64
	for {
		select {
		case <-stopCh:
			return
		default:
		}

		logger.Trace(rdr, "Trying to read new packet")

		var pkt gomedia.Packet
		pkt, err = dmx.ReadPacket()
		if err != nil {
			recInterval, dmx = rdr.handleReadError(dmx, src, recInterval, stopCh, videoOffsetHandler, audioOffsetHandler, err)
			continue
		}

		// If the packet is nil, return.
		if pkt == nil {
			continue
		}

		logger.Tracef(rdr, "Read new packet %v", pkt)

		// Increment the packet count.
		pktCnt++

		switch pkt.(type) {
		case gomedia.VideoPacket:
			if videoOffsetHandler.CheckEmptyPacket(pkt) {
				continue
			}
			videoOffsetHandler.CheckTSWrap(pkt)
			if !videoOffsetHandler.applyToPkt(pkt) {
				continue
			}

			videoOffsetHandler.lastPacket.SetDuration(pkt.Timestamp() - videoOffsetHandler.lastPacket.Timestamp())
			// pkt.SetStartTime(videoOffsetHandler.lastPacket.StartTime().Add(videoOffsetHandler.lastPacket.Duration()))
			videoOffsetHandler.lastDuration = videoOffsetHandler.lastPacket.Duration()

			select {
			case rdr.packets <- videoOffsetHandler.lastPacket:
				videoOffsetHandler.lastPacket = pkt
			case <-stopCh:
				return
			}
		case gomedia.AudioPacket:
			if audioOffsetHandler.CheckEmptyPacket(pkt) {
				continue
			}
			audioOffsetHandler.CheckTSWrap(pkt)
			if !audioOffsetHandler.applyToPkt(pkt) {
				continue
			}
			// pkt.SetStartTime(audioOffsetHandler.lastPacket.StartTime().Add(audioOffsetHandler.lastPacket.Duration()))
			audioOffsetHandler.lastDuration = audioOffsetHandler.lastPacket.Duration()

			select {
			case rdr.packets <- pkt:
				audioOffsetHandler.lastPacket = pkt
			case <-stopCh:
				return
			}
		}
	}
}

// handleReadError handles packet read errors by closing the current demuxer,
// waiting to reconnect, and creating a new demuxer.
func (rdr *reader) handleReadError(dmx gomedia.Demuxer, src string, recInterval time.Duration,
	stopCh <-chan struct{}, videoHandler, audioHandler *offsetHandler, readErr error) (time.Duration, gomedia.Demuxer) {
	// Log only if we haven't reached the max reconnect interval yet
	if recInterval < maxReconnectInternval {
		logger.Warningf(rdr, "Packet read error: %s", readErr.Error())
		logger.Infof(rdr, "Restarting demuxer with %.fs interval", recInterval.Seconds())
	}

	logger.Debug(rdr, "Closing demuxer")
	// Close the demuxer
	dmx.Close()

	// Wait for reconnect interval or stop
	select {
	case <-time.After(recInterval):
	case <-stopCh:
		return recInterval, dmx
	}

	logger.Debug(rdr, "Creating new demuxer")
	// Create a new demuxer and start it
	dmx = rdr.newDmx(src)

	logger.Debug(rdr, "Starting demuxing")
	par, err := dmx.Demux()
	// Handle demux error
	if err != nil {
		logger.Warningf(rdr, "Failed to start demuxer: %s", err.Error())
		return rdr.updateReconnectInterval(recInterval), dmx
	}

	logger.Infof(rdr, "Demuxer started. Video: %t, Audio: %t",
		par.VideoCodecParameters != nil, par.AudioCodecParameters != nil)

	// Reset handlers after successful reconnect
	videoHandler.RecalcForGap()
	audioHandler.RecalcForGap()

	return time.Second, dmx // Reset interval on success
}

// updateReconnectInterval increases the reconnect interval exponentially up to the maximum
func (rdr *reader) updateReconnectInterval(current time.Duration) time.Duration {
	// Only increase and log if we're below the maximum
	if current >= maxReconnectInternval {
		return current
	}

	const scaleFactor = 2
	// Double the interval
	newInterval := current * scaleFactor

	// Check if we've exceeded the maximum after doubling
	if newInterval >= maxReconnectInternval {
		newInterval = maxReconnectInternval
		logger.Infof(rdr, "Max reconnect interval reached. Further attempts will be silent")
	}

	return newInterval
}

// Step performs a single step of reading from the RTSP stream.
func (rdr *reader) Step(stopCh <-chan struct{}) (err error) {
	logger.Trace(rdr, "Running reader step")
	select {
	case <-stopCh:
		return &lifecycle.BreakError{}
	case src := <-rdr.addURLCh:
		logger.Infof(rdr, "Adding new URL %s", src)

		parsedURL, err := url.Parse(src)
		if err != nil {
			logger.Errorf(rdr, "Failed to parse URL %s: %s", src, err.Error())
			return err
		}
		rdr.name = "READER " + parsedURL.Hostname()

		rStopCh := make(chan struct{})
		rdr.mu.Lock()
		rdr.dmxStoppers[src] = rStopCh
		rdr.mu.Unlock()
		go rdr.repackPackets(src, rStopCh)
	case src := <-rdr.removeURLCh:
		logger.Infof(rdr, "Removing URL %s", src)
		rdr.mu.Lock()
		if stopCh, ok := rdr.dmxStoppers[src]; ok {
			close(stopCh)
			delete(rdr.dmxStoppers, src)
		}
		rdr.mu.Unlock()
	}
	return
}

// Read initiates the demuxer and returns codec parameters.
func (rdr *reader) Read() {
	startFunc := func(*reader) error {
		return nil
	}
	_ = rdr.Start(startFunc)
}

// Close_ closes the demuxer and the packets channel.
func (rdr *reader) Close_() { //nolint: revive
	rdr.mu.Lock()
	defer rdr.mu.Unlock()

	logger.Infof(rdr, "Closing reader")

	for _, stopCh := range rdr.dmxStoppers {
		close(stopCh)
	}
}

// Packets returns the channel for receiving media packets.
func (rdr *reader) Packets() <-chan gomedia.Packet {
	return rdr.packets
}

// String returns a string representation of the reader.
func (rdr *reader) String() string {
	return rdr.name
}

func (rdr *reader) AddURL() chan<- string {
	return rdr.addURLCh
}

func (rdr *reader) RemoveURL() chan<- string {
	return rdr.removeURLCh
}
