package hls

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/format/fmp4"
	"github.com/ugparu/gomedia/utils"
	"github.com/ugparu/gomedia/utils/lifecycle"
	"github.com/ugparu/gomedia/utils/logger"
)

const (
	fragmentDuration = time.Millisecond * 495
	maxTS            = time.Hour // wrap-around bound for relative timestamps
)

type segments struct {
	segments map[uint64]*segment
	sync.RWMutex
	segIDs []uint64
}

func (s *segments) addSegment(seg *segment) {
	s.Lock()
	defer s.Unlock()
	s.segments[seg.id] = seg
	s.segIDs = append(s.segIDs, seg.id)
}

func (s *segments) removeSegment(id uint64) {
	s.Lock()
	seg := s.segments[id]
	delete(s.segments, id)
	for i := 1; i < len(s.segIDs); i++ {
		s.segIDs[i-1] = s.segIDs[i]
	}
	s.segIDs = s.segIDs[:len(s.segIDs)-1]
	s.Unlock()

	if seg != nil {
		seg.release()
	}
}

func (s *segments) getSegment(id uint64) (*segment, bool) {
	s.RLock()
	defer s.RUnlock()
	seg, ok := s.segments[id]
	return seg, ok
}

func (s *segments) getCurSegment() *segment {
	s.RLock()
	defer s.RUnlock()
	return s.segments[s.segIDs[len(s.segIDs)-1]]
}

// MuxerOption is a functional option for configuring a muxer.
type MuxerOption func(*muxer)

// WithMediaName overrides the default media filename base ("media") used in segment and fragment URIs.
func WithMediaName(name string) MuxerOption {
	return func(m *muxer) { m.mediaName = name }
}

// WithFragmentDuration overrides the default fragment duration (495ms) used for LL-HLS partial segments.
func WithFragmentDuration(d time.Duration) MuxerOption {
	return func(m *muxer) { m.fragmentDuration = d }
}

// WithMaxTimestamp overrides the default max timestamp (1 hour) before wrap-around.
func WithMaxTimestamp(d time.Duration) MuxerOption {
	return func(m *muxer) { m.maxTS = d }
}

// WithBlockingTimeout overrides the default timeout (3s) for blocking HLS requests.
func WithBlockingTimeout(d time.Duration) MuxerOption {
	return func(m *muxer) { m.blockingTimeout = d }
}

// WithVersion overrides the default HLS protocol version (7).
func WithVersion(v int) MuxerOption {
	return func(m *muxer) { m.version = v }
}

// WithKeyframeSplit controls segment rotation strategy.
// When false (default) segments rotate strictly on target duration, which may
// split mid-GOP. When true, rotation is deferred to the next video keyframe
// after the target duration is reached, guaranteeing every segment starts with
// an independently decodable frame at the cost of variable segment lengths.
// This guarantee covers the stream start as well: packets arriving before the
// first video keyframe are dropped, so segment 0 never opens mid-GOP.
// Segment length is still bounded by the hard cap (WithMaxSegmentDuration):
// a source with a pathological GOP (e.g. smart codecs emitting an IDR every
// 30s on a static scene) is cut mid-GOP at the cap rather than allowed to
// produce arbitrarily long segments and unbounded muxer memory.
func WithKeyframeSplit(enabled bool) MuxerOption {
	return func(m *muxer) { m.keyframeSplit = enabled }
}

// WithMaxSegmentDuration sets a hard upper bound for segment length under
// keyframeSplit. By default there is no cap: rotation waits for a keyframe
// however long the GOP is. With a cap set, rotation still prefers the first
// keyframe after the target duration, but if none arrives before the cap the
// segment is force-cut mid-GOP, bounding segment length (and muxer memory)
// against sources with pathological GOPs. Continuous playback is unaffected
// (fMP4 is a continuous timeline); only a player that starts inside such a
// segment must wait for the next IDR. The cap also becomes the advertised
// EXT-X-TARGETDURATION. Values below the target segment duration are clamped
// to it.
func WithMaxSegmentDuration(d time.Duration) MuxerOption {
	return func(m *muxer) { m.maxSegmentDuration = d }
}

// WithMinPlaylistDuration switches segment eviction from count-based to
// duration-based: the oldest closed segment is evicted only while the
// remaining closed segments still sum to at least d. Segment count becomes
// irrelevant — a playlist of many short segments and one of few long segments
// both retain the same number of playable seconds, which also bounds muxer
// memory at roughly d plus two max-length segments. When unset (0), the
// legacy count-based eviction (segmentCount) applies.
func WithMinPlaylistDuration(d time.Duration) MuxerOption {
	return func(m *muxer) { m.minPlaylistDuration = d }
}

// muxer is an implementation of the HLS interface.
type muxer struct {
	lifecycle.Manager[*muxer] // Embedding lifecycle.Manager to manage lifecycle functions.
	log                       logger.Logger
	*segments
	segmentCount          uint8                               // Number of segments to keep in the playlist.
	mediaSequence         int64                               // Media sequence number.
	discontinuitySequence int64                               // Discontinuity sequence number (incremented when a discontinuity segment is evicted).
	segmentDuration       time.Duration                       // Target duration for each segment.
	fragmentDuration      time.Duration                       // Target duration for each fragment.
	maxTS                 time.Duration                       // Max timestamp before wrap-around.
	blockingTimeout       time.Duration                       // Timeout for blocking HLS requests.
	version               int                                 // HLS protocol version.
	manifest              atomic.Value                        // Stores the HLS manifest (string).
	indexMu               sync.Mutex                          // Protects indexCh replacement.
	indexCh               chan struct{}                       // Closed to broadcast manifest changes; then replaced.
	header                string                              // Initial part of the HLS playlist.
	codecPars             gomedia.CodecParametersPair         // Codec parameters for video and audio.
	initVersion           int                                 // Current init segment version (incremented on codec change).
	initCache             map[int]gomedia.CodecParametersPair // Cached codec params per init version.
	initBytesCache        map[int][]byte                      // Cached generated init segment bytes per version.
	initMu                sync.RWMutex                        // Protects codecPars, initVersion, initCache, initBytesCache.
	mediaName             string                              // Base filename used in segment/fragment URIs (e.g. "media").
	manifestBuilder       strings.Builder                     // Reusable builder for manifest generation.
	manifestDirty         bool                                // True when manifest needs rebuild.
	keyframeSplit         bool                                // When true, defer segment rotation to the next video keyframe.
	minPlaylistDuration   time.Duration                       // Duration-based eviction bound (0 → count-based eviction).
	maxSegmentDuration    time.Duration                       // Hard cap for segment length under keyframeSplit (0 → 2x segmentDuration).
	partHoldBack          float64                             // PART-HOLD-BACK advertised in the header; kept for header rebuilds.
	capSplitSeen          bool                                // True once any segment was force-cut mid-GOP at the cap.
	tsOffset              time.Duration                       // Timeline epoch: subtracted from incoming timestamps, advanced on wrap.
	gateOpen              bool                                // False until the first video keyframe arrives; packets are dropped meanwhile.
}

// NewHLSMuxer creates a new HLS muxer with the specified segment duration and segment count.
func NewHLSMuxer(segmentDuration time.Duration, segmentCount uint8, partHoldBack float64, log logger.Logger, opts ...MuxerOption) gomedia.HLSMuxer {
	newHLS := &muxer{
		Manager: nil,
		log:     log,
		segments: &segments{
			segments: make(map[uint64]*segment),
			segIDs:   []uint64{},
			RWMutex:  sync.RWMutex{},
		},
		segmentCount:     segmentCount,
		mediaSequence:    0,
		segmentDuration:  segmentDuration,
		fragmentDuration: fragmentDuration,
		maxTS:            maxTS,
		blockingTimeout:  time.Second * 3,
		version:          7,
		manifest:         atomic.Value{}, //nolint:govet // initialized with Store("") below
		indexCh:          make(chan struct{}),
		codecPars:        gomedia.CodecParametersPair{AudioCodecParameters: nil, VideoCodecParameters: nil, SourceID: ""},
		initVersion:      0,
		initCache:        make(map[int]gomedia.CodecParametersPair),
		initBytesCache:   make(map[int][]byte),
		mediaName:        "media",
	}
	newHLS.manifest.Store("")
	newHLS.partHoldBack = partHoldBack
	for _, o := range opts {
		o(newHLS)
	}
	if newHLS.maxSegmentDuration != 0 && newHLS.maxSegmentDuration < segmentDuration {
		newHLS.maxSegmentDuration = segmentDuration
	}
	newHLS.header = newHLS.buildHeader()
	newHLS.Manager = lifecycle.NewDefaultManager(newHLS, log)
	return newHLS
}

// buildHeader renders the static playlist header. Rebuilt when honesty of the
// advertised tags changes (first mid-GOP force-cut drops INDEPENDENT-SEGMENTS).
func (mxr *muxer) buildHeader() string {
	// RFC 8216 §4.3.3.1: TARGETDURATION is an upper bound for every segment.
	// When a hard cap is configured it is the honest bound to advertise;
	// otherwise the target duration is used.
	targetDuration := mxr.segmentDuration
	if mxr.maxSegmentDuration > 0 {
		targetDuration = mxr.maxSegmentDuration
	}
	partTarget := mxr.fragmentDuration.Seconds() * 1.01
	// EXT-X-INDEPENDENT-SEGMENTS must hold for every segment; after the first
	// forced mid-GOP cut it would be a lie, so it is dropped permanently.
	independentTag := ""
	if mxr.keyframeSplit && !mxr.capSplitSeen {
		independentTag = "#EXT-X-INDEPENDENT-SEGMENTS\n"
	}
	return fmt.Sprintf(`#EXTM3U
#EXT-X-VERSION:%d
#EXT-X-TARGETDURATION:%d
#EXT-X-SERVER-CONTROL:CAN-BLOCK-RELOAD=YES,PART-HOLD-BACK=%.5f
#EXT-X-PART-INF:PART-TARGET=%.5f
%s`, mxr.version, int(math.Ceil(targetDuration.Seconds())), mxr.partHoldBack, partTarget, independentTag)
}

// Mux initializes the HLS muxer with codec parameters.
func (mxr *muxer) Mux(codecPars gomedia.CodecParametersPair) (err error) {
	startFunc := func(mxr *muxer) error {
		if codecPars.VideoCodecParameters == nil && codecPars.AudioCodecParameters == nil {
			return &utils.NoCodecDataError{}
		}

		mux := fmp4.NewMuxer(mxr.log)
		if err = mux.Mux(codecPars); err != nil {
			return err
		}

		mxr.codecPars = codecPars
		mxr.initVersion = 0
		mxr.initCache[0] = codecPars
		newSeg := newSegment(0, mxr.fragmentDuration, mxr.segmentDuration, codecPars, mxr.mediaName, mxr.blockingTimeout, mxr.log)
		newSeg.initVersion = mxr.initVersion
		mxr.addSegment(newSeg)
		mxr.manifestDirty = true

		return nil
	}
	return mxr.Manager.Start(startFunc)
}

// UpdateCodecParameters updates codec parameters mid-stream, inserting an HLS discontinuity.
// The current segment is force-closed and a new segment with a discontinuity tag is created,
// signalling the player to re-fetch the init segment and reinitialize its decoder.
func (mxr *muxer) UpdateCodecParameters(codecPars gomedia.CodecParametersPair) error {
	if codecPars.VideoCodecParameters == nil && codecPars.AudioCodecParameters == nil {
		return &utils.NoCodecDataError{}
	}

	curSeg := mxr.getCurSegment()

	// Close the current fragment so pending waiters are unblocked.
	curFrag := curSeg.curFragment
	select {
	case <-curFrag.finished:
	default:
		curFrag.duration = 0 // mark as empty so manifest is clean
		close(curFrag.finished)
	}

	// Force-close the current segment (may be shorter than target duration).
	select {
	case <-curSeg.finished:
	default:
		if curSeg.duration > 0 {
			curSeg.manifestEntry = fmt.Sprintf("%s#EXT-X-PROGRAM-DATE-TIME:%s\n#EXTINF:%.5f\nsegment/%d/%s.m4s\n",
				curSeg.cacheEntry, curSeg.time.Format("2006-01-02T15:04:05.000000Z"), curSeg.duration.Seconds(), curSeg.id, curSeg.mediaName)
		}
		close(curSeg.finished)
	}

	mxr.initMu.Lock()
	mxr.codecPars = codecPars
	mxr.initVersion++
	mxr.initCache[mxr.initVersion] = codecPars
	initVersion := mxr.initVersion
	mxr.initMu.Unlock()

	newSegID := curSeg.id + 1
	newSeg := newSegment(newSegID, mxr.fragmentDuration, mxr.segmentDuration, codecPars, mxr.mediaName, mxr.blockingTimeout, mxr.log)
	newSeg.discontinuity = true
	newSeg.initVersion = initVersion
	mxr.addSegment(newSeg)

	// The post-discontinuity segment must also open on a keyframe: the player
	// reinitializes its decoder here, so a mid-GOP start would hang it again.
	mxr.gateOpen = false

	mxr.evictOldSegments()

	mxr.manifest.Store(mxr.updateIndexM3u8())
	mxr.broadcastIndex()
	return nil
}

// evictOldSegments removes the oldest segments according to the configured
// strategy: duration-based when minPlaylistDuration is set, count-based
// otherwise.
func (mxr *muxer) evictOldSegments() {
	if mxr.minPlaylistDuration > 0 {
		mxr.evictByDuration()
	} else {
		mxr.evictByCount()
	}
	mxr.cleanupInitCache()
}

// evictByDuration drops the oldest closed segment while the remaining closed
// segments still sum to at least minPlaylistDuration. The in-progress segment
// is not counted — the bound is on seconds a client can actually play. Keeps
// at least one closed segment plus the current one.
func (mxr *muxer) evictByDuration() {
	const minSegments = 2 // one closed + the in-progress segment
	for len(mxr.segIDs) > minSegments {
		var closedDuration time.Duration
		for _, id := range mxr.segIDs[:len(mxr.segIDs)-1] {
			if seg, ok := mxr.getSegment(id); ok {
				closedDuration += seg.duration
			}
		}
		oldest, ok := mxr.getSegment(mxr.segIDs[0])
		if !ok {
			break
		}
		if closedDuration-oldest.duration < mxr.minPlaylistDuration {
			break
		}
		mxr.evictOldest()
	}
}

// evictByCount removes the oldest segments when both conditions are met:
//  1. segment count exceeds segmentCount (minimum number of segments to keep)
//  2. total duration of all segments exceeds segmentCount * segmentDuration
func (mxr *muxer) evictByCount() {
	maxDuration := time.Duration(mxr.segmentCount) * mxr.segmentDuration
	// RFC 8216: playlist duration MUST NOT fall below 3 * targetDuration.
	minDuration := 3 * mxr.segmentDuration
	if maxDuration < minDuration {
		maxDuration = minDuration
	}
	for len(mxr.segIDs) > int(mxr.segmentCount) {
		var totalDuration time.Duration
		for _, id := range mxr.segIDs {
			if seg, ok := mxr.getSegment(id); ok {
				totalDuration += seg.duration
			}
		}
		if totalDuration <= maxDuration {
			break
		}
		mxr.evictOldest()
	}
}

// evictOldest removes the head segment, advancing the media and discontinuity
// sequence counters accordingly.
func (mxr *muxer) evictOldest() {
	oldestID := mxr.segIDs[0]
	if seg, ok := mxr.getSegment(oldestID); ok && seg.discontinuity {
		mxr.discontinuitySequence++
	}
	mxr.removeSegment(oldestID)
	mxr.mediaSequence++
}

// cleanupInitCache removes init versions that are no longer referenced by any segment.
func (mxr *muxer) cleanupInitCache() {
	used := make(map[int]bool)
	for _, id := range mxr.segIDs {
		if seg, ok := mxr.getSegment(id); ok {
			used[seg.initVersion] = true
		}
	}
	mxr.initMu.Lock()
	defer mxr.initMu.Unlock()
	for v := range mxr.initCache {
		if !used[v] {
			delete(mxr.initCache, v)
			delete(mxr.initBytesCache, v)
		}
	}
}

// canOpenTimelineEpoch reports whether pkt may start a new timeline epoch:
// a video keyframe, or any packet when the stream has no video track. Both the
// stream start gate and the wrap point are aligned to such packets so every
// advertised segment begins with an independently decodable frame.
func (mxr *muxer) canOpenTimelineEpoch(pkt gomedia.Packet) bool {
	if mxr.codecPars.VideoCodecParameters == nil {
		return true
	}
	vPkt, isVideo := pkt.(gomedia.VideoPacket)
	return isVideo && vPkt.IsKeyFrame()
}

// startDiscontinuitySegment force-closes the current segment and opens a new
// one flagged with #EXT-X-DISCONTINUITY, telling players the media timeline
// restarts rather than letting them observe tfdt jumping backwards.
func (mxr *muxer) startDiscontinuitySegment() {
	curSeg := mxr.getCurSegment()
	curSeg.closeSeg()
	newSeg := newSegment(curSeg.id+1, mxr.fragmentDuration, mxr.segmentDuration, mxr.codecPars, mxr.mediaName, mxr.blockingTimeout, mxr.log)
	newSeg.discontinuity = true
	newSeg.initVersion = mxr.initVersion
	mxr.addSegment(newSeg)
	mxr.evictOldSegments()
	mxr.manifestDirty = true
}

// WritePacket writes a multimedia packet to the current fragment of the current segment.
func (mxr *muxer) WritePacket(inpPkt gomedia.Packet) (err error) {
	if inpPkt == nil {
		return &utils.NilPacketError{}
	}

	// With keyframeSplit every segment must start with an independently
	// decodable frame — including the very first one. A segment opening on a
	// P-frame cannot be decoded until the next IDR, so the player hangs for up
	// to a GOP and then plays that far behind live. Drop everything until the
	// first video keyframe (mirrors the WebRTC writer's GOP buffer). The
	// caller keeps ownership of dropped packets and releases them.
	if mxr.keyframeSplit && !mxr.gateOpen {
		if !mxr.canOpenTimelineEpoch(inpPkt) {
			return nil
		}
		mxr.gateOpen = true
	}

	// Bound the relative timeline: players (hls.js/MSE) degrade on very large
	// baseMediaDecodeTime values, so timestamps must not grow without limit.
	// Unlike a silent modulo, the epoch reset happens only on a packet that may
	// start a segment and is announced via #EXT-X-DISCONTINUITY, so players
	// reinitialize instead of stalling on a backwards tfdt jump. The overshoot
	// past maxTS while waiting for a keyframe is harmless (tfdt is uint64).
	ts := inpPkt.Timestamp() - mxr.tsOffset
	if ts >= mxr.maxTS && mxr.canOpenTimelineEpoch(inpPkt) {
		mxr.tsOffset += ts
		ts = 0
		mxr.startDiscontinuitySegment()
	}
	if ts < 0 {
		// An audio packet interleaved slightly behind the wrap keyframe would
		// otherwise carry a negative timestamp into the fMP4 muxer.
		ts = 0
	}
	inpPkt.SetTimestamp(ts)

	// Rotate segment according to the configured strategy.
	//   - keyframeSplit=true: wait for the next video keyframe once the
	//     target duration is met. Segments may exceed target but always
	//     start with an independently decodable frame — unless the source
	//     withholds keyframes past the hard cap (maxSegmentDuration), in
	//     which case the segment is force-cut mid-GOP to keep segment
	//     length (and therefore muxer memory) bounded.
	//   - keyframeSplit=false (default): strict time-based rotation. Cut
	//     before any video packet that would push the segment past the
	//     target duration, so EXT-X-TARGETDURATION remains an honest
	//     upper bound (RFC 8216 §4.3.3.1).
	if vPkt, isVideo := inpPkt.(gomedia.VideoPacket); isVideo {
		curSeg := mxr.getCurSegment()
		var rotate bool
		projected := curSeg.duration + curSeg.curFragment.duration + inpPkt.Duration()
		hasContent := curSeg.duration > 0 || curSeg.curFragment.duration > 0
		if mxr.keyframeSplit {
			rotate = vPkt.IsKeyFrame() && curSeg.duration >= mxr.segmentDuration
			if !rotate && mxr.maxSegmentDuration > 0 && hasContent && projected > mxr.maxSegmentDuration {
				rotate = true
				if !mxr.capSplitSeen {
					mxr.capSplitSeen = true
					mxr.header = mxr.buildHeader()
					mxr.log.Infof(mxr, "segment force-cut mid-GOP at %v cap (no keyframe since target); dropping INDEPENDENT-SEGMENTS tag", mxr.maxSegmentDuration)
				}
			}
		} else {
			rotate = hasContent && projected > mxr.segmentDuration
		}
		if rotate {
			curSeg.closeSeg()
			newSegID := curSeg.id + 1
			newSeg := newSegment(newSegID, mxr.fragmentDuration, mxr.segmentDuration, mxr.codecPars, mxr.mediaName, mxr.blockingTimeout, mxr.log)
			newSeg.initVersion = mxr.initVersion
			mxr.addSegment(newSeg)
			mxr.evictOldSegments()
			mxr.manifestDirty = true
		}
	}

	changed, writeErr := mxr.getCurSegment().writePacket(inpPkt)
	if writeErr != nil {
		return writeErr
	}

	// Audio packets cannot close fragments or segments — only video packets
	// advance fragment duration. The manifest is unchanged after an audio write,
	// so skip the expensive rebuild and indexChan signal to avoid wasting CPU
	// and falsely unblocking LL-HLS long-poll clients.
	if _, isVideo := inpPkt.(gomedia.VideoPacket); !isVideo {
		return nil
	}

	if changed {
		mxr.manifestDirty = true
	}

	// Rebuild manifest only when something actually changed (fragment/segment boundary).
	if !mxr.manifestDirty {
		return nil
	}
	mxr.manifestDirty = false

	// Update the HLS manifest and broadcast the change to all waiting readers.
	mxr.manifest.Store(mxr.updateIndexM3u8())
	mxr.broadcastIndex()
	return nil
}

// broadcastIndex wakes every blocked GetIndexM3u8 caller by closing indexCh,
// then swaps in a fresh channel for the next wait cycle.
func (mxr *muxer) broadcastIndex() {
	mxr.indexMu.Lock()
	ch := mxr.indexCh
	mxr.indexCh = make(chan struct{})
	mxr.indexMu.Unlock()
	close(ch)
}

// getIndexCh returns the current broadcast channel. Callers MUST capture it
// before entering their select, otherwise a broadcast racing the wait is lost.
func (mxr *muxer) getIndexCh() chan struct{} {
	mxr.indexMu.Lock()
	defer mxr.indexMu.Unlock()
	return mxr.indexCh
}

// updateIndexM3u8 rebuilds the HLS manifest into the reusable manifestBuilder.
func (mxr *muxer) updateIndexM3u8() string {
	mxr.manifestBuilder.Reset()
	b := &mxr.manifestBuilder

	b.WriteString(mxr.header)
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:")
	b.WriteString(strconv.FormatInt(mxr.mediaSequence, 10))
	b.WriteByte('\n')

	if mxr.discontinuitySequence > 0 {
		b.WriteString("#EXT-X-DISCONTINUITY-SEQUENCE:")
		b.WriteString(strconv.FormatInt(mxr.discontinuitySequence, 10))
		b.WriteByte('\n')
	}

	curInitVersion := -1
	for _, id := range mxr.segIDs {
		seg, ok := mxr.getSegment(id)
		if !ok {
			mxr.log.Errorf(mxr, "Segment %d not found in map", id)
			continue
		}

		if seg.manifestEntry == "" {
			mxr.log.Errorf(mxr, "Manifest entry for segment %d is nil", id)
			continue
		}

		if seg.discontinuity {
			b.WriteString("#EXT-X-DISCONTINUITY\n")
		}

		// Emit #EXT-X-MAP when init version changes (including the first segment).
		if seg.initVersion != curInitVersion {
			curInitVersion = seg.initVersion
			b.WriteString("#EXT-X-MAP:URI=\"init.mp4?v=")
			b.WriteString(strconv.Itoa(curInitVersion))
			b.WriteString("\"\n")
		}

		b.WriteString(seg.manifestEntry)
	}

	return b.String()
}

// GetIndexM3u8 returns the HLS manifest based on the requested segment and part.
func (mxr *muxer) GetIndexM3u8(ctx context.Context, needSeg int64, needPart int8) (string, error) {
	if needSeg < 0 {
		return mxr.manifest.Load().(string), nil
	}

	// The whole blocking reload — waiting for the segment/part AND for the
	// manifest rebuild — must be bounded. A caller whose ctx never fires (an
	// unbounded request context) would otherwise wait here forever once the
	// source stops producing packets, since the segment it waits on can only be
	// finished by an incoming packet. Callers hold locks across this call, so
	// an unbounded wait deadlocks the writer rather than just the request.
	ctx, cancel := context.WithTimeout(ctx, mxr.blockingTimeout)
	defer cancel()

	// Capture the broadcast channel BEFORE waiting, so if the manifest is
	// updated while we wait for the segment/part, the channel will already
	// be closed and we won't block below.
	ch := mxr.getIndexCh()

	// Wait for segment or part
	waitErr := mxr.waitForSegmentOrPart(ctx, needSeg, needPart)
	if waitErr != nil {
		return "", waitErr
	}

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-ch:
	}

	return mxr.manifest.Load().(string), nil
}

// waitForSegmentOrPart blocks until segment needSeg (and optionally fragment
// needPart, when >= 0) exists in the playlist. Factored out of GetIndexM3u8 to
// keep that function's cyclomatic complexity in check.
func (mxr *muxer) waitForSegmentOrPart(ctx context.Context, needSeg int64, needPart int8) error {
	if needSeg < 0 {
		mxr.log.Errorf(mxr, "Segment index %d is negative", needSeg)
		return errors.New("segment index cannot be negative")
	}

	needSegUint64 := uint64(needSeg)
	shouldWaitForPart := needPart >= 0

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		curSeg := mxr.getCurSegment()

		if curSeg.id > needSegUint64 {
			return nil
		}
		if curSeg.id == needSegUint64 && !shouldWaitForPart {
			return nil
		}
		if curSeg.id == needSegUint64 && shouldWaitForPart {
			curSeg.waitFragment(ctx, uint8(needPart))
			return ctx.Err()
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-curSeg.finished:
		}
	}
}

func (mxr *muxer) GetInit() ([]byte, error) {
	mxr.initMu.RLock()
	version := mxr.initVersion
	mxr.initMu.RUnlock()
	return mxr.GetInitByVersion(version)
}

// GetInitByVersion returns the init segment for a given codec version, caching
// the bytes so subsequent requests skip the re-mux.
func (mxr *muxer) GetInitByVersion(version int) ([]byte, error) {
	mxr.initMu.RLock()
	if cached, ok := mxr.initBytesCache[version]; ok {
		mxr.initMu.RUnlock()
		return cached, nil
	}
	codecPars, ok := mxr.initCache[version]
	mxr.initMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("init version %d not found", version)
	}
	mux := fmp4.NewMuxer(mxr.log)
	if err := mux.Mux(codecPars); err != nil {
		return nil, err
	}
	buf := mux.GetInit()
	if len(buf.Data()) == 0 {
		return nil, errors.New("empty init buffer")
	}
	data := make([]byte, len(buf.Data()))
	copy(data, buf.Data())
	mxr.initMu.Lock()
	mxr.initBytesCache[version] = data
	mxr.initMu.Unlock()
	return data, nil
}

// GetSegment blocks until segment index is finished, then returns its MP4 bytes.
// The MP4 is lazily generated on first call and cached.
func (mxr *muxer) GetSegment(ctx context.Context, index uint64) ([]byte, error) {
	seg, ok := mxr.getSegment(index)
	if !ok {
		return nil, errors.New("segment not found")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-seg.finished:
	}
	buf := seg.getMp4Buffer()
	if buf == nil {
		return nil, errors.New("segment expired")
	}
	return buf.Data(), nil
}

// GetFragment blocks until the fragment closes, then returns its MP4 bytes
// (LL-HLS part). Lazily generated and cached on first call.
func (mxr *muxer) GetFragment(ctx context.Context, segindex uint64, index uint8) ([]byte, error) {
	seg, ok := mxr.getSegment(segindex)
	if !ok {
		return nil, errors.New("segment not found")
	}

	seg.waitFragment(ctx, index)

	buf := seg.getFragment(ctx, index)
	if buf == nil {
		return nil, errors.New("fragment expired")
	}
	bytes := buf.Data()

	if len(bytes) == 0 {
		return nil, errors.New("fragment not found")
	}
	return bytes, nil
}

// Release drops every segment and frees their retained ring-buffer slots.
func (mxr *muxer) Release() { //nolint:revive // Method name required by interface
	mxr.segments.Lock()
	segs := make([]*segment, 0, len(mxr.segments.segments))
	for _, seg := range mxr.segments.segments {
		segs = append(segs, seg)
	}
	mxr.segments.segments = make(map[uint64]*segment)
	mxr.segments.segIDs = nil
	mxr.segments.Unlock()

	for _, seg := range segs {
		seg.release()
	}
}

// GetMasterEntry builds this stream's #EXT-X-STREAM-INF line for the master playlist.
func (mxr *muxer) GetMasterEntry() (string, error) {
	mxr.initMu.RLock()
	codecPars := mxr.codecPars
	mxr.initMu.RUnlock()
	if codecPars.VideoCodecParameters == nil {
		return "", errors.New("no video codec")
	}
	w, h := codecPars.VideoCodecParameters.Width(), codecPars.VideoCodecParameters.Height()
	codecsStr := codecPars.VideoCodecParameters.Tag()
	if codecPars.AudioCodecParameters != nil {
		codecsStr += fmt.Sprintf(",%s", codecPars.AudioCodecParameters.Tag())
	}

	masterEntry := fmt.Sprintf(
		"#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d,CODECS=\"%s\",FRAME-RATE=%.3f",
		codecPars.VideoCodecParameters.Bitrate(),
		w,
		h,
		codecsStr,
		float64(codecPars.VideoCodecParameters.FPS()),
	)
	return masterEntry, nil
}

func (mxr *muxer) String() string {
	return "HLS_MUXER"
}
