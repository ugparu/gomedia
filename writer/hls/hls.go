package hls

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/format/hls"
	"github.com/ugparu/gomedia/utils"
	"github.com/ugparu/gomedia/utils/lifecycle"
	"github.com/ugparu/gomedia/utils/logger"
)

// hlsWriter is a struct representing an HLS (HTTP Live Streaming) writer.
type hlsWriter struct {
	lifecycle.AsyncManager[*hlsWriter]
	segmentCount    uint8
	segmentDuration time.Duration
	id              uint64

	inpPktCh            chan gomedia.Packet
	segmentDurationChan chan time.Duration
	rmSrcCh             chan string

	muxerIDs   map[uint8]gomedia.HLSMuxer
	muxerURLs  map[string]gomedia.HLSMuxer
	codPars    map[string]*gomedia.CodecParametersPair
	sortedURLs []string
	mu         sync.RWMutex
	master     string
}

func New(id uint64, segCnt uint8, segDur time.Duration, chanSize int) gomedia.HLSStreamer {
	hwr := &hlsWriter{
		AsyncManager:    nil,
		segmentCount:    segCnt,
		segmentDuration: segDur,
		id:              id,

		inpPktCh:            make(chan gomedia.Packet, chanSize),
		segmentDurationChan: make(chan time.Duration, chanSize),
		rmSrcCh:             make(chan string, chanSize),

		muxerIDs:   map[uint8]gomedia.HLSMuxer{},
		muxerURLs:  make(map[string]gomedia.HLSMuxer),
		codPars:    map[string]*gomedia.CodecParametersPair{},
		sortedURLs: []string{},
		mu:         sync.RWMutex{},
		master:     "",
	}
	hwr.AsyncManager = lifecycle.NewFailSafeAsyncManager(hwr)
	return hwr
}

func (hlsw *hlsWriter) checkCodPar(url string, codecPar gomedia.CodecParameters) (err error) {
	if codecPar.Type().String() == "UNKNOWN" {
		return errors.New("unknown codec type")
	}

	if _, ok := hlsw.codPars[url]; !ok {
		hlsw.codPars[url] = &gomedia.CodecParametersPair{
			URL:                  url,
			AudioCodecParameters: nil,
			VideoCodecParameters: nil,
		}
		hlsw.sortedURLs = append(hlsw.sortedURLs, url)
	}

	switch par := codecPar.(type) {
	case gomedia.VideoCodecParameters:
		if hlsw.codPars[url].VideoCodecParameters == par {
			return
		}
		hlsw.codPars[url].VideoCodecParameters = par
	case gomedia.AudioCodecParameters:
		if hlsw.codPars[url].AudioCodecParameters == par {
			return
		}
		hlsw.codPars[url].AudioCodecParameters = par
	default:
		return
	}

	for url, par := range hlsw.codPars {
		mux, ok := hlsw.muxerURLs[par.URL]
		if ok {
			mux.Close()
		}
		mux = hls.NewHLSMuxer(hlsw.segmentDuration, hlsw.segmentCount)
		logger.Infof(hlsw, "Muxing %s", par.URL)
		if err = mux.Mux(*par); err != nil {
			return
		}
		hlsw.muxerURLs[url] = mux
	}

	return hlsw.recalcManifest()
}

func (hlsw *hlsWriter) removeSrc(url string) error {
	logger.Infof(hlsw, "Removing source %s", url)

	delete(hlsw.codPars, url)
	delete(hlsw.muxerURLs, url)

	if idx := slices.Index(hlsw.sortedURLs, url); idx != -1 {
		hlsw.sortedURLs = slices.Delete(hlsw.sortedURLs, idx, idx+1)
	}
	return hlsw.recalcManifest()
}

func (hlsw *hlsWriter) recalcManifest() (err error) {
	clear(hlsw.muxerIDs)

	for i := len(hlsw.sortedURLs) - 1; i >= 1; i-- {
		var oldResolution uint
		if hlsw.codPars[hlsw.sortedURLs[i-1]].VideoCodecParameters != nil {
			oldResolution = hlsw.codPars[hlsw.sortedURLs[i-1]].VideoCodecParameters.Width() *
				hlsw.codPars[hlsw.sortedURLs[i-1]].VideoCodecParameters.Height()
		}
		var newResolution uint
		if hlsw.codPars[hlsw.sortedURLs[i]].VideoCodecParameters != nil {
			newResolution = hlsw.codPars[hlsw.sortedURLs[i]].VideoCodecParameters.Width() *
				hlsw.codPars[hlsw.sortedURLs[i]].VideoCodecParameters.Height()
		}

		if oldResolution > newResolution {
			hlsw.sortedURLs[i-1], hlsw.sortedURLs[i] = hlsw.sortedURLs[i], hlsw.sortedURLs[i-1]
		} else {
			break
		}
	}

	var builder strings.Builder
	if _, err = builder.WriteString("#EXTM3U\n#EXT-X-VERSION:7\n"); err != nil {
		return
	}
	hlsw.mu.Lock()
	defer hlsw.mu.Unlock()

	index := uint8(0)
	for _, url := range hlsw.sortedURLs {
		hlsw.muxerIDs[index] = hlsw.muxerURLs[url]

		var entry string
		if entry, err = hlsw.muxerURLs[url].GetMasterEntry(); err != nil {
			return err
		}
		if _, err = builder.WriteString(fmt.Sprintf("%s\n", entry)); err != nil {
			return
		}
		if _, err = builder.WriteString(fmt.Sprintf("%d/%d/cubic.m3u8\n", hlsw.id, index)); err != nil {
			return
		}
		index++
	}

	hlsw.master = builder.String()
	return
}

func (hlsw *hlsWriter) Write() {
	startFunc := func(*hlsWriter) error {
		return nil
	}
	_ = hlsw.Start(startFunc)
}

// Step performs a single step in the processing of media data.
// It listens to the stopCh channel for termination signals.
// If stopCh is closed, it returns a BreakError.
// If a media packet is received on inpPktCh, it writes the packet to the associated HLS Muxer.
// If a map of codec parameters is received on paramsChan, it updates the HLS parameters.
func (hlsw *hlsWriter) Step(stopCh <-chan struct{}) (err error) {
	select {
	case <-stopCh:
		return &lifecycle.BreakError{}
	case url := <-hlsw.rmSrcCh:
		return hlsw.removeSrc(url)
	case inpPkt := <-hlsw.inpPktCh:
		logger.Tracef(hlsw, "Received packet %v", inpPkt)

		if inpPkt == nil {
			return &utils.NilPacketError{}
		}

		switch pkt := inpPkt.(type) {
		case gomedia.VideoPacket:
			if err = hlsw.checkCodPar(inpPkt.URL(), pkt.CodecParameters()); err != nil {
				return
			}
		case gomedia.AudioPacket:
			if err = hlsw.checkCodPar(inpPkt.URL(), pkt.CodecParameters()); err != nil {
				return
			}
		}

		if err = hlsw.muxerURLs[inpPkt.URL()].WritePacket(inpPkt); err != nil {
			return err
		}
	case hlsw.segmentDuration = <-hlsw.segmentDurationChan:
	}
	return nil
}

// getMasterPlaylist returns the master playlist string stored in the innerHLS instance.
func (hlsw *hlsWriter) GetMasterPlaylist() (string, error) {
	return hlsw.master, nil
}

// getIndexM3u8 retrieves the M3U8 playlist for a specific index using the innerHLS instance.
// It takes context, index, needMSN (Media Sequence Number), and needPart as parameters.
func (hlsw *hlsWriter) GetIndexM3u8(ctx context.Context, index uint8, needMSN int64, needPart int8) (string, error) {
	hlsw.mu.RLock()
	defer hlsw.mu.RUnlock()
	mux, found := hlsw.muxerIDs[index]
	if !found {
		return "", fmt.Errorf("output %d not found", index)
	}
	return mux.GetIndexM3u8(ctx, needMSN, needPart)
}

// getInit retrieves the initialization segment for a specific index using the innerHLS instance.
func (hlsw *hlsWriter) GetInit(index uint8) ([]byte, error) {
	hlsw.mu.RLock()
	defer hlsw.mu.RUnlock()
	mux, found := hlsw.muxerIDs[index]
	if !found {
		return nil, fmt.Errorf("output %d not found", index)
	}
	return mux.GetInit()
}

// getSegment retrieves the media segment for a specific index and segment index using the innerHLS instance.
// It takes context, index, and segIndex as parameters.
func (hlsw *hlsWriter) GetSegment(ctx context.Context, index uint8, segIndex uint64) ([]byte, error) {
	hlsw.mu.RLock()
	defer hlsw.mu.RUnlock()
	mux, found := hlsw.muxerIDs[index]

	if !found {
		return nil, fmt.Errorf("output %d not found", index)
	}
	return mux.GetSegment(ctx, segIndex)
}

func (hlsw *hlsWriter) GetFragment(ctx context.Context, index uint8, segIndex uint64, fragIndex uint8) ([]byte, error) {
	hlsw.mu.RLock()
	defer hlsw.mu.RUnlock()
	mux, found := hlsw.muxerIDs[index]
	if !found {
		return nil, fmt.Errorf("output %d not found", index)
	}
	return mux.GetFragment(ctx, segIndex, fragIndex)
}

// Close closes the innerHLS instance by closing all associated HLS Muxers, inpPktCh, and paramsChan channels.
func (hlsw *hlsWriter) Close_() { //nolint: revive
	for _, mux := range hlsw.muxerURLs {
		mux.Close()
	}
	close(hlsw.inpPktCh)
}

func (hlsw *hlsWriter) String() string {
	return fmt.Sprintf("HLS_WRITER id=%d mxrs=%d", hlsw.id, len(hlsw.muxerURLs))
}

// Packets returns the channel for sending media packets using the innerHLS instance of outerHLS.
func (hlsw *hlsWriter) Packets() chan<- gomedia.Packet {
	return hlsw.inpPktCh
}

// Packets returns the channel for sending media packets using the innerHLS instance of outerHLS.
func (hlsw *hlsWriter) SegmentDuration() chan<- time.Duration {
	return hlsw.segmentDurationChan
}

func (hlsw *hlsWriter) RemoveSource() chan<- string {
	return hlsw.rmSrcCh
}
