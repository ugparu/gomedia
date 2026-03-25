package segmenter

import (
	"errors"
	"fmt"
	"io"
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

const memBufInitSize = 512 * 1024 //nolint:mnd // 512KB initial buffer, covers ~1s of 4Mbps video

// memWriteSeeker is an in-memory io.WriteSeeker backed by buffer.PooledBuffer.
// All muxer writes go here; the buffer is flushed to disk in one write at segment close.
type memWriteSeeker struct {
	buf buffer.Buffer
	len int // logical length of written data (may differ from buf.Len after Resize)
	pos int
}

func newMemWriteSeeker() *memWriteSeeker {
	return &memWriteSeeker{buf: buffer.Get(memBufInitSize)}
}

func (m *memWriteSeeker) Write(p []byte) (int, error) {
	end := m.pos + len(p)
	if end > m.len {
		if end > m.buf.Cap() {
			m.buf.Resize(end)
		} else if end > m.buf.Len() {
			m.buf.Resize(end)
		}
		m.len = end
	}
	copy(m.buf.Data()[m.pos:], p)
	m.pos = end
	return len(p), nil
}

func (m *memWriteSeeker) Seek(offset int64, whence int) (int64, error) {
	var newPos int64
	switch whence {
	case io.SeekStart:
		newPos = offset
	case io.SeekCurrent:
		newPos = int64(m.pos) + offset
	case io.SeekEnd:
		newPos = int64(m.len) + offset
	default:
		return 0, errors.New("invalid whence")
	}
	if newPos < 0 {
		return 0, errors.New("negative seek position")
	}
	m.pos = int(newPos)
	return newPos, nil
}

// Bytes returns the accumulated data up to the logical length.
func (m *memWriteSeeker) Bytes() []byte { return m.buf.Data()[:m.len] }

// Len returns the logical length of written data.
func (m *memWriteSeeker) Len() int { return m.len }

// Reset resets the buffer for reuse, keeping the allocated capacity.
func (m *memWriteSeeker) Reset() { m.len = 0; m.pos = 0 }

// ensure interface compliance at compile time.
var _ io.WriteSeeker = (*memWriteSeeker)(nil)

// activeFile accumulates packets in memory via the MP4 muxer.
// The muxer holds one packet per stream internally (lastPacket);
// we track those in lastPkts so they can be released after writePacket
// consumes them or when the segment is closed.
type activeFile struct {
	mem       *memWriteSeeker
	muxer     *mp4.Muxer
	startTime time.Time
	folder    string
	name      string
	duration  time.Duration
	pktCount  int
	lastPkts  map[uint8]gomedia.Packet // packets held by muxer, keyed by stream index
}

// ringBuffer holds a window of packets for Event mode pre-buffering
type ringBuffer struct {
	packets  []gomedia.Packet
	duration time.Duration
	maxDur   time.Duration
}

func newRingBuffer(maxDur time.Duration) *ringBuffer {
	return &ringBuffer{
		packets:  make([]gomedia.Packet, 0),
		duration: 0,
		maxDur:   maxDur,
	}
}

func (rb *ringBuffer) add(pkt gomedia.Packet) {
	rb.packets = append(rb.packets, pkt)
	// Only count video packet durations for accurate timing
	if _, ok := pkt.(gomedia.VideoPacket); ok {
		rb.duration += pkt.Duration()
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
	mem          *memWriteSeeker             // reusable in-memory buffer for MP4 muxing
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

// openNewSegment initializes an in-memory MP4 muxer for a new segment.
// No file is created on disk until the segment is closed via closeSegment.
func (s *segmenter) openNewSegment(url string, stream *streamState, startTime time.Time) error {
	if stream.codecPar.VideoCodecParameters == nil {
		return errors.New("cannot open file with nil video codec parameters")
	}

	streamIdx := s.getSourceIndex(url)
	if streamIdx == -1 {
		return errors.New("URL not found in sources")
	}

	folder, name := s.pathFunc(startTime, streamIdx)

	if stream.mem == nil {
		stream.mem = newMemWriteSeeker()
	} else {
		stream.mem.Reset()
	}
	muxer := mp4.NewMuxer(stream.mem)
	if err := muxer.Mux(stream.codecPar); err != nil {
		return err
	}

	stream.activeFile = &activeFile{
		mem:       stream.mem,
		muxer:     muxer,
		startTime: startTime,
		folder:    folder,
		name:      name,
		duration:  0,
		pktCount:  0,
		lastPkts:  make(map[uint8]gomedia.Packet),
	}

	return nil
}

// closeSegment finalizes the active segment: writes the MP4 trailer in memory,
// then flushes the entire buffer to disk in a single write.
// The returned *gomedia.FileInfo must be sent via sendFileInfo outside of any lock.
// minDuration discards segments shorter than the threshold (use 0 to keep all).
func (s *segmenter) closeSegment(stream *streamState, minDuration time.Duration) (*gomedia.FileInfo, error) {
	if stream.activeFile == nil {
		return nil, nil
	}

	af := stream.activeFile
	stream.activeFile = nil

	if af.pktCount == 0 {
		for _, p := range af.lastPkts {
			p.Release()
		}
		return nil, nil
	}

	// Discard segments shorter than minDuration — these are typically artifacts
	// of codec parameter initialization arriving after the first keyframe.
	if minDuration > 0 && af.duration < minDuration {
		s.log.Infof(s, "Discarding short segment %s%s (%v)", af.folder, af.name, af.duration)
		for _, p := range af.lastPkts {
			p.Release()
		}
		return nil, nil
	}

	// WriteTrailer writes the remaining buffered packets and the moov atom — all in memory.
	err := af.muxer.WriteTrailer()

	// Release the last held packets (consumed by WriteTrailer).
	for _, p := range af.lastPkts {
		p.Release()
	}

	if err != nil {
		return nil, err
	}

	// Flush the entire MP4 to disk in a single write.
	filename := filepath.Join(s.dest, af.folder, af.name)
	f, err := s.createFile(filename)
	if err != nil {
		return nil, err
	}

	data := af.mem.Bytes()
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(filename)
		return nil, err
	}
	_ = f.Close()

	info := &gomedia.FileInfo{
		Name:       af.folder + af.name,
		Start:      af.startTime,
		Stop:       af.startTime.Add(af.duration),
		Size:       len(data),
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

// writePacket writes a packet to the active segment's muxer immediately.
// The muxer holds one packet per stream internally; the previously held packet
// is released here after the muxer writes it to disk.
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
				ringBuf:      newRingBuffer(s.preBufferDuration),
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
		return infos, s.writePacket(stream, pkt)
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
