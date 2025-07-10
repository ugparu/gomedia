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
	packets         []gomedia.Packet
	firstPacketTime time.Duration
	trackAtom       *mp4io.Track
	timeScale       int64
	duration        time.Duration
	sample          *mp4io.SampleTable
	sampleIndex     int
	buffer          []byte
}

func (s *Stream) timeToTS(tm time.Duration) int64 {
	return int64(tm * time.Duration(s.timeScale) / time.Second)
}

// safeInt16Conversion safely converts a uint value to int16 with validation
func (s *Stream) safeInt16Conversion(val uint, name string) int16 {
	if val <= uint(maxInt16Value) {
		return int16(val)
	}
	// Default to max int16 value if overflow would occur
	logger.Errorf(s, "%s value %d is too large for int16, capping at %d", name, val, maxInt16Value)
	return maxInt16Value
}

// safeInt32Conversion safely converts an int64 value to int32
func (s *Stream) safeInt32Conversion(val int64, name string) int32 {
	maxSafeInt32 := int64(^uint32(0) >> 1) // Maximum value for int32
	if val <= maxSafeInt32 {
		//nolint:gosec // This is a safe int32 conversion
		return int32(val)
	}
	// Default to max int32 value if overflow would occur
	logger.Errorf(s, "%s value %d is too large for int32, capping at %d", name, val, maxSafeInt32)
	return int32(maxSafeInt32)
}

func (s *Stream) fillTrackAtom() {
	// Safe conversion with validation for TimeScale
	timeScaleInt32 := s.safeInt32Conversion(s.timeScale, "timeScale")
	s.trackAtom.Media.Header.TimeScale = timeScaleInt32

	// Safe conversion with validation for Duration
	durationNs := int64(s.duration)
	durationInt32 := s.safeInt32Conversion(durationNs, "duration")
	s.trackAtom.Media.Header.Duration = durationInt32

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
			VorizontalResolution: defaultDPI,
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
			VorizontalResolution: defaultDPI,
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
	case *aac.CodecParameters:
		// Safe conversions with validation
		var channelsInt16 int16
		channelCount := codecPar.ChannelLayout().Count()
		if channelCount >= 0 && channelCount <= maxInt16Value {
			channelsInt16 = int16(channelCount)
		} else {
			channelsInt16 = maxInt16Value
			logger.Errorf(s, "channel count %d is too large for int16, capping at %d", channelCount, channelsInt16)
		}

		var sampleSizeInt16 int16
		bytesPerSample := codecPar.SampleFormat().BytesPerSample() * bytesPerSampleScale
		if bytesPerSample >= 0 && bytesPerSample <= maxInt16Value {
			sampleSizeInt16 = int16(bytesPerSample)
		} else {
			sampleSizeInt16 = maxInt16Value
			logger.Errorf(s, "sample size %d is too large for int16, capping at %d", bytesPerSample, sampleSizeInt16)
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
				DecConfig:  codecPar.MPEG4AudioConfigBytes(),
				TrackId:    uint16(s.StreamIndex()) + 1,
				MaxBitrate: 128000, // Default AAC bitrate
				AvgBitrate: 128000,
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

		s.trackAtom.Header.Volume = 1
		s.trackAtom.Header.AlternateGroup = 1
		s.trackAtom.Media.Handler = &mp4io.HandlerRefer{
			Version:     0,
			Flags:       0,
			HandlerType: [4]byte{'s', 'o', 'u', 'n'},
			Reserved:    [3]uint32{0, 0, 0},
			Name:        []byte("SoundHandler"),
			AtomPos: mp4io.AtomPos{
				Offset: 0,
				Size:   0,
			},
		}
		s.trackAtom.Media.Info.Sound = new(mp4io.SoundMediaInfo)
	default:
		logger.Errorf(s, "unsupported codec type %T", codecPar)
	}
}

func (s *Stream) writePacket(pkt gomedia.Packet) error {
	if s.sampleIndex == 0 {
		s.firstPacketTime = pkt.Timestamp()
	}
	s.packets = append(s.packets, pkt)
	s.duration += pkt.Duration()
	s.buffer = append(s.buffer, pkt.Data()...)
	s.sampleIndex++

	return nil
}
