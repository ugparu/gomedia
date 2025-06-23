package segmenter

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/format/mp4"
	"github.com/ugparu/gomedia/utils"
	"github.com/ugparu/gomedia/utils/lifecycle"
)

type segment struct {
	packets  []gomedia.Packet
	duration time.Duration
}

func (s *segment) add(pkt gomedia.Packet) {
	s.packets = append(s.packets, pkt)
	s.duration += pkt.Duration()
}

type segmenter struct {
	lifecycle.AsyncManager[*segmenter]
	recordMode     gomedia.RecordMode
	duration       time.Duration
	targetDuration time.Duration
	recordModeCh   chan gomedia.RecordMode
	eventCh        chan struct{}
	inpPktCh       chan gomedia.Packet
	outInfoCh      chan gomedia.FileInfo
	lastEvent      time.Time
	eventSaved     bool
	curSegment     *segment
	dest           string
	codecPar       gomedia.CodecParametersPair
	segments       []*segment
	rmSrcCh        chan string
}

// New creates a new instance of the archiver with the specified parameters.
func New(dest string, segSize time.Duration, recordMode gomedia.RecordMode, chanSize int) gomedia.Segmenter {
	newArch := &segmenter{
		AsyncManager:   nil,
		recordMode:     recordMode,
		duration:       0,
		targetDuration: segSize,
		recordModeCh:   make(chan gomedia.RecordMode, chanSize),
		eventCh:        make(chan struct{}, chanSize),
		inpPktCh:       make(chan gomedia.Packet, chanSize),
		outInfoCh:      make(chan gomedia.FileInfo, chanSize),
		lastEvent:      time.Now(),
		eventSaved:     true,
		curSegment:     nil,
		dest:           dest,
		codecPar:       gomedia.CodecParametersPair{AudioCodecParameters: nil, VideoCodecParameters: nil},
		segments:       []*segment{},
		rmSrcCh:        make(chan string, chanSize),
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
	if err = os.MkdirAll(dir, os.ModePerm); err != nil {
		return nil, err
	}

	// Try creating the file again
	return os.Create(path)
}

func (s *segmenter) dumpToFile(stopChan <-chan struct{}) (err error) {
	defer func() {
		s.duration = 0
		s.segments = s.segments[:0]
		s.curSegment = nil
	}()
	if len(s.segments) == 0 {
		return nil
	}
	if s.codecPar.VideoCodecParameters == nil {
		return errors.New("can not save segment with nil video codec parameters")
	}

	folder := fmt.Sprintf("%d/%d/%d/", s.segments[0].packets[0].StartTime().Year(),
		s.segments[0].packets[0].StartTime().Month(), s.segments[0].packets[0].StartTime().Day())
	name := s.segments[0].packets[0].StartTime().Format("2006-01-02T15:04:05") + ".mp4"
	filename := fmt.Sprintf("%s%s%s", s.dest, folder, name)

	f, err := createFile(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	muxer := mp4.NewMuxer(f)
	if err = muxer.Mux(s.codecPar); err != nil {
		return err
	}

	for _, s := range s.segments {
		for _, p := range s.packets {
			if err = muxer.WritePacket(p); err != nil {
				return err
			}
		}
	}
	if err = muxer.WriteTrailer(); err != nil {
		return err
	}

	fi, err := f.Stat()
	if err != nil {
		return err
	}

	lastPacket := s.curSegment.packets[len(s.curSegment.packets)-1]
	select {
	case s.outInfoCh <- gomedia.FileInfo{
		Name:  folder + name,
		Start: s.segments[0].packets[0].StartTime(),
		Stop:  lastPacket.StartTime().Add(lastPacket.Duration()),
		Size:  int(fi.Size()),
	}:
	case <-stopChan:
		return &lifecycle.BreakError{}
	}

	return
}

// Write initiates the writing process for the archiver based on the provided codec parameters.
// It uses the Start function with a startFunc that updates codec parameters.
func (s *segmenter) Write() {
	startFunc := func(*segmenter) error {
		return nil
	}
	_ = s.Start(startFunc)
}

// Step processes a single step in the archiving pipeline based on the provided channels and parameters.
// It listens for updates in record mode, codec parameters, and input packets to make appropriate actions.
// It returns an error if any issues occur during the processing.
func (s *segmenter) Step(stopCh <-chan struct{}) (err error) {
	select {
	case <-stopCh:
		return &lifecycle.BreakError{}
	case recordMode := <-s.recordModeCh:
		if recordMode != s.recordMode {
			err = s.dumpToFile(stopCh)
			s.recordMode = recordMode
		}
	case <-s.rmSrcCh:
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

		if vPkt, ok := inpPkt.(gomedia.VideoPacket); ok && vPkt.CodecParameters() != s.codecPar.VideoCodecParameters {
			s.codecPar.VideoCodecParameters = vPkt.CodecParameters()
		}

		if aPkt, ok := inpPkt.(gomedia.AudioPacket); ok && aPkt.CodecParameters() != s.codecPar.AudioCodecParameters {
			s.codecPar.AudioCodecParameters = aPkt.CodecParameters()
		}

		if vPkt, ok := inpPkt.(gomedia.VideoPacket); s.curSegment == nil && (!ok || !vPkt.IsKeyFrame()) {
			return nil
		}

		if vPkt, ok := inpPkt.(gomedia.VideoPacket); !ok || !vPkt.IsKeyFrame() {
			s.curSegment.add(inpPkt)
			if ok {
				s.duration += inpPkt.Duration()
			}
			return nil
		}

		if s.recordMode == gomedia.Always && s.duration >= s.targetDuration ||
			s.recordMode == gomedia.Event && !s.eventSaved &&
				(time.Since(s.lastEvent) >= s.targetDuration/2 || s.duration >= time.Minute) {
			err = s.dumpToFile(stopCh)
			s.eventSaved = true
		}
		if s.recordMode == gomedia.Event && s.eventSaved &&
			len(s.segments) > 0 && s.duration-s.segments[0].duration >= s.targetDuration/2 {
			s.duration -= s.segments[0].duration
			s.segments = s.segments[1:]
		}

		s.curSegment = &segment{
			packets: []gomedia.Packet{},
		}
		s.segments = append(s.segments, s.curSegment)
		s.curSegment.add(inpPkt)
		s.duration += inpPkt.Duration()
	}
	return
}

// Close_ initiates the closing process for the archiver.
// It closes the necessary channels and performs a final dump to the file.
func (s *segmenter) Close_() { //nolint: revive
	const stopGraceTimeout = time.Second * 5
	stopCh := make(chan struct{})
	go func() {
		time.Sleep(stopGraceTimeout)
		close(stopCh)
	}()
	_ = s.dumpToFile(stopCh)
	close(s.inpPktCh)
	close(s.outInfoCh)
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
