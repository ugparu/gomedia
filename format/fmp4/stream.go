package fmp4

import (
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/aac"
	"github.com/ugparu/gomedia/codec/h264"
	"github.com/ugparu/gomedia/codec/h265"
	"github.com/ugparu/gomedia/format/mp4/mp4io"
	"github.com/ugparu/gomedia/utils/logger"
)

// Constants to avoid magic numbers
const (
	defaultDPI          = 72
	defaultDepth        = 24
	maxInt16Value       = 32767 // Maximum value for int16
	bytesPerSampleScale = 4     // Used in SampleSize calculation
)

type Stream struct {
	gomedia.CodecParameters
	log             logger.Logger
	packets         []gomedia.Packet
	bufSize         int
	firstPacketTime time.Duration
	trackAtom       *mp4io.Track
	timeScale       int64
	duration        time.Duration
	sample          *mp4io.SampleTable
	sampleIndex     int
}

func (s *Stream) timeToTS(tm time.Duration) int64 {
	// Split into seconds and remainder to avoid int64 overflow on large durations
	sec := tm / time.Second
	rem := tm % time.Second
	return int64(sec)*s.timeScale + int64(rem)*s.timeScale/int64(time.Second)
}

// safeInt16Conversion safely converts a uint value to int16 with validation
func (s *Stream) safeInt16Conversion(val uint, name string) int16 {
	if val <= uint(maxInt16Value) {
		return int16(val)
	}
	// Default to max int16 value if overflow would occur
	s.log.Errorf(s, "%s value %d is too large for int16, capping at %d", name, val, maxInt16Value)
	return maxInt16Value
}

func (s *Stream) fillTrackAtom() {
	// Safe conversion with validation for TimeScale
	timeScaleInt32 := safeInt32Conversion(s.log, s, s.timeScale, "timeScale")
	s.trackAtom.Media.Header.TimeScale = timeScaleInt32

	s.trackAtom.Media.Header.Duration = s.timeToTS(s.duration)

	// Customize settings based on the codec type
	switch codecPar := s.CodecParameters.(type) {
	case *h264.CodecParameters:
		width, height := codecPar.Width(), codecPar.Height()
		widthInt16 := s.safeInt16Conversion(width, "width")
		heightInt16 := s.safeInt16Conversion(height, "height")

		s.sample.SampleDesc.AVC1Desc = &mp4io.AVC1Desc{
			DataRefIdx:           1,
			Version:              0,
			Revision:             0,
			Vendor:               0,
			TemporalQuality:      0,
			SpatialQuality:       0,
			Width:                widthInt16,
			Height:               heightInt16,
			HorizontalResolution: defaultDPI,
			VerticalResolution:   defaultDPI,
			FrameCount:           1,
			CompressorName:       [32]byte{},
			Depth:                defaultDepth,
			ColorTableId:         -1,
			Conf: &mp4io.AVC1Conf{
				Data: codecPar.AVCDecoderConfRecordBytes(),
				AtomPos: mp4io.AtomPos{
					Offset: 0,
					Size:   0,
				},
			},
			Unknowns: []mp4io.Atom{},
			AtomPos: mp4io.AtomPos{
				Offset: 0,
				Size:   0,
			},
		}
		s.trackAtom.Media.Info.Video = new(mp4io.VideoMediaInfo)
	case *h265.CodecParameters:
		width, height := codecPar.Width(), codecPar.Height()
		widthInt16 := s.safeInt16Conversion(width, "width")
		heightInt16 := s.safeInt16Conversion(height, "height")

		s.sample.SampleDesc.HV1Desc = &mp4io.HV1Desc{
			DataRefIdx:           1,
			Version:              0,
			Revision:             0,
			Vendor:               0,
			TemporalQuality:      0,
			SpatialQuality:       0,
			Width:                widthInt16,
			Height:               heightInt16,
			HorizontalResolution: defaultDPI,
			VerticalResolution:   defaultDPI,
			FrameCount:           1,
			CompressorName:       [32]byte{},
			Depth:                defaultDepth,
			ColorTableId:         -1,
			Conf: &mp4io.HV1Conf{
				Data: codecPar.AVCDecoderConfRecordBytes(),
				AtomPos: mp4io.AtomPos{
					Offset: 0,
					Size:   0,
				},
			},
			Unknowns: []mp4io.Atom{},
			AtomPos: mp4io.AtomPos{
				Offset: 0,
				Size:   0,
			},
		}
		s.trackAtom.Media.Info.Video = new(mp4io.VideoMediaInfo)
	case *aac.CodecParameters:
		// Safe conversions with validation
		var channelsInt16 int16
		channelCount := codecPar.ChannelLayout().Count()
		if channelCount >= 0 && channelCount <= maxInt16Value {
			channelsInt16 = int16(channelCount)
		} else {
			channelsInt16 = maxInt16Value
			s.log.Errorf(s, "channel count %d is too large for int16, capping at %d", channelCount, channelsInt16)
		}

		var sampleSizeInt16 int16
		bytesPerSample := codecPar.SampleFormat().BytesPerSample() * bytesPerSampleScale
		if bytesPerSample >= 0 && bytesPerSample <= maxInt16Value {
			sampleSizeInt16 = int16(bytesPerSample)
		} else {
			sampleSizeInt16 = maxInt16Value
			s.log.Errorf(s, "sample size %d is too large for int16, capping at %d", bytesPerSample, sampleSizeInt16)
		}

		s.sample.SampleDesc.MP4ADesc = &mp4io.MP4ADesc{
			DataRefIdx:       1,
			Version:          0,
			RevisionLevel:    0,
			Vendor:           0,
			NumberOfChannels: channelsInt16,
			SampleSize:       sampleSizeInt16,
			CompressionId:    0,
			SampleRate:       float64(codecPar.SampleRate()),
			Conf: &mp4io.ElemStreamDesc{
				DecConfig: codecPar.MPEG4AudioConfigBytes(),
				TrackId:   uint16(s.StreamIndex()) + 1,
				AtomPos: mp4io.AtomPos{
					Offset: 0,
					Size:   0,
				},
			},
			Unknowns: []mp4io.Atom{},
			AtomPos: mp4io.AtomPos{
				Offset: 0,
				Size:   0,
			},
		}

		s.trackAtom.Media.Info.Sound = new(mp4io.SoundMediaInfo)
	default:
		s.log.Errorf(s, "unsupported codec type %T", codecPar)
	}
}

func (s *Stream) writePacket(pkt gomedia.Packet) error {
	if s.sampleIndex == 0 {
		s.firstPacketTime = pkt.Timestamp()
	}
	s.packets = append(s.packets, pkt)
	s.duration += pkt.Duration()
	s.sampleIndex++
	s.bufSize += pkt.Len()
	return nil
}

func (s *Stream) Close() {
}
