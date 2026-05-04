package hls

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

const uidLen = 4 //nolint:mnd // 4 random bytes → 8 hex chars

func generateUID() string {
	var b [uidLen]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

type Option func(*hlsWriter)

func WithLogger(l logger.Logger) Option {
	return func(h *hlsWriter) { h.log = l }
}

// WithIndexName overrides the default index playlist filename ("index.m3u8").
func WithIndexName(name string) Option {
	return func(h *hlsWriter) { h.indexName = name }
}

// WithMediaName overrides the default media segment/fragment filename base ("media").
func WithMediaName(name string) Option {
	return func(h *hlsWriter) { h.mediaName = name }
}

// WithVersion overrides the default HLS protocol version (7).
func WithVersion(v int) Option {
	return func(h *hlsWriter) { h.version = v }
}

// WithKeyframeSplit defers segment rotation to the next video keyframe
// after the target duration expires. Default (false) rotates strictly on
// duration, which may split mid-GoP and hurt seek accuracy.
func WithKeyframeSplit(enabled bool) Option {
	return func(h *hlsWriter) { h.keyframeSplit = enabled }
}

// hlsWriter fans media packets to one HLS muxer per source URL and publishes
// a master playlist across all muxers. Each muxer rotates on segmentDuration
// and retains segmentCount live segments; reads are served under mu so the
// Step goroutine can safely rebuild muxerIDs when sources are added/removed.
type hlsWriter struct {
	lifecycle.AsyncManager[*hlsWriter]
	log             logger.Logger
	segmentCount    uint8
	segmentDuration time.Duration
	id              uint64

	inpPktCh chan gomedia.Packet
	addSrcCh chan string
	rmSrcCh  chan string

	muxerIDs      map[string]gomedia.HLSMuxer
	muxerURLs     map[string]gomedia.HLSMuxer
	muxerUIDs     map[string]string
	codPars       map[string]*gomedia.CodecParametersPair
	sortedURLs    []string
	mu            sync.RWMutex
	master        string
	indexName     string
	mediaName     string
	partHoldBack  float64
	version       int
	keyframeSplit bool
}

func New(id uint64, segCnt uint8, segDur time.Duration, chanSize int, partHoldBack float64, opts ...Option) gomedia.HLSStreamer {
	hwr := &hlsWriter{
		AsyncManager:    nil,
		log:             logger.Default,
		segmentCount:    segCnt,
		segmentDuration: segDur,
		id:              id,

		inpPktCh: make(chan gomedia.Packet, chanSize),
		addSrcCh: make(chan string, chanSize),
		rmSrcCh:  make(chan string, chanSize),

		muxerIDs:     map[string]gomedia.HLSMuxer{},
		muxerURLs:    make(map[string]gomedia.HLSMuxer),
		muxerUIDs:    make(map[string]string),
		codPars:      map[string]*gomedia.CodecParametersPair{},
		sortedURLs:   []string{},
		mu:           sync.RWMutex{},
		master:       "",
		indexName:    "index.m3u8",
		mediaName:    "media",
		partHoldBack: partHoldBack,
		version:      7,
	}

	for _, o := range opts {
		o(hwr)
	}

	hwr.log.Infof(hwr, "Initialized HLS writer with %d segments, %.2f seconds per segment, part hold back %.2f", segCnt, segDur.Seconds(), partHoldBack)
	hwr.AsyncManager = lifecycle.NewFailSafeAsyncManager(hwr, hwr.log)
	return hwr
}

func (hlsw *hlsWriter) checkCodPar(url string, codecPar gomedia.CodecParameters) (err error) {
	if codecPar.Type().String() == "UNKNOWN" {
		return errors.New("unknown codec type")
	}

	if _, ok := hlsw.codPars[url]; !ok {
		hlsw.codPars[url] = &gomedia.CodecParametersPair{
			SourceID:             url,
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
		hlsw.log.Infof(hlsw, "Setting new video codec parameters for url %s", url)
		hlsw.codPars[url].VideoCodecParameters = par
	case gomedia.AudioCodecParameters:
		if hlsw.codPars[url].AudioCodecParameters == par {
			return
		}
		hlsw.log.Infof(hlsw, "Setting new audio codec parameters for url %s", url)
		hlsw.codPars[url].AudioCodecParameters = par
	default:
		return
	}

	par := hlsw.codPars[url]
	if par.VideoCodecParameters == nil {
		return hlsw.recalcManifest()
	}

	mux, ok := hlsw.muxerURLs[url]
	if ok {
		if err = mux.UpdateCodecParameters(*par); err != nil {
			return
		}
		hlsw.muxerUIDs[url] = generateUID()
	} else {
		mux = hls.NewHLSMuxer(hlsw.segmentDuration, hlsw.segmentCount, hlsw.partHoldBack, hlsw.log, hls.WithMediaName(hlsw.mediaName), hls.WithVersion(hlsw.version), hls.WithKeyframeSplit(hlsw.keyframeSplit))
		if err = mux.Mux(*par); err != nil {
			return
		}
		hlsw.muxerURLs[url] = mux
		hlsw.muxerUIDs[url] = generateUID()
	}

	return hlsw.recalcManifest()
}

func (hlsw *hlsWriter) removeSrc(url string) error {
	hlsw.log.Infof(hlsw, "Removing source %s", url)

	if mux, ok := hlsw.muxerURLs[url]; ok {
		mux.Close()
	}
	delete(hlsw.codPars, url)
	delete(hlsw.muxerURLs, url)
	delete(hlsw.muxerUIDs, url)

	if idx := slices.Index(hlsw.sortedURLs, url); idx != -1 {
		hlsw.sortedURLs = slices.Delete(hlsw.sortedURLs, idx, idx+1)
	}
	return hlsw.recalcManifest()
}

func (hlsw *hlsWriter) recalcManifest() (err error) {
	slices.SortFunc(hlsw.sortedURLs, func(a, b string) int {
		var resA, resB uint
		if p := hlsw.codPars[a]; p != nil && p.VideoCodecParameters != nil {
			resA = p.VideoCodecParameters.Width() * p.VideoCodecParameters.Height()
		}
		if p := hlsw.codPars[b]; p != nil && p.VideoCodecParameters != nil {
			resB = p.VideoCodecParameters.Width() * p.VideoCodecParameters.Height()
		}
		if resA < resB {
			return -1
		}
		if resA > resB {
			return 1
		}
		return 0
	})

	var builder strings.Builder
	if _, err = builder.WriteString(fmt.Sprintf("#EXTM3U\n#EXT-X-VERSION:%d\n", hlsw.version)); err != nil {
		return
	}

	hlsw.mu.Lock()
	defer hlsw.mu.Unlock()

	clear(hlsw.muxerIDs)

	for _, url := range hlsw.sortedURLs {
		mux, ok := hlsw.muxerURLs[url]
		if !ok {
			continue
		}

		uid := hlsw.muxerUIDs[url]
		hlsw.muxerIDs[uid] = mux

		var entry string
		if entry, err = mux.GetMasterEntry(); err != nil {
			return err
		}
		if _, err = builder.WriteString(fmt.Sprintf("%s\n", entry)); err != nil {
			return
		}
		if _, err = builder.WriteString(fmt.Sprintf("%d/%s/%s\n", hlsw.id, uid, hlsw.indexName)); err != nil {
			return
		}
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

func (hlsw *hlsWriter) Step(stopCh <-chan struct{}) (err error) {
	select {
	case <-stopCh:
		return &lifecycle.BreakError{}
	case <-hlsw.addSrcCh:
		// Sources are auto-created on first packet via checkCodPar.
	case url := <-hlsw.rmSrcCh:
		return hlsw.removeSrc(url)
	case inpPkt := <-hlsw.inpPktCh:
		hlsw.log.Tracef(hlsw, "Received packet %v", inpPkt)

		if inpPkt == nil {
			return &utils.NilPacketError{}
		}

		switch pkt := inpPkt.(type) {
		case gomedia.VideoPacket:
			if err = hlsw.checkCodPar(inpPkt.SourceID(), pkt.CodecParameters()); err != nil {
				inpPkt.Release()
				return
			}
		case gomedia.AudioPacket:
			if err = hlsw.checkCodPar(inpPkt.SourceID(), pkt.CodecParameters()); err != nil {
				inpPkt.Release()
				return
			}
		}

		mux, ok := hlsw.muxerURLs[inpPkt.SourceID()]
		if !ok {
			inpPkt.Release()
			return
		}

		err = mux.WritePacket(inpPkt)
		inpPkt.Release()
		if err != nil {
			return err
		}
	}
	return nil
}

func (hlsw *hlsWriter) GetMasterPlaylist() (string, error) {
	hlsw.mu.RLock()
	defer hlsw.mu.RUnlock()
	return hlsw.master, nil
}

func (hlsw *hlsWriter) GetIndexM3u8(ctx context.Context, uid string, needMSN int64, needPart int8) (string, error) {
	hlsw.mu.RLock()
	defer hlsw.mu.RUnlock()
	mux, found := hlsw.muxerIDs[uid]
	if !found {
		return "", fmt.Errorf("output %s not found", uid)
	}
	return mux.GetIndexM3u8(ctx, needMSN, needPart)
}

func (hlsw *hlsWriter) GetInit(uid string) ([]byte, error) {
	hlsw.mu.RLock()
	defer hlsw.mu.RUnlock()
	mux, found := hlsw.muxerIDs[uid]
	if !found {
		return nil, fmt.Errorf("output %s not found", uid)
	}
	return mux.GetInit()
}

func (hlsw *hlsWriter) GetInitByVersion(uid string, version int) ([]byte, error) {
	hlsw.mu.RLock()
	defer hlsw.mu.RUnlock()
	mux, found := hlsw.muxerIDs[uid]
	if !found {
		return nil, fmt.Errorf("output %s not found", uid)
	}
	return mux.GetInitByVersion(version)
}

func (hlsw *hlsWriter) GetSegment(ctx context.Context, uid string, segIndex uint64) ([]byte, error) {
	hlsw.mu.RLock()
	defer hlsw.mu.RUnlock()
	mux, found := hlsw.muxerIDs[uid]
	if !found {
		return nil, fmt.Errorf("output %s not found", uid)
	}
	return mux.GetSegment(ctx, segIndex)
}

func (hlsw *hlsWriter) GetFragment(ctx context.Context, uid string, segIndex uint64, fragIndex uint8) ([]byte, error) {
	hlsw.mu.RLock()
	defer hlsw.mu.RUnlock()
	mux, found := hlsw.muxerIDs[uid]
	if !found {
		return nil, fmt.Errorf("output %s not found", uid)
	}
	return mux.GetFragment(ctx, segIndex, fragIndex)
}

// Release closes every per-source muxer and drains the input channel so any
// in-flight packets release their ring-buffer slots before the channel is closed.
func (hlsw *hlsWriter) Release() { //nolint: revive
	for _, mux := range hlsw.muxerURLs {
		mux.Close()
	}
	for {
		select {
		case pkt, ok := <-hlsw.inpPktCh:
			if !ok {
				return
			}
			if pkt != nil {
				pkt.Release()
			}
		default:
			close(hlsw.inpPktCh)
			return
		}
	}
}

func (hlsw *hlsWriter) String() string {
	return fmt.Sprintf("HLS_WRITER id=%d mxrs=%d", hlsw.id, len(hlsw.muxerURLs))
}

func (hlsw *hlsWriter) Packets() chan<- gomedia.Packet {
	return hlsw.inpPktCh
}

func (hlsw *hlsWriter) RemoveSource() chan<- string {
	return hlsw.rmSrcCh
}

func (hlsw *hlsWriter) AddSource() chan<- string {
	return hlsw.addSrcCh
}
