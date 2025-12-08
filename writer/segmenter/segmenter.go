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
		rb.packets = rb.packets[trimIdx:]
		rb.duration -= trimDur
	}
}

func (rb *ringBuffer) clear() {
	rb.packets = rb.packets[:0]
	rb.duration = 0
}

func (rb *ringBuffer) drain() []gomedia.Packet {
	pkts := rb.packets
	rb.packets = make([]gomedia.Packet, 0)
	rb.duration = 0
	return pkts
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
	codecPar          gomedia.CodecParametersPair
	rmSrcCh           chan string

	// Active file being written (for Always mode or after event triggered)
	activeFile *activeFile

	// Ring buffer for Event mode pre-buffering
	ringBuf *ringBuffer

	// Track if we've received a keyframe yet
	seenKeyframe bool
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
		codecPar:          gomedia.CodecParametersPair{URL: "", AudioCodecParameters: nil, VideoCodecParameters: nil},
		rmSrcCh:           make(chan string, chanSize),
		activeFile:        nil,
		ringBuf:           newRingBuffer(segSize / 2), //nolint:mnd
		seenKeyframe:      false,
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

// openNewFile opens a new MP4 file for writing
func (s *segmenter) openNewFile(startTime time.Time) error {
	if s.codecPar.VideoCodecParameters == nil {
		return errors.New("cannot open file with nil video codec parameters")
	}

	folder := fmt.Sprintf("%d/%d/%d/", startTime.Year(), startTime.Month(), startTime.Day())
	name := startTime.Format("2006-01-02T15:04:05") + ".mp4"
	filename := fmt.Sprintf("%s%s%s", s.dest, folder, name)

	f, err := createFile(filename)
	if err != nil {
		return err
	}

	muxer := mp4.NewMuxer(f)
	if err = muxer.Mux(s.codecPar); err != nil {
		_ = f.Close()
		return err
	}

	s.activeFile = &activeFile{
		file:      f,
		muxer:     muxer,
		startTime: startTime,
		folder:    folder,
		name:      name,
		duration:  0,
	}

	return nil
}

// closeActiveFile closes the current file and emits file info
func (s *segmenter) closeActiveFile(stopChan <-chan struct{}) error {
	if s.activeFile == nil {
		return nil
	}

	af := s.activeFile
	s.activeFile = nil

	if err := af.muxer.WriteTrailer(); err != nil {
		_ = af.file.Close()
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

		fi, err := af.file.Stat()
		if err != nil {
			_ = af.file.Close()
			return
		}

		select {
		case s.outInfoCh <- gomedia.FileInfo{
			Name:  af.folder + af.name,
			Start: af.startTime,
			Stop:  af.startTime.Add(af.duration),
			Size:  int(fi.Size()),
		}:
		case <-stopChan:
			return
		}

		select {
		case <-waitDone:
		case <-time.After(af.duration * 3):
			logger.Warningf(nil, "timeout waiting for packets to close for file %s (waited %v), closing anyway",
				af.name, af.duration*3)
		}

		_ = af.file.Close()
	}(af)

	return nil
}

// writePacketToFile writes a packet directly to the active file
func (s *segmenter) writePacketToFile(pkt gomedia.Packet) error {
	if s.activeFile == nil {
		return errors.New("no active file")
	}

	af := s.activeFile

	// Get the pre-last packet (the one that will be written)
	preLastPkt := af.muxer.GetPreLastPacket(pkt.StreamIndex())

	if err := af.muxer.WritePacket(pkt); err != nil {
		return err
	}

	// Switch pre-last packet to file if it was written
	if preLastPkt != nil {
		// Get the actual packet data offset and size (excluding SPS/PPS/VPS for keyframes)
		dataOffset, dataSize := af.muxer.GetLastPacketDataInfo(pkt.StreamIndex())
		if dataSize > 0 {
			af.wg.Add(1)
			done := func() error {
				af.wg.Done()
				return nil
			}
			if err := preLastPkt.SwitchToFile(af.file, dataOffset, dataSize, done); err != nil {
				af.wg.Done() // Ensure WaitGroup is decremented on error
				return err
			}
		}
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
			// Close current file and clear buffers
			if s.activeFile != nil {
				err = s.closeActiveFile(stopCh)
			}
			s.ringBuf.clear()
			s.recordMode = recordMode
			s.seenKeyframe = false
			if s.recordCurStatus {
				s.recordCurStatus = false
				s.recordCurStatusCh <- false
			}
		}

	case <-s.rmSrcCh:
		// Handle source removal - close current file
		if s.activeFile != nil {
			err = s.closeActiveFile(stopCh)
		}
		s.ringBuf.clear()
		s.seenKeyframe = false

	case <-s.eventCh:
		s.eventSaved = false
		s.lastEvent = time.Now()

	case inpPkt := <-s.inpPktCh:
		if s.recordMode == gomedia.Never {
			return errors.New("attempt to process packet with never record mode")
		}

		if inpPkt == nil {
			return &utils.NilPacketError{}
		}
		defer func() {
			inpPkt.Close()
		}()

		// Update codec parameters if changed
		if vPkt, ok := inpPkt.(gomedia.VideoPacket); ok && vPkt.CodecParameters() != s.codecPar.VideoCodecParameters {
			s.codecPar.VideoCodecParameters = vPkt.CodecParameters()
			if err = s.closeActiveFile(s.Done()); err != nil {
				return
			}
		}
		if aPkt, ok := inpPkt.(gomedia.AudioPacket); ok && aPkt.CodecParameters() != s.codecPar.AudioCodecParameters {
			s.codecPar.AudioCodecParameters = aPkt.CodecParameters()
			if err = s.closeActiveFile(s.Done()); err != nil {
				return
			}
		}

		// Wait for first keyframe
		vPkt, isVideo := inpPkt.(gomedia.VideoPacket)
		isKeyframe := isVideo && vPkt.IsKeyFrame()

		if !s.seenKeyframe && !isKeyframe {
			return nil
		}
		if isKeyframe {
			s.seenKeyframe = true
		}

		// Handle based on record mode
		switch s.recordMode {
		case gomedia.Always:
			err = s.handleAlwaysMode(stopCh, inpPkt, isKeyframe)
		case gomedia.Event:
			err = s.handleEventMode(stopCh, inpPkt, isKeyframe)
		case gomedia.Never:
			// Should not reach here due to earlier check
		}
	}
	return
}

// handleAlwaysMode handles packet processing in Always record mode
func (s *segmenter) handleAlwaysMode(stopCh <-chan struct{}, pkt gomedia.Packet, isKeyframe bool) error {
	// Check if we need to rotate file (on keyframe when duration exceeded)
	if isKeyframe && s.activeFile != nil && s.activeFile.duration >= s.targetDuration {
		if err := s.closeActiveFile(stopCh); err != nil {
			return err
		}
	}

	// Open new file if needed (on keyframe)
	if s.activeFile == nil && isKeyframe {
		if err := s.openNewFile(pkt.StartTime()); err != nil {
			return err
		}
		// Update recording status
		if !s.recordCurStatus {
			s.recordCurStatus = true
			s.recordCurStatusCh <- true
		}
	}

	// Write packet if we have an active file
	if s.activeFile != nil {
		return s.writePacketToFile(pkt)
	}

	return nil
}

// handleEventMode handles packet processing in Event record mode
func (s *segmenter) handleEventMode(stopCh <-chan struct{}, pkt gomedia.Packet, isKeyframe bool) error {
	// If we have an active file, handle writing or closing
	if s.activeFile != nil {
		return s.handleEventModeActiveFile(stopCh, pkt, isKeyframe)
	}

	// Buffer mode: accumulate packets in ring buffer
	s.ringBuf.add(pkt)

	// Trim old packets on keyframe
	if isKeyframe {
		s.ringBuf.trim()
	}

	// Check if event was triggered and we should start recording
	if !s.eventSaved && isKeyframe {
		return s.startEventRecording()
	}

	return nil
}

// handleEventModeActiveFile handles event mode when file is already open
func (s *segmenter) handleEventModeActiveFile(stopCh <-chan struct{}, pkt gomedia.Packet, isKeyframe bool) error {
	// Check if we should close the file (event saved and enough time passed)
	shouldClose := isKeyframe && s.eventSaved &&
		(time.Since(s.lastEvent) >= s.targetDuration/2 || s.activeFile.duration >= time.Minute) //nolint:mnd

	if shouldClose {
		if err := s.closeActiveFile(stopCh); err != nil {
			return err
		}
		s.recordCurStatus = false
		s.recordCurStatusCh <- false
		return nil // Continue to buffer mode in next packet
	}

	// Check if we need to rotate file (duration exceeded but event still active)
	if isKeyframe && !s.eventSaved && s.activeFile.duration >= s.targetDuration {
		if err := s.closeActiveFile(stopCh); err != nil {
			return err
		}
		if err := s.openNewFile(pkt.StartTime()); err != nil {
			return err
		}
	}

	return s.writePacketToFile(pkt)
}

// startEventRecording starts recording when event is triggered
func (s *segmenter) startEventRecording() error {
	bufferedPkts := s.ringBuf.drain()
	if len(bufferedPkts) == 0 {
		return nil
	}

	// Find first keyframe in buffer to start file
	startIdx := s.findFirstKeyframe(bufferedPkts)

	if err := s.openNewFile(bufferedPkts[startIdx].StartTime()); err != nil {
		return err
	}

	if !s.recordCurStatus {
		s.recordCurStatus = true
		s.recordCurStatusCh <- true
	}

	// Write buffered packets
	for i := startIdx; i < len(bufferedPkts); i++ {
		if err := s.writePacketToFile(bufferedPkts[i]); err != nil {
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

	// Close active file if any
	if s.activeFile != nil {
		_ = s.closeActiveFile(stopCh)
	}

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
