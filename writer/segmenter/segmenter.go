package segmenter

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/format/mp4"
	"github.com/ugparu/gomedia/utils"
	"github.com/ugparu/gomedia/utils/buffer"
	"github.com/ugparu/gomedia/utils/lifecycle"
	"github.com/ugparu/gomedia/utils/logger"
)

const defaultIOBufInitSize = 8 * 1024 //nolint:mnd // 8KB initial I/O assembly buffer for optional mp4 buffering

// activeFile tracks the on-disk segment currently being recorded.
// The MP4 muxer buffers data internally; Flush() or WriteTrailer() writes to the file.
type activeFile struct {
	file      *os.File
	muxer     *mp4.Muxer
	startTime time.Time
	folder    string
	name      string
	duration  time.Duration
	lastFlush time.Duration // segment duration at last Flush call
	pktCount  int
	lastPkts  map[uint8]gomedia.Packet // packets held by muxer, keyed by stream index
}

// ringBuffer holds a window of packets for Event mode pre-buffering
type ringBuffer struct {
	packets  []gomedia.Packet
	duration time.Duration
	maxDur   time.Duration
	// hardCap bounds accumulated duration when trim() can't fire (no keyframe in sight).
	// On overflow the leading keyframe is preserved and subsequent packets are
	// overwritten front-to-back: packets[2] → packets[1], packets[3] → packets[2], …
	// The result is keyframe + most recent tail. For static scenes playback is roughly
	// coherent despite artifacts from the dropped middle.
	hardCap time.Duration
}

func newRingBuffer(maxDur, hardCap time.Duration) *ringBuffer {
	return &ringBuffer{
		packets:  make([]gomedia.Packet, 0),
		duration: 0,
		maxDur:   maxDur,
		hardCap:  hardCap,
	}
}

func (rb *ringBuffer) add(pkt gomedia.Packet) {
	rb.packets = append(rb.packets, pkt)
	// Only count video packet durations for accurate timing
	if _, ok := pkt.(gomedia.VideoPacket); ok {
		rb.duration += pkt.Duration()
	}
	// Overflow fallback: drop packets[1] and shift the tail down, preserving
	// the leading keyframe. Runs only when trim() has been unable to reclaim
	// space (no keyframe seen for longer than hardCap).
	for rb.duration > rb.hardCap && len(rb.packets) > 1 {
		dropped := rb.packets[1]
		if _, ok := dropped.(gomedia.VideoPacket); ok {
			rb.duration -= dropped.Duration()
		}
		dropped.Release()
		copy(rb.packets[1:], rb.packets[2:])
		rb.packets = rb.packets[:len(rb.packets)-1]
	}
}

// trim removes old packets from the front until duration is within limit
// Only trims on keyframes to ensure we can start decoding
func (rb *ringBuffer) trim() {
	for len(rb.packets) > 1 && rb.duration > rb.maxDur {
		// Find first keyframe after the front that we can trim to
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
			break // No keyframe found, can't trim
		}
		// Calculate duration being trimmed (only video packets)
		var trimDur time.Duration
		for i := range trimIdx {
			if _, ok := rb.packets[i].(gomedia.VideoPacket); ok {
				trimDur += rb.packets[i].Duration()
			}
		}
		// Release ring-buffer slots for the dropped packets.
		for i := 0; i < trimIdx; i++ {
			rb.packets[i].Release()
		}
		rb.packets = rb.packets[trimIdx:]
		rb.duration -= trimDur
	}
}

func (rb *ringBuffer) clear() {
	for _, pkt := range rb.packets {
		pkt.Release()
	}
	rb.packets = rb.packets[:0]
	rb.duration = 0
}

func (rb *ringBuffer) drain() []gomedia.Packet {
	pkts := rb.packets
	rb.packets = rb.packets[:0] // reuse capacity
	rb.duration = 0
	return pkts
}

// streamState holds per-URL stream state
type streamState struct {
	activeFile   *activeFile                 // currently recording file for this URL
	ringBuf      *ringBuffer                 // pre-buffer for event mode
	ioBuf        buffer.Buffer               // optional: reusable I/O assembly buffer passed to mp4.Muxer
	seenKeyframe bool                        // track first keyframe
	eventSaved   bool                        // whether this stream has saved the current event
	codecPar     gomedia.CodecParametersPair // codec parameters for this URL
}

// Option is a functional option for configuring a segmenter.
type Option func(*segmenter)

// PathFunc generates the subdirectory and filename for a new segment.
type PathFunc func(startTime time.Time, streamIdx int) (dir, filename string)

// WithLogger sets the logger for the segmenter.
func WithLogger(l logger.Logger) Option {
	return func(s *segmenter) { s.log = l }
}

// WithPathFunc overrides the default segment path generation logic.
func WithPathFunc(f PathFunc) Option {
	return func(s *segmenter) { s.pathFunc = f }
}

// WithPreBufferDuration overrides the default event mode pre-buffer duration (targetDuration / 2).
func WithPreBufferDuration(d time.Duration) Option {
	return func(s *segmenter) { s.preBufferDuration = d }
}

// WithMaxEventDuration overrides the default max event recording duration (1 minute).
func WithMaxEventDuration(d time.Duration) Option {
	return func(s *segmenter) { s.maxEventDuration = d }
}

// WithDirPermissions overrides the default directory permissions (ModePerm).
func WithDirPermissions(perm os.FileMode) Option {
	return func(s *segmenter) { s.dirPerm = perm }
}

// WithMuxerBuffer enables mp4 muxer in-memory batching and sets the initial buffer size.
// When disabled (default), mp4 payloads are written directly to the file (lower RAM, more syscalls).
// If size <= 0, a sensible default is used.
func WithMuxerBuffer(size int) Option {
	return func(s *segmenter) {
		s.useMuxerBuffer = true
		if size > 0 {
			s.ioBufInitSize = size
		}
	}
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
	maxEventDuration  time.Duration
	dirPerm           os.FileMode
	useMuxerBuffer    bool
	ioBufInitSize     int
	// Per-URL stream state management
	sources   []string                // ordered list of registered URLs
	streams   map[string]*streamState // map of URL to stream state
	streamsMu sync.RWMutex            // protect concurrent access
}

// hasSource checks if a source URL is registered in the sources slice
func (s *segmenter) hasSource(url string) bool {
	for _, src := range s.sources {
		if src == url {
			return true
		}
	}
	return false
}

// getSourceIndex returns the index of a URL in the sources slice, or -1 if not found
func (s *segmenter) getSourceIndex(url string) int {
	for i, src := range s.sources {
		if src == url {
			return i
		}
	}
	return -1
}

// addSource adds a new source URL to the sources slice
// The stream will be created when first packet with codec parameters arrives
func (s *segmenter) addSource(url string) {
	if s.hasSource(url) {
		s.log.Infof(s, "Source %s already exists, skipping", url)
		return
	}

	s.sources = append(s.sources, url)
	s.log.Infof(s, "Added new source %s", url)
}

// removeSource removes a source URL from the sources slice and cleans up resources.
// Returns file info if an active segment was flushed (must be sent outside the lock).
func (s *segmenter) removeSource(url string) *gomedia.FileInfo {
	var info *gomedia.FileInfo
	// Close active file for this stream if it exists
	if stream, exists := s.streams[url]; exists {
		if stream.activeFile != nil {
			info, _ = s.closeSegment(stream, 0)
		}
		stream.ringBuf.clear()
		delete(s.streams, url)
	}

	// Remove from sources slice
	for i, src := range s.sources {
		if src == url {
			s.sources = append(s.sources[:i], s.sources[i+1:]...)
			s.log.Infof(s, "Removed source %s", url)
			break
		}
	}
	return info
}

// New creates a new instance of the archiver with the specified parameters.
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
		ioBufInitSize: defaultIOBufInitSize,
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
	if newArch.dirPerm == 0 {
		newArch.dirPerm = os.ModePerm
	}
	newArch.AsyncManager = lifecycle.NewFailSafeAsyncManager(newArch, newArch.log)
	return newArch
}

// createFile creates a file ensuring parent directories exist
func (s *segmenter) createFile(path string) (*os.File, error) {
	f, err := os.Create(path)
	if err == nil {
		return f, nil
	}

	if !os.IsNotExist(err) {
		return nil, err
	}

	// Create parent directory
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, s.dirPerm); err != nil {
		return nil, err
	}

	// Try creating the file again
	return os.Create(path)
}

// openNewSegment opens a file on disk and initializes the MP4 muxer for a new segment.
// If WithMuxerBuffer is enabled, the muxer batches header + packet data in memory
// and writes on Flush/WriteTrailer. Otherwise packet payloads are written directly
// to the file as they arrive.
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
	if s.useMuxerBuffer {
		if stream.ioBuf == nil {
			stream.ioBuf = buffer.Get(s.ioBufInitSize)
		}
		muxer = mp4.NewMuxer(f, mp4.WithBuffer(stream.ioBuf))
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
		lastPkts:  make(map[uint8]gomedia.Packet),
	}

	return nil
}

// closeSegment finalizes the active segment: calls WriteTrailer (which writes the
// complete MP4 to the file), then closes the file handle.
// The returned *gomedia.FileInfo must be sent via sendFileInfo outside of any lock.
// minDuration discards segments shorter than the threshold (use 0 to keep all).
func (s *segmenter) closeSegment(stream *streamState, minDuration time.Duration) (*gomedia.FileInfo, error) {
	if stream.activeFile == nil {
		return nil, nil
	}

	af := stream.activeFile
	stream.activeFile = nil
	filename := filepath.Join(s.dest, af.folder, af.name)

	if af.pktCount == 0 {
		for _, p := range af.lastPkts {
			p.Release()
		}
		_ = af.file.Close()
		_ = os.Remove(filename)
		return nil, nil
	}

	// Discard segments shorter than minDuration — these are typically artifacts
	// of codec parameter initialization arriving after the first keyframe.
	if minDuration > 0 && af.duration < minDuration {
		s.log.Infof(s, "Discarding short segment %s%s (%v)", af.folder, af.name, af.duration)
		for _, p := range af.lastPkts {
			p.Release()
		}
		_ = af.file.Close()
		_ = os.Remove(filename)
		return nil, nil
	}

	// WriteTrailer writes remaining buffered packets + moov to the file.
	// If Flush was never called it uses the fast no-seek path (single write).
	// If Flush was called (always-mode periodic flush) it uses the seek path.
	err := af.muxer.WriteTrailer()

	// Release the last held packets (consumed by WriteTrailer).
	for _, p := range af.lastPkts {
		p.Release()
	}

	if err != nil {
		_ = af.file.Close()
		_ = os.Remove(filename)
		return nil, err
	}

	// Get file size before closing.
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

// sendFileInfo sends file info to the output channel without holding any lock.
func (s *segmenter) sendFileInfo(info *gomedia.FileInfo, stopCh <-chan struct{}) {
	if info == nil {
		return
	}
	select {
	case s.outInfoCh <- *info:
	case <-stopCh:
	}
}

// writePacket buffers a packet via the active segment's muxer.
// The muxer holds one packet per stream internally; the previously held packet
// is released here after the muxer consumes it.
func (s *segmenter) writePacket(stream *streamState, pkt gomedia.Packet) error {
	if stream.activeFile == nil {
		return errors.New("no active segment")
	}

	af := stream.activeFile

	if err := af.muxer.WritePacket(pkt); err != nil {
		pkt.Release()
		return err
	}

	// The muxer consumed the previous packet for this stream index.
	// Release it now that its data has been written to disk.
	idx := pkt.StreamIndex()
	if prev, ok := af.lastPkts[idx]; ok {
		prev.Release()
	}
	af.lastPkts[idx] = pkt
	af.pktCount++

	if _, ok := pkt.(gomedia.VideoPacket); ok {
		af.duration += pkt.Duration()
	}

	return nil
}

// Write initiates the writing process for the archiver based on the provided codec parameters.
func (s *segmenter) Write() {
	startFunc := func(*segmenter) error {
		return nil
	}
	_ = s.Start(startFunc)
}

// Step processes a single step in the archiving pipeline based on the provided channels and parameters.
func (s *segmenter) Step(stopCh <-chan struct{}) (err error) {
	select {
	case <-stopCh:
		return &lifecycle.BreakError{}

	case recordMode := <-s.recordModeCh:
		if recordMode != s.recordMode {
			// Close all active files and clear all buffers
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
			stream.eventSaved = false
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

		// Extract URL from packet
		url := inpPkt.SourceID()

		var pendingInfos []*gomedia.FileInfo

		s.streamsMu.Lock()

		// Check if URL is registered
		if !s.hasSource(url) {
			s.streamsMu.Unlock()
			inpPkt.Release()
			return nil // Skip packets from unregistered sources
		}

		// Get or create stream state for this URL
		stream, exists := s.streams[url]
		if !exists {
			// Create new stream state
			stream = &streamState{
				activeFile:   nil,
				ringBuf:      newRingBuffer(s.preBufferDuration, s.preBufferDuration+s.targetDuration),
				seenKeyframe: false,
				eventSaved:   true,
				codecPar:     gomedia.CodecParametersPair{SourceID: url, AudioCodecParameters: nil, VideoCodecParameters: nil},
			}
			s.streams[url] = stream
		}

		// Update codec parameters if changed
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

		// Wait for first keyframe
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

		// Handle based on record mode
		var infos []*gomedia.FileInfo
		switch s.recordMode {
		case gomedia.Always:
			infos, err = s.handleAlwaysMode(url, stream, inpPkt, isKeyframe)
		case gomedia.Event:
			infos, err = s.handleEventMode(url, stream, inpPkt, isKeyframe)
		case gomedia.Never:
			// Should not reach here due to earlier check
		}
		pendingInfos = append(pendingInfos, infos...)

		s.streamsMu.Unlock()

		for _, info := range pendingInfos {
			s.sendFileInfo(info, stopCh)
		}
	}
	return
}

// handleAlwaysMode handles packet processing in Always record mode for a specific stream.
// Returns file infos that must be sent outside the lock.
func (s *segmenter) handleAlwaysMode(url string, stream *streamState, pkt gomedia.Packet, isKeyframe bool) ([]*gomedia.FileInfo, error) {
	var infos []*gomedia.FileInfo

	// Check if we need to rotate file (on keyframe when duration exceeded)
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

	// Open new file if needed (on keyframe)
	if stream.activeFile == nil && isKeyframe {
		if err := s.openNewSegment(url, stream, pkt.StartTime()); err != nil {
			pkt.Release()
			return infos, err
		}
		// Update recording status
		if !s.recordCurStatus {
			s.recordCurStatus = true
			s.sendRecordStatus(true)
		}
	}

	// Write packet if we have an active file
	if stream.activeFile != nil {
		if err := s.writePacket(stream, pkt); err != nil {
			return infos, err
		}
		// Periodic flush to avoid unbounded memory growth when keyframes are rare.
		af := stream.activeFile
		if af != nil && af.duration-af.lastFlush >= s.targetDuration+time.Second {
			if err := af.muxer.Flush(); err != nil {
				return infos, err
			}
			af.lastFlush = af.duration
		}
		return infos, nil
	}

	// No active file and not a keyframe: waiting for next keyframe to open a new
	// segment. The packet is not stored anywhere, so release its ring slot.
	pkt.Release()
	return infos, nil
}

// handleEventMode handles packet processing in Event record mode for a specific stream.
// Returns file infos that must be sent outside the lock.
func (s *segmenter) handleEventMode(url string, stream *streamState, pkt gomedia.Packet, isKeyframe bool) ([]*gomedia.FileInfo, error) {
	// If we have an active file, handle writing or closing
	if stream.activeFile != nil {
		return s.handleEventModeActiveFile(url, stream, pkt, isKeyframe)
	}

	// Buffer mode: accumulate packets in ring buffer
	stream.ringBuf.add(pkt)

	// Trim old packets on keyframe
	if isKeyframe {
		stream.ringBuf.trim()
	}

	// Check if event was triggered and we should start recording
	if !stream.eventSaved && isKeyframe {
		return nil, s.startEventRecording(url, stream)
	}

	return nil, nil
}

// handleEventModeActiveFile handles event mode when file is already open for a specific stream.
// Returns file infos that must be sent outside the lock.
func (s *segmenter) handleEventModeActiveFile(url string, stream *streamState, pkt gomedia.Packet, isKeyframe bool) ([]*gomedia.FileInfo, error) {
	var infos []*gomedia.FileInfo

	// Check if we should close the file (event saved and enough time passed)
	shouldClose := isKeyframe && stream.eventSaved &&
		(time.Since(s.lastEvent) >= s.targetDuration/2 || stream.activeFile.duration >= s.maxEventDuration)

	if shouldClose {
		info, err := s.closeSegment(stream, 0)
		if info != nil {
			infos = append(infos, info)
		}
		if err != nil {
			pkt.Release()
			return infos, err
		}

		// Check if any other stream is still recording
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
		// Switching back to buffer mode. Current packet is not stored; release it.
		pkt.Release()
		return infos, nil
	}

	// Check if we need to rotate file (duration exceeded but event still active)
	if isKeyframe && !stream.eventSaved && stream.activeFile.duration >= s.targetDuration {
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

	return infos, s.writePacket(stream, pkt)
}

// startEventRecording starts recording when event is triggered for a specific stream
func (s *segmenter) startEventRecording(url string, stream *streamState) error {
	bufferedPkts := stream.ringBuf.drain()
	if len(bufferedPkts) == 0 {
		return nil
	}

	// Find first keyframe in buffer to start file
	startIdx := s.findFirstKeyframe(bufferedPkts)

	// Release packets before the start keyframe — they won't be written.
	for i := 0; i < startIdx; i++ {
		bufferedPkts[i].Release()
	}

	if err := s.openNewSegment(url, stream, bufferedPkts[startIdx].StartTime()); err != nil {
		for i := startIdx; i < len(bufferedPkts); i++ {
			bufferedPkts[i].Release()
		}
		return err
	}

	if !s.recordCurStatus {
		s.recordCurStatus = true
		s.sendRecordStatus(true)
	}

	// Write buffered packets
	for i := startIdx; i < len(bufferedPkts); i++ {
		if err := s.writePacket(stream, bufferedPkts[i]); err != nil {
			for j := i + 1; j < len(bufferedPkts); j++ {
				bufferedPkts[j].Release()
			}
			return err
		}
	}

	stream.eventSaved = true
	return nil
}

// findFirstKeyframe finds the index of first keyframe in packet slice
func (s *segmenter) findFirstKeyframe(pkts []gomedia.Packet) int {
	for i, p := range pkts {
		if vp, ok := p.(gomedia.VideoPacket); ok && vp.IsKeyFrame() {
			return i
		}
	}
	return 0
}

// Release initiates the closing process for the archiver.
func (s *segmenter) Release() { //nolint: revive
	const stopGraceTimeout = time.Second * 5
	stopCh := make(chan struct{})
	go func() {
		time.Sleep(stopGraceTimeout)
		close(stopCh)
	}()

	// Close all active files and clear ring buffers
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

	// Drain remaining packets from the channel to prevent leaks.
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

// sendRecordStatus attempts to send a recording status update with a timeout
// to prevent blocking if the consumer is not draining the channel.
func (s *segmenter) sendRecordStatus(status bool) {
	select {
	case s.recordCurStatusCh <- status:
	case <-time.After(statusSendTimeout):
		s.log.Infof(s, "Timed out sending record status %v", status)
	}
}

// String returns a string representation of the archiver, indicating the destination path.
func (s *segmenter) String() string {
	return fmt.Sprintf("ARCHIVER dest=%s", s.dest)
}

// Files returns a channel for receiving information about archived files.
func (s *segmenter) Files() <-chan gomedia.FileInfo {
	return s.outInfoCh
}

// Packets returns a channel for sending input media packets to be archived.
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

// RecordMode returns a channel for sending updates to the recording mode.
func (s *segmenter) RecordMode() chan<- gomedia.RecordMode {
	return s.recordModeCh
}

func (s *segmenter) RecordCurStatus() <-chan bool {
	return s.recordCurStatusCh
}
