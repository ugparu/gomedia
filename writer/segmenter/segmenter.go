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
	packets  []gomedia.Packet
	duration time.Duration
	maxDur   time.Duration
	hardCap  time.Duration
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
	// Only video packets have meaningful durations; audio packet timing is
	// per-chunk and would distort the rolling window bound.
	if _, ok := pkt.(gomedia.VideoPacket); ok {
		rb.duration += pkt.Duration()
	}
	// Overflow fallback: shift the tail down over packet[1] so the leading
	// keyframe survives even when no later keyframe is in sight for trim().
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
	rb.packets = rb.packets[:0]
	rb.duration = 0
	return pkts
}

type streamState struct {
	activeFile   *activeFile
	ringBuf      *ringBuffer
	seenKeyframe bool
	eventSaved   bool
	codecPar     gomedia.CodecParametersPair
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
	maxEventDuration  time.Duration
	dirPerm           os.FileMode
	batchedDump       bool

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
	if newArch.dirPerm == 0 {
		newArch.dirPerm = os.ModePerm
	}
	newArch.AsyncManager = lifecycle.NewFailSafeAsyncManager(newArch, newArch.log)
	return newArch
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
				ringBuf:      newRingBuffer(s.preBufferDuration, s.preBufferDuration+s.targetDuration),
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
		return nil, s.startEventRecording(url, stream)
	}

	return nil, nil
}

// handleEventModeActiveFile closes the in-flight event file either when the
// trigger has gone idle for ≥ targetDuration/2, or when the event has run
// past maxEventDuration — both thresholds must align with a keyframe so the
// next segment starts decodable.
func (s *segmenter) handleEventModeActiveFile(url string, stream *streamState, pkt gomedia.Packet, isKeyframe bool) ([]*gomedia.FileInfo, error) {
	var infos []*gomedia.FileInfo

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
		pkt.Release()
		return infos, nil
	}

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

// startEventRecording drains the pre-buffer into a fresh segment, skipping
// packets before the first buffered keyframe so decoding starts cleanly.
func (s *segmenter) startEventRecording(url string, stream *streamState) error {
	bufferedPkts := stream.ringBuf.drain()
	if len(bufferedPkts) == 0 {
		return nil
	}

	startIdx := s.findFirstKeyframe(bufferedPkts)

	for i := range startIdx {
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

func (s *segmenter) findFirstKeyframe(pkts []gomedia.Packet) int {
	for i, p := range pkts {
		if vp, ok := p.(gomedia.VideoPacket); ok && vp.IsKeyFrame() {
			return i
		}
	}
	return 0
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
