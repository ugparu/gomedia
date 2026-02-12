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
	"github.com/ugparu/gomedia/utils/lifecycle"
	"github.com/ugparu/gomedia/utils/logger"
)

// activeFile represents a currently open file being written to
type activeFile struct {
	file      *os.File
	muxer     *mp4.Muxer
	startTime time.Time
	folder    string
	name      string
	duration  time.Duration
	wg        sync.WaitGroup // Track pending packet closures for SwitchToFile
	count     int
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
			rb.packets[i].Close()
		}
		rb.packets = rb.packets[trimIdx:]
		rb.duration -= trimDur
	}
}

func (rb *ringBuffer) clear() {
	for _, pkt := range rb.packets {
		pkt.Close()
	}
	rb.packets = rb.packets[:0]
	rb.duration = 0
}

func (rb *ringBuffer) drain() []gomedia.Packet {
	pkts := rb.packets
	rb.packets = make([]gomedia.Packet, 0)
	rb.duration = 0
	return pkts
}

// streamState holds per-URL stream state
type streamState struct {
	activeFile   *activeFile                 // currently recording file for this URL
	ringBuf      *ringBuffer                 // pre-buffer for event mode
	seenKeyframe bool                        // track first keyframe
	codecPar     gomedia.CodecParametersPair // codec parameters for this URL
}

type segmenter struct {
	lifecycle.AsyncManager[*segmenter]
	recordMode        gomedia.RecordMode
	targetDuration    time.Duration
	recordModeCh      chan gomedia.RecordMode
	eventCh           chan struct{}
	inpPktCh          chan gomedia.Packet
	outInfoCh         chan gomedia.FileInfo
	recordCurStatusCh chan bool
	recordCurStatus   bool
	lastEvent         time.Time
	eventSaved        bool
	dest              string
	rmSrcCh           chan string
	addSrcCh          chan string

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
		logger.Infof(s, "Source %s already exists, skipping", url)
		return
	}

	s.sources = append(s.sources, url)
	logger.Infof(s, "Added new source %s", url)
}

// removeSource removes a source URL from the sources slice and cleans up resources
func (s *segmenter) removeSource(url string) {
	// Close active file for this stream if it exists
	if stream, exists := s.streams[url]; exists {
		if stream.activeFile != nil {
			_ = s.closeActiveFile(stream, s.Done())
		}
		stream.ringBuf.clear()
		delete(s.streams, url)
	}

	// Remove from sources slice
	for i, src := range s.sources {
		if src == url {
			s.sources = append(s.sources[:i], s.sources[i+1:]...)
			logger.Infof(s, "Removed source %s", url)
			break
		}
	}
}

// New creates a new instance of the archiver with the specified parameters.
func New(dest string, segSize time.Duration, recordMode gomedia.RecordMode, chanSize int) gomedia.Segmenter {
	newArch := &segmenter{
		AsyncManager:      nil,
		recordMode:        recordMode,
		targetDuration:    segSize,
		recordModeCh:      make(chan gomedia.RecordMode, chanSize),
		eventCh:           make(chan struct{}, chanSize),
		inpPktCh:          make(chan gomedia.Packet, chanSize),
		outInfoCh:         make(chan gomedia.FileInfo, chanSize),
		recordCurStatusCh: make(chan bool, chanSize),
		recordCurStatus:   false,
		lastEvent:         time.Now(),
		eventSaved:        true,
		dest:              dest,
		rmSrcCh:           make(chan string, chanSize),
		addSrcCh:          make(chan string, chanSize),
		sources:           []string{},
		streams:           make(map[string]*streamState),
		streamsMu:         sync.RWMutex{},
	}
	newArch.AsyncManager = lifecycle.NewFailSafeAsyncManager[*segmenter](newArch)
	return newArch
}

// createFile creates a file ensuring parent directories exist
func createFile(path string) (*os.File, error) {
	f, err := os.Create(path)
	if err == nil {
		return f, nil
	}

	if !os.IsNotExist(err) {
		return nil, err
	}

	// Create parent directory
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, os.ModePerm); err != nil { //nolint:mnd
		return nil, err
	}

	// Try creating the file again
	return os.Create(path)
}

// openNewFile opens a new MP4 file for writing for the specified URL stream
func (s *segmenter) openNewFile(url string, stream *streamState, startTime time.Time) error {
	if stream.codecPar.VideoCodecParameters == nil {
		return errors.New("cannot open file with nil video codec parameters")
	}

	// Get stream index for filename
	streamIdx := s.getSourceIndex(url)
	if streamIdx == -1 {
		return errors.New("URL not found in sources")
	}

	folder := fmt.Sprintf("%d/%d/%d/", startTime.Year(), startTime.Month(), startTime.Day())
	name := fmt.Sprintf("%d_%s.mp4", streamIdx, startTime.Format("2006-01-02T15:04:05"))
	filename := fmt.Sprintf("%s%s%s", s.dest, folder, name)

	f, err := createFile(filename)
	if err != nil {
		return err
	}

	muxer := mp4.NewMuxer(f)
	if err = muxer.Mux(stream.codecPar); err != nil {
		_ = f.Close()
		return err
	}

	stream.activeFile = &activeFile{
		file:      f,
		muxer:     muxer,
		startTime: startTime,
		folder:    folder,
		name:      name,
		duration:  0,
		wg:        sync.WaitGroup{},
	}

	return nil
}

// closeActiveFile closes the active file for a specific stream and emits file info
func (s *segmenter) closeActiveFile(stream *streamState, stopChan <-chan struct{}) error {
	if stream.activeFile == nil {
		return nil
	}

	af := stream.activeFile
	stream.activeFile = nil

	// Capture last packets from muxer because WriteTrailer will flush them from muxer logic
	// but does not close them. We must close them after WriteTrailer.
	// We check indices 0 and 1 as we typically have at most 2 streams (Audio/Video).
	var lastPackets []gomedia.Packet
	for i := uint8(0); i < 2; i++ {
		if pkt := af.muxer.GetPreLastPacket(i); pkt != nil {
			lastPackets = append(lastPackets, pkt)
		}
	}

	if err := af.muxer.WriteTrailer(); err != nil {
		// Even on error, we must close captured packets as Muxer likely dropped them
		for _, pkt := range lastPackets {
			pkt.Close()
		}
		_ = af.file.Close()
		return err
	}

	for _, pkt := range lastPackets {
		pkt.Close()
	}

	fi, err := af.file.Stat()
	if err != nil {
		_ = af.file.Close()
		return err
	}

	select {
	case s.outInfoCh <- gomedia.FileInfo{
		Name:       af.folder + af.name,
		Start:      af.startTime,
		Stop:       af.startTime.Add(af.duration),
		Size:       int(fi.Size()),
		URL:        stream.codecPar.URL,
		Resolution: fmt.Sprintf("%dx%d", stream.codecPar.VideoCodecParameters.Width(), stream.codecPar.VideoCodecParameters.Height()),
	}:
	case <-stopChan:
		return err
	}

	// Close file in separate goroutine after all referenced packets are closed
	go func(af *activeFile) {
		// Wait for all packets to be closed with timeout of 2x duration
		waitDone := make(chan struct{})
		go func() {
			af.wg.Wait()
			close(waitDone)
		}()

		select {
		case <-waitDone:
		case <-time.After(af.duration * 3):
			logger.Warningf(af.folder, "timeout waiting for packets to close for file %s (waited %v), closing anyway",
				af.name, af.duration*3)
		}

		_ = af.file.Close()
	}(af)

	return nil
}

// writePacketToFile writes a packet directly to the active file of a stream
func (s *segmenter) writePacketToFile(stream *streamState, pkt gomedia.Packet) error {
	if stream.activeFile == nil {
		pkt.Close()
		return errors.New("no active file")
	}

	af := stream.activeFile

	// Get the pre-last packet (the one that will be written)
	preLastPkt := af.muxer.GetPreLastPacket(pkt.StreamIndex())

	if err := af.muxer.WritePacket(pkt); err != nil {
		if preLastPkt != nil {
			preLastPkt.Close()
		}
		return err
	}

	// Switch pre-last packet to file if it was written
	if preLastPkt != nil {
		// Get the actual packet data offset and size (excluding SPS/PPS/VPS for keyframes)
		dataOffset, dataSize := af.muxer.GetLastPacketDataInfo(pkt.StreamIndex())
		if dataSize > 0 {
			af.wg.Add(1)
			af.count++
			done := func() error {
				af.wg.Done()
				af.count--
				return nil
			}
			if err := preLastPkt.SwitchToFile(af.file, dataOffset, dataSize, done); err != nil {
				af.wg.Done() // Ensure WaitGroup is decremented on error
				preLastPkt.Close()
				return err
			}
		}
		preLastPkt.Close()
	}

	// Only count video packet durations for segment timing
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
			s.streamsMu.Lock()
			for _, stream := range s.streams {
				if stream.activeFile != nil {
					if err = s.closeActiveFile(stream, stopCh); err != nil {
						s.streamsMu.Unlock()
						return
					}
				}
				stream.ringBuf.clear()
				stream.seenKeyframe = false
			}
			s.streamsMu.Unlock()

			s.recordMode = recordMode
			if s.recordCurStatus {
				s.recordCurStatus = false
				s.recordCurStatusCh <- false
			}
		}

	case addURL := <-s.addSrcCh:
		s.streamsMu.Lock()
		s.addSource(addURL)
		s.streamsMu.Unlock()

	case rmURL := <-s.rmSrcCh:
		s.streamsMu.Lock()
		s.removeSource(rmURL)
		s.streamsMu.Unlock()

	case <-s.eventCh:
		s.eventSaved = false
		s.lastEvent = time.Now()

	case inpPkt := <-s.inpPktCh:
		if s.recordMode == gomedia.Never {
			inpPkt.Close()
			return errors.New("attempt to process packet with never record mode")
		}

		if inpPkt == nil {
			return &utils.NilPacketError{}
		}

		// Extract URL from packet
		url := inpPkt.URL()

		s.streamsMu.Lock()
		defer s.streamsMu.Unlock()

		// Check if URL is registered
		if !s.hasSource(url) {
			inpPkt.Close()
			return nil // Skip packets from unregistered sources
		}

		// Get or create stream state for this URL
		stream, exists := s.streams[url]
		if !exists {
			// Create new stream state
			stream = &streamState{
				activeFile:   nil,
				ringBuf:      newRingBuffer(s.targetDuration / 2), //nolint:mnd
				seenKeyframe: false,
				codecPar:     gomedia.CodecParametersPair{URL: url, AudioCodecParameters: nil, VideoCodecParameters: nil},
			}
			s.streams[url] = stream
		}

		// Update codec parameters if changed
		if vPkt, ok := inpPkt.(gomedia.VideoPacket); ok {
			if vPkt.CodecParameters() != stream.codecPar.VideoCodecParameters {
				stream.codecPar.VideoCodecParameters = vPkt.CodecParameters()
				if err = s.closeActiveFile(stream, s.Done()); err != nil {
					inpPkt.Close()
					return
				}
			}
		}
		if aPkt, ok := inpPkt.(gomedia.AudioPacket); ok {
			if aPkt.CodecParameters() != stream.codecPar.AudioCodecParameters {
				stream.codecPar.AudioCodecParameters = aPkt.CodecParameters()
				if err = s.closeActiveFile(stream, s.Done()); err != nil {
					inpPkt.Close()
					return
				}
			}
		}

		// Wait for first keyframe
		vPkt, isVideo := inpPkt.(gomedia.VideoPacket)
		isKeyframe := isVideo && vPkt.IsKeyFrame()

		if !stream.seenKeyframe && !isKeyframe {
			inpPkt.Close()
			return nil
		}
		if isKeyframe {
			stream.seenKeyframe = true
		}

		// Handle based on record mode
		switch s.recordMode {
		case gomedia.Always:
			err = s.handleAlwaysMode(url, stream, stopCh, inpPkt, isKeyframe)
		case gomedia.Event:
			err = s.handleEventMode(url, stream, stopCh, inpPkt, isKeyframe)
		case gomedia.Never:
			// Should not reach here due to earlier check
		}
	}
	return
}

// handleAlwaysMode handles packet processing in Always record mode for a specific stream
func (s *segmenter) handleAlwaysMode(url string, stream *streamState, stopCh <-chan struct{}, pkt gomedia.Packet, isKeyframe bool) error {
	// Check if we need to rotate file (on keyframe when duration exceeded)
	if isKeyframe && stream.activeFile != nil && stream.activeFile.duration >= s.targetDuration {
		if err := s.closeActiveFile(stream, stopCh); err != nil {
			pkt.Close()
			return err
		}
	}

	// Open new file if needed (on keyframe)
	if stream.activeFile == nil && isKeyframe {
		if err := s.openNewFile(url, stream, pkt.StartTime()); err != nil {
			pkt.Close()
			return err
		}
		// Update recording status
		if !s.recordCurStatus {
			s.recordCurStatus = true
			s.recordCurStatusCh <- true
		}
	}

	// Write packet if we have an active file
	if stream.activeFile != nil {
		return s.writePacketToFile(stream, pkt)
	}

	pkt.Close()
	return nil
}

// handleEventMode handles packet processing in Event record mode for a specific stream
func (s *segmenter) handleEventMode(url string, stream *streamState, stopCh <-chan struct{}, pkt gomedia.Packet, isKeyframe bool) error {
	// If we have an active file, handle writing or closing
	if stream.activeFile != nil {
		return s.handleEventModeActiveFile(url, stream, stopCh, pkt, isKeyframe)
	}

	// Buffer mode: accumulate packets in ring buffer
	stream.ringBuf.add(pkt)

	// Trim old packets on keyframe
	if isKeyframe {
		stream.ringBuf.trim()
	}

	// Check if event was triggered and we should start recording
	if !s.eventSaved && isKeyframe {
		return s.startEventRecording(url, stream)
	}

	return nil
}

// handleEventModeActiveFile handles event mode when file is already open for a specific stream
func (s *segmenter) handleEventModeActiveFile(url string, stream *streamState, stopCh <-chan struct{}, pkt gomedia.Packet, isKeyframe bool) error {
	// Check if we should close the file (event saved and enough time passed)
	shouldClose := isKeyframe && s.eventSaved &&
		(time.Since(s.lastEvent) >= s.targetDuration/2 || stream.activeFile.duration >= time.Minute) //nolint:mnd

	if shouldClose {
		if err := s.closeActiveFile(stream, stopCh); err != nil {
			pkt.Close()
			return err
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
			s.recordCurStatusCh <- false
		}
		pkt.Close()
		return nil // Continue to buffer mode in next packet
	}

	// Check if we need to rotate file (duration exceeded but event still active)
	if isKeyframe && !s.eventSaved && stream.activeFile.duration >= s.targetDuration {
		if err := s.closeActiveFile(stream, stopCh); err != nil {
			pkt.Close()
			return err
		}
		if err := s.openNewFile(url, stream, pkt.StartTime()); err != nil {
			pkt.Close()
			return err
		}
	}

	return s.writePacketToFile(stream, pkt)
}

// startEventRecording starts recording when event is triggered for a specific stream
func (s *segmenter) startEventRecording(url string, stream *streamState) error {
	bufferedPkts := stream.ringBuf.drain()
	if len(bufferedPkts) == 0 {
		return nil
	}

	// Find first keyframe in buffer to start file
	startIdx := s.findFirstKeyframe(bufferedPkts)

	// Close packets before the first keyframe — they can't be used
	for i := 0; i < startIdx; i++ {
		bufferedPkts[i].Close()
	}

	if err := s.openNewFile(url, stream, bufferedPkts[startIdx].StartTime()); err != nil {
		return err
	}

	if !s.recordCurStatus {
		s.recordCurStatus = true
		s.recordCurStatusCh <- true
	}

	// Write buffered packets
	for i := startIdx; i < len(bufferedPkts); i++ {
		if err := s.writePacketToFile(stream, bufferedPkts[i]); err != nil {
			// Close remaining packets that won't be written
			for j := i + 1; j < len(bufferedPkts); j++ {
				bufferedPkts[j].Close()
			}
			return err
		}
	}

	s.eventSaved = true
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

// Close_ initiates the closing process for the archiver.
func (s *segmenter) Close_() { //nolint: revive
	const stopGraceTimeout = time.Second * 5
	stopCh := make(chan struct{})
	go func() {
		time.Sleep(stopGraceTimeout)
		close(stopCh)
	}()

	// Close all active files
	s.streamsMu.Lock()
	for _, stream := range s.streams {
		if stream.activeFile != nil {
			_ = s.closeActiveFile(stream, stopCh)
		}
	}
	s.streamsMu.Unlock()

	if s.recordCurStatus {
		s.recordCurStatusCh <- false
	}
	close(s.inpPktCh)
	close(s.outInfoCh)
	close(s.recordCurStatusCh)
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
