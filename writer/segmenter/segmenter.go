package segmenter

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/format/mp4"
	"github.com/ugparu/gomedia/utils"
	"github.com/ugparu/gomedia/utils/buffer"
	"github.com/ugparu/gomedia/utils/lifecycle"
	"github.com/ugparu/gomedia/utils/logger"
)

// activeFile tracks the segment currently being written. The MP4 muxer
// buffers packets; Flush/WriteTrailer is what actually hits the disk.
type activeFile struct {
	file      *os.File
	muxer     *mp4.Muxer
	startTime time.Time
	folder    string
	name      string
	duration  time.Duration
	lastFlush time.Duration
	pktCount  int
}

// ringBuffer holds a rolling window of packets preceding an event trigger
// so Event-mode recordings can start a few seconds before the trigger.
// hardCap bounds total duration when trim() cannot fire because no keyframe
// is present in the tail. On overflow we keep the leading keyframe and
// shift the tail over packet[1], preserving approximate coherence for
// mostly-static scenes at the cost of a dropped middle.
type ringBuffer struct {
	packets    []gomedia.Packet
	duration   time.Duration
	bytes      int
	maxDur     time.Duration
	hardCap    time.Duration
	maxPackets int
	maxBytes   int
}

// ringPacketsPerSecond is a conservative headroom estimate for interleaved
// audio+video when enforcing a packet-count cap on the idle pre-buffer.
const ringPacketsPerSecond = 60

func newRingBuffer(maxDur, hardCap time.Duration, maxBytes int) *ringBuffer {
	return &ringBuffer{
		packets:    make([]gomedia.Packet, 0),
		duration:   0,
		maxDur:     maxDur,
		hardCap:    hardCap,
		maxBytes:   maxBytes,
		maxPackets: ringMaxPackets(maxBytes, hardCap),
	}
}

// ringByteBudgets splits RingBudgetBytes(target, preBuffer) between the idle
// pre-buffer and the in-flight muxer window (reference: 10s target + 5s pre-buffer).
func ringByteBudgets(target, preBuffer time.Duration) (ringBufBytes, muxerBytes int) {
	total := buffer.RingBudgetBytes(target, preBuffer)
	window := target.Seconds() + preBuffer.Seconds()
	if window <= 0 {
		return total / 3, total - total/3 //nolint:mnd
	}
	ringBufBytes = int(float64(total) * preBuffer.Seconds() / window)
	if ringBufBytes < 256*1024 { //nolint:mnd
		ringBufBytes = 256 * 1024 //nolint:mnd
	}
	if ringBufBytes > total {
		ringBufBytes = total
	}
	return ringBufBytes, total - ringBufBytes
}

func ringMaxPackets(maxBytes int, hardCap time.Duration) int {
	byBytes := maxBytes / 4096 //nolint:mnd
	byDuration := int(hardCap.Seconds()) * ringPacketsPerSecond
	n := byBytes
	if byDuration < n {
		n = byDuration
	}
	if n < 128 { //nolint:mnd
		return 128
	}
	return n
}

// ringHardCap bounds the idle pre-buffer when trim() cannot find a keyframe.
// It must track preBufferDuration only — tying it to targetDuration (segment
// size for Always mode) can retain minutes of packets and exhaust the ring allocator.
func ringHardCap(preBuffer time.Duration) time.Duration {
	return preBuffer * 2
}

func (rb *ringBuffer) dropPacketAt(idx int) {
	dropped := rb.packets[idx]
	rb.bytes -= dropped.Len()
	if rb.bytes < 0 {
		rb.bytes = 0
	}
	if _, ok := dropped.(gomedia.VideoPacket); ok {
		rb.duration -= dropped.Duration()
		if rb.duration < 0 {
			rb.duration = 0
		}
	}
	dropped.Release()
	copy(rb.packets[idx:], rb.packets[idx+1:])
	rb.packets = rb.packets[:len(rb.packets)-1]
}

func (rb *ringBuffer) enforceLimits() {
	for rb.bytes > rb.maxBytes && len(rb.packets) > 1 {
		rb.dropPacketAt(1)
	}
	// Duration-based cap (video timing). Audio is excluded from duration but
	// still holds ring-allocator slots, so also enforce a packet-count cap.
	for rb.duration > rb.hardCap && len(rb.packets) > 1 {
		rb.dropPacketAt(1)
	}
	for len(rb.packets) > rb.maxPackets && len(rb.packets) > 1 {
		rb.dropPacketAt(1)
	}
}

func (rb *ringBuffer) add(pkt gomedia.Packet) {
	rb.packets = append(rb.packets, pkt)
	rb.bytes += pkt.Len()
	// Only video packets have meaningful durations; audio packet timing is
	// per-chunk and would distort the rolling window bound.
	if _, ok := pkt.(gomedia.VideoPacket); ok {
		rb.duration += pkt.Duration()
	}
	// Overflow fallback: shift the tail down over packet[1] so the leading
	// keyframe survives even when no later keyframe is in sight for trim().
	rb.enforceLimits()
}

// trim drops everything before the next keyframe whenever the window
// exceeds maxDur. Stopping at a keyframe keeps the buffer decodable so
// an event recording can start instantly from whatever remains.
func (rb *ringBuffer) trim() {
	for len(rb.packets) > 1 && rb.duration > rb.maxDur {
		trimIdx := -1
		for i := range rb.packets {
			if i == 0 {
				continue
			}
			if vPkt, ok := rb.packets[i].(gomedia.VideoPacket); ok && vPkt.IsKeyFrame() {
				trimIdx = i
				break
			}
		}
		if trimIdx == -1 {
			break
		}
		var trimDur time.Duration
		for i := range trimIdx {
			if _, ok := rb.packets[i].(gomedia.VideoPacket); ok {
				trimDur += rb.packets[i].Duration()
			}
		}
		for i := 0; i < trimIdx; i++ {
			rb.bytes -= rb.packets[i].Len()
			rb.packets[i].Release()
		}
		rb.packets = rb.packets[trimIdx:]
		rb.duration -= trimDur
	}
	rb.enforceLimits()
}

func (rb *ringBuffer) clear() {
	for _, pkt := range rb.packets {
		pkt.Release()
	}
	rb.packets = rb.packets[:0]
	rb.duration = 0
	rb.bytes = 0
}

func (rb *ringBuffer) drain() []gomedia.Packet {
	pkts := rb.packets
	rb.packets = rb.packets[:0]
	rb.duration = 0
	rb.bytes = 0
	return pkts
}

type streamState struct {
	activeFile       *activeFile
	ringBuf          *ringBuffer
	seenKeyframe     bool
	eventSaved       bool
	eventRecordStart time.Time
	// eventSessionDur accumulates video duration across every segment of the
	// current event session (it survives targetDuration rotations, unlike
	// activeFile.duration). It bounds a session against maxEventDuration.
	eventSessionDur time.Duration
	// lastRecordStop is the media stop time of the previous event recording.
	// A follow-up event arriving within preBufferDuration resumes from here so
	// footage already written is not duplicated.
	lastRecordStop time.Time
	codecPar       gomedia.CodecParametersPair
}

type Option func(*segmenter)

// PathFunc generates the subdirectory and filename for a new segment.
type PathFunc func(startTime time.Time, streamIdx int) (dir, filename string)

func WithLogger(l logger.Logger) Option {
	return func(s *segmenter) { s.log = l }
}

// WithPathFunc overrides the default segment path generation logic.
func WithPathFunc(f PathFunc) Option {
	return func(s *segmenter) { s.pathFunc = f }
}

// WithPreBufferDuration overrides the default event mode pre-buffer duration (targetDuration / 2).
// This controls how much video before the event trigger is included in the recording.
func WithPreBufferDuration(d time.Duration) Option {
	return func(s *segmenter) { s.preBufferDuration = d }
}

// WithPostEventDuration overrides the default event mode post-event duration (maxEventDuration in Event mode).
// This controls how long the recording keeps running after the last event trigger before it is closed.
func WithPostEventDuration(d time.Duration) Option {
	return func(s *segmenter) { s.postEventDuration = d }
}

// WithMaxEventDuration overrides the default max event recording duration (1 minute).
func WithMaxEventDuration(d time.Duration) Option {
	return func(s *segmenter) { s.maxEventDuration = d }
}

// WithDirPermissions overrides the default directory permissions (ModePerm).
func WithDirPermissions(perm os.FileMode) Option {
	return func(s *segmenter) { s.dirPerm = perm }
}

// WithBatchedDump collapses each MP4 flush into a single write by staging
// pending packets in memory first — trades a bit of RAM for one syscall per
// flush. The default (unbatched) writes directly from packet buffers.
func WithBatchedDump() Option {
	return func(s *segmenter) { s.batchedDump = true }
}

type segmenter struct {
	lifecycle.AsyncManager[*segmenter]
	log               logger.Logger
	recordMode        gomedia.RecordMode
	targetDuration    time.Duration
	recordModeCh      chan gomedia.RecordMode
	eventCh           chan struct{}
	inpPktCh          chan gomedia.Packet
	outInfoCh         chan gomedia.FileInfo
	recordCurStatusCh chan bool
	recordCurStatus   bool
	lastEvent         time.Time
	dest              string
	rmSrcCh           chan string
	addSrcCh          chan string

	pathFunc          PathFunc
	preBufferDuration time.Duration
	postEventDuration time.Duration
	maxEventDuration  time.Duration
	dirPerm           os.FileMode
	batchedDump       bool
	ringBufferByteCap int
	muxerFlushByteCap int

	sources   []string
	streams   map[string]*streamState
	streamsMu sync.RWMutex
}

func (s *segmenter) hasSource(url string) bool {
	return slices.Contains(s.sources, url)
}

func (s *segmenter) getSourceIndex(url string) int {
	for i, src := range s.sources {
		if src == url {
			return i
		}
	}
	return -1
}

// addSource registers a new source; the underlying streamState and file are
// only materialized once the first packet arrives and codec parameters are known.
func (s *segmenter) addSource(url string) {
	if s.hasSource(url) {
		s.log.Infof(s, "Source %s already exists, skipping", url)
		return
	}

	s.sources = append(s.sources, url)
	s.log.Infof(s, "Added new source %s", url)
}

// removeSource returns any FileInfo that closeSegment produced; the caller
// must forward it on outInfoCh outside of streamsMu to avoid holding the
// lock across a potentially blocking channel send.
func (s *segmenter) removeSource(url string) *gomedia.FileInfo {
	var info *gomedia.FileInfo
	if stream, exists := s.streams[url]; exists {
		if stream.activeFile != nil {
			info, _ = s.closeSegment(stream, 0)
		}
		stream.ringBuf.clear()
		delete(s.streams, url)
	}

	for i, src := range s.sources {
		if src == url {
			s.sources = append(s.sources[:i], s.sources[i+1:]...)
			s.log.Infof(s, "Removed source %s", url)
			break
		}
	}
	return info
}

func New(dest string, segSize time.Duration, recordMode gomedia.RecordMode, chanSize int, opts ...Option) gomedia.Segmenter {
	newArch := &segmenter{
		AsyncManager:      nil,
		log:               logger.Default,
		recordMode:        recordMode,
		targetDuration:    segSize,
		recordModeCh:      make(chan gomedia.RecordMode, chanSize),
		eventCh:           make(chan struct{}, chanSize),
		inpPktCh:          make(chan gomedia.Packet, chanSize),
		outInfoCh:         make(chan gomedia.FileInfo, chanSize),
		recordCurStatusCh: make(chan bool, chanSize),
		recordCurStatus:   false,
		lastEvent:         time.Now(),
		dest:              dest,
		rmSrcCh:           make(chan string, chanSize),
		addSrcCh:          make(chan string, chanSize),
		sources:           []string{},
		streams:           make(map[string]*streamState),
		streamsMu:         sync.RWMutex{},
		pathFunc: func(startTime time.Time, streamIdx int) (string, string) {
			dir := fmt.Sprintf("%d/%d/%d/", startTime.Year(), startTime.Month(), startTime.Day())
			name := fmt.Sprintf("%d_%s.mp4", streamIdx, startTime.Format("2006-01-02T15:04:05"))
			return dir, name
		},
	}
	for _, o := range opts {
		o(newArch)
	}
	if newArch.preBufferDuration == 0 {
		newArch.preBufferDuration = newArch.targetDuration / 2
	}
	if newArch.maxEventDuration == 0 {
		newArch.maxEventDuration = time.Minute
	}
	if newArch.postEventDuration == 0 {
		if newArch.recordMode == gomedia.Event {
			newArch.postEventDuration = newArch.maxEventDuration
		} else {
			newArch.postEventDuration = newArch.targetDuration / 2
		}
	} else if newArch.recordMode == gomedia.Event && newArch.postEventDuration > newArch.maxEventDuration {
		newArch.postEventDuration = newArch.maxEventDuration
	}
	if newArch.dirPerm == 0 {
		newArch.dirPerm = os.ModePerm
	}
	newArch.ringBufferByteCap, newArch.muxerFlushByteCap = ringByteBudgets(
		newArch.targetDuration, newArch.preBufferDuration,
	)
	newArch.AsyncManager = lifecycle.NewFailSafeAsyncManager(newArch, newArch.log)
	return newArch
}

// RingAllocatorBudget returns the recommended GrowingRingAlloc byte cap for the
// given segmenter timings (reference: target=10s, preBuffer=5s → 16 MB).
func RingAllocatorBudget(target, preBuffer time.Duration) int {
	return buffer.RingBudgetBytes(target, preBuffer)
}

// createFile is os.Create with an implicit MkdirAll on ENOENT — avoids the
// usual two-step in the hot path where parent directories almost always exist
// and we only pay for MkdirAll on the first segment of a new day/hour.
func (s *segmenter) createFile(path string) (*os.File, error) {
	f, err := os.Create(path)
	if err == nil {
		return f, nil
	}

	if !os.IsNotExist(err) {
		return nil, err
	}

	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, s.dirPerm); err != nil {
		return nil, err
	}

	return os.Create(path)
}

func (s *segmenter) openNewSegment(url string, stream *streamState, startTime time.Time) error {
	if stream.codecPar.VideoCodecParameters == nil {
		return errors.New("cannot open file with nil video codec parameters")
	}

	streamIdx := s.getSourceIndex(url)
	if streamIdx == -1 {
		return errors.New("URL not found in sources")
	}

	folder, name := s.pathFunc(startTime, streamIdx)

	filename := filepath.Join(s.dest, folder, name)
	f, err := s.createFile(filename)
	if err != nil {
		return err
	}

	var muxer *mp4.Muxer
	if s.batchedDump {
		muxer = mp4.NewMuxer(f, mp4.WithBatchedDump())
	} else {
		muxer = mp4.NewMuxer(f)
	}
	if err = muxer.Mux(stream.codecPar); err != nil {
		_ = f.Close()
		_ = os.Remove(filename)
		return err
	}

	stream.activeFile = &activeFile{
		file:      f,
		muxer:     muxer,
		startTime: startTime,
		folder:    folder,
		name:      name,
		duration:  0,
		lastFlush: 0,
		pktCount:  0,
	}

	return nil
}

// closeSegment runs WriteTrailer then closes the handle. minDuration>0
// discards too-short segments (usually from codec-parameter churn right
// after a keyframe). The returned *FileInfo must be sent via sendFileInfo
// outside streamsMu so a blocked consumer cannot deadlock the writer.
func (s *segmenter) closeSegment(stream *streamState, minDuration time.Duration) (*gomedia.FileInfo, error) {
	if stream.activeFile == nil {
		return nil, nil
	}

	af := stream.activeFile
	stream.activeFile = nil
	filename := filepath.Join(s.dest, af.folder, af.name)

	if af.pktCount == 0 {
		af.muxer.ReleasePending()
		_ = af.file.Close()
		_ = os.Remove(filename)
		return nil, nil
	}

	if minDuration > 0 && af.duration < minDuration {
		s.log.Infof(s, "Discarding short segment %s%s (%v)", af.folder, af.name, af.duration)
		af.muxer.ReleasePending()
		_ = af.file.Close()
		_ = os.Remove(filename)
		return nil, nil
	}

	err := af.muxer.WriteTrailer()
	if err != nil {
		_ = af.file.Close()
		_ = os.Remove(filename)
		return nil, err
	}

	var fileSize int64
	if stat, statErr := af.file.Stat(); statErr == nil {
		fileSize = stat.Size()
	}
	_ = af.file.Close()

	info := &gomedia.FileInfo{
		Name:       af.folder + af.name,
		Start:      af.startTime,
		Stop:       af.startTime.Add(af.duration),
		Size:       int(fileSize),
		URL:        stream.codecPar.SourceID,
		Resolution: fmt.Sprintf("%dx%d", stream.codecPar.VideoCodecParameters.Width(), stream.codecPar.VideoCodecParameters.Height()),
		Codec:      stream.codecPar.VideoCodecParameters.Type().String(),
	}

	return info, nil
}

func (s *segmenter) sendFileInfo(info *gomedia.FileInfo, stopCh <-chan struct{}) {
	if info == nil {
		return
	}
	select {
	case s.outInfoCh <- *info:
	case <-stopCh:
	}
}

// writePacket transfers ownership of pkt to the muxer; it is released
// at the next Flush or WriteTrailer and must not be touched here again.
func (s *segmenter) writePacket(stream *streamState, pkt gomedia.Packet) error {
	if stream.activeFile == nil {
		return errors.New("no active segment")
	}

	af := stream.activeFile

	if err := af.muxer.WritePacket(pkt); err != nil {
		return err
	}

	af.pktCount++

	if _, ok := pkt.(gomedia.VideoPacket); ok {
		af.duration += pkt.Duration()
	}

	return nil
}

func (s *segmenter) Write() {
	startFunc := func(*segmenter) error {
		return nil
	}
	_ = s.Start(startFunc)
}

func (s *segmenter) Step(stopCh <-chan struct{}) (err error) {
	select {
	case <-stopCh:
		return &lifecycle.BreakError{}

	case recordMode := <-s.recordModeCh:
		if recordMode != s.recordMode {
			var pendingInfos []*gomedia.FileInfo
			s.streamsMu.Lock()
			for _, stream := range s.streams {
				if stream.activeFile != nil {
					var info *gomedia.FileInfo
					info, err = s.closeSegment(stream, 0)
					if err != nil {
						s.streamsMu.Unlock()
						return
					}
					if info != nil {
						pendingInfos = append(pendingInfos, info)
					}
				}
				stream.ringBuf.clear()
				stream.seenKeyframe = false
			}
			s.streamsMu.Unlock()
			for _, info := range pendingInfos {
				s.sendFileInfo(info, stopCh)
			}

			s.recordMode = recordMode
			if s.recordCurStatus {
				s.recordCurStatus = false
				s.sendRecordStatus(false)
			}
		}

	case addURL := <-s.addSrcCh:
		s.streamsMu.Lock()
		s.addSource(addURL)
		s.streamsMu.Unlock()

	case rmURL := <-s.rmSrcCh:
		s.streamsMu.Lock()
		info := s.removeSource(rmURL)
		s.streamsMu.Unlock()
		s.sendFileInfo(info, stopCh)

	case <-s.eventCh:
		s.lastEvent = time.Now()
		s.streamsMu.Lock()
		for _, stream := range s.streams {
			// Only idle streams need eventSaved cleared so the next keyframe
			// drains the ring buffer. Resetting it during an active recording
			// blocks the post-event idle close (shouldClose requires eventSaved).
			if stream.activeFile == nil {
				stream.eventSaved = false
			}
		}
		s.streamsMu.Unlock()

	case inpPkt := <-s.inpPktCh:
		if inpPkt == nil {
			return &utils.NilPacketError{}
		}

		if s.recordMode == gomedia.Never {
			inpPkt.Release()
			return errors.New("attempt to process packet with never record mode")
		}

		url := inpPkt.SourceID()

		var pendingInfos []*gomedia.FileInfo

		s.streamsMu.Lock()

		if !s.hasSource(url) {
			s.streamsMu.Unlock()
			inpPkt.Release()
			return nil
		}

		stream, exists := s.streams[url]
		if !exists {
			stream = &streamState{
				activeFile:   nil,
				ringBuf:      newRingBuffer(s.preBufferDuration, ringHardCap(s.preBufferDuration), s.ringBufferByteCap),
				seenKeyframe: false,
				eventSaved:   true,
				codecPar:     gomedia.CodecParametersPair{SourceID: url, AudioCodecParameters: nil, VideoCodecParameters: nil},
			}
			s.streams[url] = stream
		}

		// A codec parameter change invalidates the current MP4 moov; close the
		// segment so the next one is opened with the new parameters.
		if vPkt, ok := inpPkt.(gomedia.VideoPacket); ok {
			if vPkt.CodecParameters() != stream.codecPar.VideoCodecParameters {
				stream.codecPar.VideoCodecParameters = vPkt.CodecParameters()
				var info *gomedia.FileInfo
				info, err = s.closeSegment(stream, time.Second)
				if info != nil {
					pendingInfos = append(pendingInfos, info)
				}
				if err != nil {
					s.streamsMu.Unlock()
					inpPkt.Release()
					return
				}
			}
		}
		if aPkt, ok := inpPkt.(gomedia.AudioPacket); ok {
			if aPkt.CodecParameters() != stream.codecPar.AudioCodecParameters {
				stream.codecPar.AudioCodecParameters = aPkt.CodecParameters()
				var info *gomedia.FileInfo
				info, err = s.closeSegment(stream, time.Second)
				if info != nil {
					pendingInfos = append(pendingInfos, info)
				}
				if err != nil {
					s.streamsMu.Unlock()
					inpPkt.Release()
					return
				}
			}
		}

		// Drop everything before the first keyframe — no decoder can start
		// without it, and Event mode would otherwise fill the ring with
		// undecodable data.
		vPkt, isVideo := inpPkt.(gomedia.VideoPacket)
		isKeyframe := isVideo && vPkt.IsKeyFrame()

		if !stream.seenKeyframe && !isKeyframe {
			s.streamsMu.Unlock()
			inpPkt.Release()
			return nil
		}
		if isKeyframe {
			stream.seenKeyframe = true
		}

		var infos []*gomedia.FileInfo
		switch s.recordMode {
		case gomedia.Always:
			infos, err = s.handleAlwaysMode(url, stream, inpPkt, isKeyframe)
		case gomedia.Event:
			infos, err = s.handleEventMode(url, stream, inpPkt, isKeyframe)
		case gomedia.Never:
		}
		pendingInfos = append(pendingInfos, infos...)

		s.streamsMu.Unlock()

		for _, info := range pendingInfos {
			s.sendFileInfo(info, stopCh)
		}
	}
	return
}

// eventFlushInterval is how much video may accumulate in the MP4 muxer between
// Flush calls during Event-mode recording. Without periodic flush the muxer
// holds every packet until WriteTrailer, which can exhaust the ring allocator
// on long pre/post-event windows.
func (s *segmenter) eventFlushInterval() time.Duration {
	interval := s.preBufferDuration / 4
	if interval < 2*time.Second {
		return 2 * time.Second
	}
	if interval > 8*time.Second {
		return 8 * time.Second
	}
	return interval
}

func (s *segmenter) flushEventSegment(stream *streamState) error {
	af := stream.activeFile
	if af == nil {
		return nil
	}
	if err := af.muxer.Flush(); err != nil {
		return err
	}
	af.lastFlush = af.duration
	return nil
}

func (s *segmenter) maybeFlushEventSegment(stream *streamState) error {
	af := stream.activeFile
	if af == nil {
		return nil
	}
	if af.duration-af.lastFlush < s.eventFlushInterval() &&
		af.muxer.PendingBytes() < s.muxerFlushByteCap {
		return nil
	}
	return s.flushEventSegment(stream)
}

// handleAlwaysMode rotates and writes in the "always recording" mode:
// segments rotate on the first keyframe past targetDuration, and a periodic
// Flush releases muxer-held packets so memory doesn't grow unboundedly on
// sparse-keyframe streams. The returned infos must be sent outside streamsMu.
func (s *segmenter) handleAlwaysMode(url string, stream *streamState, pkt gomedia.Packet, isKeyframe bool) ([]*gomedia.FileInfo, error) {
	var infos []*gomedia.FileInfo

	if isKeyframe && stream.activeFile != nil && stream.activeFile.duration >= s.targetDuration {
		info, err := s.closeSegment(stream, 0)
		if info != nil {
			infos = append(infos, info)
		}
		if err != nil {
			pkt.Release()
			return infos, err
		}
	}

	if stream.activeFile == nil && isKeyframe {
		if err := s.openNewSegment(url, stream, pkt.StartTime()); err != nil {
			pkt.Release()
			return infos, err
		}
		if !s.recordCurStatus {
			s.recordCurStatus = true
			s.sendRecordStatus(true)
		}
	}

	if stream.activeFile != nil {
		if err := s.writePacket(stream, pkt); err != nil {
			return infos, err
		}
		af := stream.activeFile
		if af != nil && af.duration-af.lastFlush >= s.targetDuration+time.Second {
			if err := af.muxer.Flush(); err != nil {
				return infos, err
			}
			af.lastFlush = af.duration
		}
		return infos, nil
	}

	// Waiting for the first keyframe; the packet has no home so release it.
	pkt.Release()
	return infos, nil
}

// handleEventMode keeps the ring buffer filled while idle so a later trigger
// can snap back preBufferDuration seconds before the event itself. Once
// eventSaved=true for the current event, subsequent packets go straight to
// the active file via handleEventModeActiveFile.
func (s *segmenter) handleEventMode(url string, stream *streamState, pkt gomedia.Packet, isKeyframe bool) ([]*gomedia.FileInfo, error) {
	if stream.activeFile != nil {
		return s.handleEventModeActiveFile(url, stream, pkt, isKeyframe)
	}

	stream.ringBuf.add(pkt)

	if isKeyframe {
		stream.ringBuf.trim()
	}

	if !stream.eventSaved && isKeyframe {
		return s.startEventRecording(url, stream)
	}

	return nil, nil
}

// shouldCloseEventRecording reports whether an active event recording has hit
// its max length or outlived the post-event idle window.
func (s *segmenter) shouldCloseEventRecording(stream *streamState) bool {
	if stream.activeFile == nil {
		return false
	}
	videoDur := stream.activeFile.duration
	wallDur := time.Since(stream.eventRecordStart)
	idleDur := time.Since(s.lastEvent)
	return videoDur >= s.maxEventDuration ||
		wallDur >= s.maxEventDuration ||
		idleDur >= s.postEventDuration
}

// handleEventModeActiveFile drives an in-flight event recording. The session
// is sliced into targetDuration segments (rotating only on keyframes so every
// file stays independently decodable) and keeps running as long as triggers
// arrive: it ends postEventDuration after the last event, or when the session
// outlives maxEventDuration. The idle close waits for a keyframe so the next
// recording can start cleanly; the max cap fires immediately so a short cap is
// not stretched to the next GOP.
func (s *segmenter) handleEventModeActiveFile(url string, stream *streamState, pkt gomedia.Packet, isKeyframe bool) ([]*gomedia.FileInfo, error) {
	var infos []*gomedia.FileInfo

	idleReached := isKeyframe && time.Since(s.lastEvent) >= s.postEventDuration
	maxReached := stream.eventSessionDur >= s.maxEventDuration ||
		time.Since(stream.eventRecordStart) >= s.maxEventDuration

	if idleReached || maxReached {
		return s.endEventSession(stream, pkt, isKeyframe)
	}

	// Rotate to a fresh targetDuration segment on the first keyframe past the
	// target so long sessions are split into digestible files rather than one
	// stretched recording.
	if isKeyframe && stream.activeFile.duration >= s.targetDuration {
		info, err := s.closeSegment(stream, 0)
		if info != nil {
			infos = append(infos, info)
		}
		if err != nil {
			pkt.Release()
			return infos, err
		}
		if err = s.openNewSegment(url, stream, pkt.StartTime()); err != nil {
			pkt.Release()
			return infos, err
		}
	}

	var vDur time.Duration
	if vPkt, ok := pkt.(gomedia.VideoPacket); ok {
		vDur = vPkt.Duration()
	}
	if err := s.writePacket(stream, pkt); err != nil {
		return infos, err
	}
	stream.eventSessionDur += vDur
	if err := s.maybeFlushEventSegment(stream); err != nil {
		return infos, err
	}
	return infos, nil
}

// endEventSession finalizes the current event recording and returns the stream
// to idle. The triggering packet is pushed into the pre-buffer instead of being
// dropped so a follow-up event arriving within preBufferDuration can resume
// seamlessly (see startEventRecording).
func (s *segmenter) endEventSession(stream *streamState, pkt gomedia.Packet, isKeyframe bool) ([]*gomedia.FileInfo, error) {
	var infos []*gomedia.FileInfo

	info, err := s.closeSegment(stream, 0)
	if info != nil {
		infos = append(infos, info)
		stream.lastRecordStop = info.Stop
	}
	if err != nil {
		pkt.Release()
		return infos, err
	}
	stream.eventRecordStart = time.Time{}
	stream.eventSessionDur = 0

	hasActiveFile := false
	for _, st := range s.streams {
		if st.activeFile != nil {
			hasActiveFile = true
			break
		}
	}
	if !hasActiveFile && s.recordCurStatus {
		s.recordCurStatus = false
		s.sendRecordStatus(false)
	}

	stream.ringBuf.add(pkt)
	if isKeyframe {
		stream.ringBuf.trim()
	}
	return infos, nil
}

// startEventRecording drains the pre-buffer into fresh segments, skipping
// packets before the first usable keyframe so decoding starts cleanly. The
// pre-buffer itself is sliced into targetDuration chunks (rotating on keyframes)
// so a preBufferDuration larger than targetDuration does not yield one oversized
// leading segment. When a previous event recording ended less than
// preBufferDuration ago the pre-buffer still holds footage already written to
// that file; in that case the drain starts at the first keyframe at/after
// lastRecordStop so the new recording continues right where the last one left
// off instead of duplicating footage.
func (s *segmenter) startEventRecording(url string, stream *streamState) ([]*gomedia.FileInfo, error) {
	var infos []*gomedia.FileInfo

	bufferedPkts := stream.ringBuf.drain()
	if len(bufferedPkts) == 0 {
		return infos, nil
	}

	startIdx := s.findFirstKeyframe(bufferedPkts)
	if !stream.lastRecordStop.IsZero() {
		startIdx = s.findKeyframeAtOrAfter(bufferedPkts, startIdx, stream.lastRecordStop)
	}

	for i := range startIdx {
		bufferedPkts[i].Release()
	}

	if err := s.openNewSegment(url, stream, bufferedPkts[startIdx].StartTime()); err != nil {
		for i := startIdx; i < len(bufferedPkts); i++ {
			bufferedPkts[i].Release()
		}
		return infos, err
	}
	stream.eventRecordStart = time.Now()
	stream.eventSessionDur = 0

	if !s.recordCurStatus {
		s.recordCurStatus = true
		s.sendRecordStatus(true)
	}

	for i := startIdx; i < len(bufferedPkts); i++ {
		pkt := bufferedPkts[i]
		var vDur time.Duration
		isKeyframe := false
		if vPkt, ok := pkt.(gomedia.VideoPacket); ok {
			vDur = vPkt.Duration()
			isKeyframe = vPkt.IsKeyFrame()
		}

		// Rotate the pre-buffer into targetDuration chunks on keyframes, matching
		// the live-recording split so a long pre-buffer is not one big segment.
		if isKeyframe && stream.activeFile.duration >= s.targetDuration {
			info, err := s.closeSegment(stream, 0)
			if info != nil {
				infos = append(infos, info)
			}
			if err != nil {
				for j := i; j < len(bufferedPkts); j++ {
					bufferedPkts[j].Release()
				}
				return infos, err
			}
			if err = s.openNewSegment(url, stream, pkt.StartTime()); err != nil {
				for j := i; j < len(bufferedPkts); j++ {
					bufferedPkts[j].Release()
				}
				return infos, err
			}
		}

		if err := s.writePacket(stream, pkt); err != nil {
			for j := i + 1; j < len(bufferedPkts); j++ {
				bufferedPkts[j].Release()
			}
			return infos, err
		}
		stream.eventSessionDur += vDur
		if err := s.maybeFlushEventSegment(stream); err != nil {
			return infos, err
		}
	}
	if err := s.flushEventSegment(stream); err != nil {
		return infos, err
	}

	stream.eventSaved = true
	return infos, nil
}

func (s *segmenter) findFirstKeyframe(pkts []gomedia.Packet) int {
	for i, p := range pkts {
		if vp, ok := p.(gomedia.VideoPacket); ok && vp.IsKeyFrame() {
			return i
		}
	}
	return 0
}

// findKeyframeAtOrAfter returns the index of the first keyframe (scanning from
// start) whose media start time is not before stop. When the pre-buffer holds
// only fresh footage (the previous recording ended long ago) every keyframe is
// after stop and start is returned unchanged; when the previous recording ended
// recently the leading, already-written keyframes are skipped. If no keyframe
// qualifies, start is returned so the caller still records something.
func (s *segmenter) findKeyframeAtOrAfter(pkts []gomedia.Packet, start int, stop time.Time) int {
	for i := start; i < len(pkts); i++ {
		vp, ok := pkts[i].(gomedia.VideoPacket)
		if !ok || !vp.IsKeyFrame() {
			continue
		}
		if !pkts[i].StartTime().Before(stop) {
			return i
		}
	}
	return start
}

// Release finalizes each open segment, emits their FileInfos with a 5s
// grace period in case the consumer is slow, then drains the packet
// channel so no ring-buffer slots leak after shutdown.
func (s *segmenter) Release() { //nolint: revive
	const stopGraceTimeout = time.Second * 5
	stopCh := make(chan struct{})
	go func() {
		time.Sleep(stopGraceTimeout)
		close(stopCh)
	}()

	var pendingInfos []*gomedia.FileInfo
	s.streamsMu.Lock()
	for _, stream := range s.streams {
		if stream.activeFile != nil {
			info, _ := s.closeSegment(stream, 0)
			if info != nil {
				pendingInfos = append(pendingInfos, info)
			}
		}
		stream.ringBuf.clear()
	}
	s.streamsMu.Unlock()

	for _, info := range pendingInfos {
		s.sendFileInfo(info, stopCh)
	}

	if s.recordCurStatus {
		s.sendRecordStatus(false)
	}

	for {
		select {
		case pkt, ok := <-s.inpPktCh:
			if !ok {
				goto drained
			}
			if pkt != nil {
				pkt.Release()
			}
		default:
			close(s.inpPktCh)
			goto drained
		}
	}
drained:
	close(s.outInfoCh)
	close(s.recordCurStatusCh)
}

const statusSendTimeout = time.Second * 5 //nolint:mnd // grace period for status channel consumers

// sendRecordStatus caps the send at statusSendTimeout so a stalled consumer
// of RecordCurStatus cannot block the Step goroutine.
func (s *segmenter) sendRecordStatus(status bool) {
	select {
	case s.recordCurStatusCh <- status:
	case <-time.After(statusSendTimeout):
		s.log.Infof(s, "Timed out sending record status %v", status)
	}
}

func (s *segmenter) String() string {
	return fmt.Sprintf("ARCHIVER dest=%s", s.dest)
}

func (s *segmenter) Files() <-chan gomedia.FileInfo {
	return s.outInfoCh
}

func (s *segmenter) Packets() chan<- gomedia.Packet {
	return s.inpPktCh
}

func (s *segmenter) RemoveSource() chan<- string {
	return s.rmSrcCh
}

func (s *segmenter) AddSource() chan<- string {
	return s.addSrcCh
}

func (s *segmenter) Events() chan<- struct{} {
	return s.eventCh
}

func (s *segmenter) RecordMode() chan<- gomedia.RecordMode {
	return s.recordModeCh
}

func (s *segmenter) RecordCurStatus() <-chan bool {
	return s.recordCurStatusCh
}
