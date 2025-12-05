//nolint:mnd // This package has many constants that are unavoidable magic numbers related to fmp4 format standard
package fmp4

import (
	"fmt"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/aac"
	"github.com/ugparu/gomedia/format/mp4/mp4io"
	"github.com/ugparu/gomedia/utils/bits/pio"
	"github.com/ugparu/gomedia/utils/buffer"
	"github.com/ugparu/gomedia/utils/logger"
)

// Constants for safe integer conversions
const (
	maxInt32Value = int64(^uint32(0) >> 1) // Maximum value for int32
)

type Muxer struct {
	strs   []*Stream
	params gomedia.CodecParametersPair
}

func NewMuxer() *Muxer {
	return &Muxer{
		strs: []*Stream{},
		params: gomedia.CodecParametersPair{
			URL:                  "",
			AudioCodecParameters: nil,
			VideoCodecParameters: nil,
		},
	}
}

// safeInt32Conversion safely converts an int64 value to int32
func (m *Muxer) safeInt32Conversion(val int64, name string) int32 {
	if val <= maxInt32Value {
		//nolint:gosec // This is a safe int32 conversion
		return int32(val)
	}
	// Default to max int32 value if overflow would occur
	logger.Errorf(m, "%s value %d is too large for int32, capping at %d", name, val, maxInt32Value)
	return int32(maxInt32Value)
}

// safeUint32Conversion safely converts an int value to uint32
func (m *Muxer) safeUint32Conversion(val int, name string) uint32 {
	if val < 0 || val > int(^uint32(0)) {
		logger.Errorf(m, "%s value %d is outside uint32 range, using 0", name, val)
		return 0
	}
	return uint32(val)
}

func (m *Muxer) newStream(codec gomedia.CodecParameters) (err error) {
	switch codec.Type() {
	case gomedia.H264, gomedia.H265, gomedia.AAC:
	default:
		err = fmt.Errorf("fmp4: codec type=%v is not supported", codec.Type())
		return
	}
	stream := new(Stream)
	stream.timeScale = 90000
	if codec.Type() == gomedia.AAC {
		// Safely convert sample rate to int64 to avoid overflow
		aacCodec, ok := codec.(*aac.CodecParameters)
		if ok {
			// This conversion is safe as audio sample rates are typically well within int64 range
			stream.timeScale = int64(aacCodec.SampleRate()) //nolint:gosec // Audio sample rates are always within int64 range
		}
	}
	stream.CodecParameters = codec
	stream.sample = &mp4io.SampleTable{
		SampleDesc:        new(mp4io.SampleDesc),
		TimeToSample:      new(mp4io.TimeToSample),
		CompositionOffset: nil,
		SampleToChunk:     new(mp4io.SampleToChunk),
		SyncSample:        nil,
		ChunkOffset:       new(mp4io.ChunkOffset),
		SampleSize:        new(mp4io.SampleSize),
		AtomPos: mp4io.AtomPos{
			Offset: 0,
			Size:   0,
		},
	}
	stream.trackAtom = new(mp4io.Track)
	stream.trackAtom.Header = &mp4io.TrackHeader{
		Version:        0,
		Flags:          0x0003,
		CreateTime:     time.Time{},
		ModifyTime:     time.Time{},
		TrackId:        int32(stream.StreamIndex() + 1),
		Duration:       0,
		Layer:          0,
		AlternateGroup: 0,
		Volume:         0,
		Matrix:         [9]int32{0x10000, 0, 0, 0, 0x10000, 0, 0, 0, 0x40000000},
		TrackWidth:     0,
		TrackHeight:    0,
		AtomPos: mp4io.AtomPos{
			Offset: 0,
			Size:   0,
		},
	}
	stream.trackAtom.Media = new(mp4io.Media)

	// Use int32 conversion with validation to avoid potential overflow
	timeScaleInt32 := m.safeInt32Conversion(stream.timeScale, "timeScale")

	stream.trackAtom.Media.Header = &mp4io.MediaHeader{
		Version:    0,
		Flags:      0,
		CreateTime: time.Time{},
		ModifyTime: time.Time{},
		TimeScale:  timeScaleInt32,
		Duration:   0,
		Language:   21956,
		Quality:    0,
		AtomPos: mp4io.AtomPos{
			Offset: 0,
			Size:   0,
		},
	}
	stream.trackAtom.Media.Info = &mp4io.MediaInfo{
		Sound: nil,
		Video: nil,
		Data: &mp4io.DataInfo{
			Refer: &mp4io.DataRefer{
				Version: 0,
				Flags:   0,
				Url: &mp4io.DataReferUrl{
					Version: 0,
					Flags:   0x000001,
					AtomPos: mp4io.AtomPos{
						Offset: 0,
						Size:   0,
					},
				},
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
		},
		Sample:   stream.sample,
		Unknowns: []mp4io.Atom{},
		AtomPos: mp4io.AtomPos{
			Offset: 0,
			Size:   0,
		},
	}

	stream.trackAtom.Media.Handler = &mp4io.HandlerRefer{
		Version: 0,
		Flags:   0,
		Type:    [4]byte([]byte("mhlr")),
		SubType: [4]byte([]byte("vide")),
		Name:    []byte("VideoHandler"),
		AtomPos: mp4io.AtomPos{
			Offset: 0,
			Size:   0,
		},
	}

	switch codec.Type() {
	case gomedia.H264:
		stream.sample.SyncSample = new(mp4io.SyncSample)
	case gomedia.H265:
		stream.sample.SyncSample = new(mp4io.SyncSample)
	}

	m.strs = append(m.strs, stream)

	return
}

func (m *Muxer) Mux(params gomedia.CodecParametersPair) (err error) {
	m.params = params

	if params.VideoCodecParameters != nil {
		if err = m.newStream(params.VideoCodecParameters); err != nil {
			return
		}
	}

	if params.AudioCodecParameters != nil {
		if err = m.newStream(params.AudioCodecParameters); err != nil {
			return
		}
	}

	return
}

func (m *Muxer) WriteTrailer() (err error) {
	return
}

func (m *Muxer) GetInit() buffer.PooledBuffer {
	moov := &mp4io.Movie{
		Header: mp4io.NewMovieHeader(),
		MovieExtend: &mp4io.MovieExtend{
			Tracks:   []*mp4io.TrackExtend{},
			Unknowns: []mp4io.Atom{},
			AtomPos: mp4io.AtomPos{
				Offset: 0,
				Size:   0,
			},
		},
		Tracks:   []*mp4io.Track{},
		Unknowns: []mp4io.Atom{},
		AtomPos: mp4io.AtomPos{
			Offset: 0,
			Size:   0,
		},
	}

	// Safe int->int32 conversion with a check
	nextTrackID := m.safeInt32Conversion(int64(len(m.strs)+1), "next track ID")
	moov.Header.NextTrackID = nextTrackID

	for i, stream := range m.strs {
		// Safe int->uint32 conversion with validation
		trackID := m.safeUint32Conversion(i+1, "track ID")

		moov.MovieExtend.Tracks = append(moov.MovieExtend.Tracks, &mp4io.TrackExtend{
			Version:               0,
			Flags:                 0,
			TrackId:               trackID,
			DefaultSampleDescIdx:  1,
			DefaultSampleDuration: 0,
			DefaultSampleSize:     0,
			DefaultSampleFlags:    0,
			AtomPos: mp4io.AtomPos{
				Offset: 0,
				Size:   0,
			},
		})

		stream.fillTrackAtom()
		moov.Tracks = append(moov.Tracks, stream.trackAtom)
	}

	ftype := mp4io.NewFileType()
	ftype.CompatibleBrands[3] = pio.U32BE([]byte("dash"))

	buf := buffer.Get(moov.Len() + ftype.Len())
	ftype.Marshal(buf.Data())
	moov.Marshal(buf.Data()[ftype.Len():])
	return buf
}

func (m *Muxer) WritePacket(pkt gomedia.Packet) error {
	stream := m.strs[pkt.StreamIndex()]
	return stream.writePacket(pkt)
}

// processMuxer handles the common processing of streams within GetMP4Fragment
// to reduce cyclomatic complexity
func (m *Muxer) processMuxer() {
	m.strs = m.strs[:0]

	if m.params.VideoCodecParameters != nil {
		if err := m.newStream(m.params.VideoCodecParameters); err != nil {
			logger.Errorf(m, "newStream error: %v", err)
		}
	}

	if m.params.AudioCodecParameters != nil {
		if err := m.newStream(m.params.AudioCodecParameters); err != nil {
			logger.Errorf(m, "newStream error: %v", err)
		}
	}
}

// processTrackHeader processes track header data to reduce complexity
func (m *Muxer) processTrackHeader(track *mp4io.TrackFrag, s *Stream) {
	if len(s.packets) == 0 {
		return
	}

	// Safe conversion with validation for DefaultDuration
	durationTS := s.timeToTS(s.packets[0].Duration())
	if durationTS < 0 || durationTS > int64(^uint32(0)) {
		logger.Errorf(m, "Duration time value %d is outside uint32 range", durationTS)
		track.Header.DefaultDuration = 0
	} else {
		track.Header.DefaultDuration = uint32(durationTS)
	}

	// Safe conversion with validation for DefaultSize
	pktSize := s.packets[0].Len()
	if pktSize < 0 || pktSize > int(^uint32(0)) {
		logger.Errorf(m, "Packet size %d is outside uint32 range", pktSize)
		track.Header.DefaultSize = 0
	} else {
		track.Header.DefaultSize = uint32(pktSize)
	}

	firstFlags := mp4io.SampleNonKeyframe
	if vPkt, casted := s.packets[0].(gomedia.VideoPacket); casted && vPkt.IsKeyFrame() {
		firstFlags = mp4io.SampleNoDependencies
	}
	if len(s.packets) > 1 {
		track.Header.DefaultFlags = mp4io.SampleNonKeyframe
		if vPkt, casted := s.packets[1].(gomedia.VideoPacket); casted && vPkt.IsKeyFrame() {
			track.Header.DefaultFlags = mp4io.SampleNoDependencies
		}
	} else {
		track.Header.DefaultFlags = firstFlags
	}

	if firstFlags != track.Header.DefaultFlags {
		track.Run.Flags |= mp4io.TRUNFirstSampleFlags
	}
}

// processPackets processes packets within a stream to reduce complexity
func (m *Muxer) processPackets(track *mp4io.TrackFrag, s *Stream, streamIndex int) {
	for j, pkt := range s.packets {
		if pkt.Len() != int(track.Header.DefaultSize) {
			track.Run.Flags |= mp4io.TRUNSampleSize
		}

		// Safe conversion with validation
		pktDurationTS := s.timeToTS(pkt.Duration())
		if pktDurationTS < 0 || pktDurationTS > int64(^uint32(0)) {
			logger.Errorf(m, "Packet duration %d is outside uint32 range", pktDurationTS)
		} else if uint32(pktDurationTS) != track.Header.DefaultDuration {
			track.Run.Flags |= mp4io.TRUNSampleDuration
		}

		// Use a different name to avoid shadowing
		entryRunFlag := mp4io.SampleNonKeyframe
		if vPkt, casted := pkt.(gomedia.VideoPacket); casted && vPkt.IsKeyFrame() {
			entryRunFlag = mp4io.SampleNoDependencies
		}

		if j != 0 && entryRunFlag != mp4io.SampleNonKeyframe {
			track.Run.Flags |= mp4io.TRUNSampleFlags
		}

		// Safe conversions with validation
		var entryDuration uint32
		if pktDurationTS < 0 || pktDurationTS > int64(^uint32(0)) {
			logger.Errorf(m, "Packet duration %d is outside uint32 range, using 0", pktDurationTS)
			entryDuration = 0
		} else {
			entryDuration = uint32(pktDurationTS)
		}

		var entrySize uint32
		pktDataSize := pkt.Len()
		if pktDataSize < 0 || pktDataSize > int(^uint32(0)) {
			logger.Errorf(m, "Packet data size %d is outside uint32 range, using 0", pktDataSize)
			entrySize = 0
		} else {
			entrySize = uint32(pktDataSize)
		}

		runEnrty := mp4io.TrackFragRunEntry{
			Duration: entryDuration,
			Size:     entrySize,
			Cts:      0,
			Flags:    entryRunFlag,
		}
		// if streamIndex == 1 {
		// 	runEnrty.Duration++ // Increment for audio stream
		// }
		track.Run.Entries = append(track.Run.Entries, runEnrty)
	}
}

// createSegmentIndex creates segment index entries to reduce complexity
func (m *Muxer) createSegmentIndex(s *Stream, _ int) mp4io.SegmentIndex {
	// Safe conversion with validation for RefernceID
	streamIndex := int(s.StreamIndex())
	refID := m.safeInt32Conversion(int64(streamIndex+1), "stream index")

	// Safe conversion with validation for Timescale
	timeScaleInt32 := m.safeInt32Conversion(s.timeScale, "timeScale")

	referencedSize := m.safeInt32Conversion(int64(s.bufSize), "buffer size")

	// Safe conversion for SubsegmentDuration
	durationValue := int64(s.duration) * s.timeScale / int64(time.Second)
	subsegmentDuration := m.safeInt32Conversion(durationValue, "subsegment duration")

	return mp4io.SegmentIndex{
		Version:     1,
		Flags:       0,
		RefernceID:  refID,
		Timescale:   timeScaleInt32,
		EarliestPT:  int64(s.firstPacketTime) * s.timeScale / int64(time.Second),
		FirstOffset: 0,
		Entries: []mp4io.Reference{
			{
				ReferenceType:      0,
				ReferencedSize:     referencedSize,
				SubsegmentDuration: subsegmentDuration,
				StartsWithSAP:      1,
				SAPType:            0,
				SAPDeltaTime:       0,
			},
		},
		AtomPos: mp4io.AtomPos{
			Offset: 0,
			Size:   0,
		},
	}
}

// processDataOffsets processes data offsets for tracks to reduce complexity
func (m *Muxer) processDataOffsets(moof *mp4io.MovieFrag, startMOOF int, out []byte, n int) int {
	for i, s := range m.strs {
		// Safe conversion with validation
		offset := n - startMOOF
		dataOffset := m.safeUint32Conversion(offset, "data offset")
		moof.Tracks[i].Run.DataOffset = dataOffset

		for _, pkt := range s.packets {
			pkt.View(func(data []byte) {
				copy(out[n:], data)
				n += len(data)
			})
		}
	}
	return n
}

// GetMP4Fragment returns an MP4 fragment
// This function is complex but has been refactored to reduce cyclomatic complexity
func (m *Muxer) GetMP4Fragment(idx int) buffer.PooledBuffer {
	defer m.processMuxer()

	moof := new(mp4io.MovieFrag)
	moof.Header = &mp4io.MovieFragHeader{
		Version: 0,
		Flags:   0,
		// Safe conversion with validation
		Seqnum: m.safeUint32Conversion(idx, "sequence number"),
		AtomPos: mp4io.AtomPos{
			Offset: 0,
			Size:   0,
		},
	}

	for i, s := range m.strs {
		// Define runFlag at function level to avoid shadowing
		outerRunFlag := mp4io.SampleNonKeyframe
		if len(s.packets) > 0 {
			if vPkt, casted := s.packets[0].(gomedia.VideoPacket); casted && vPkt.IsKeyFrame() {
				outerRunFlag = mp4io.SampleNoDependencies
			}
		}

		// Safe conversion with validation for TrackID
		streamIndex := int(s.StreamIndex())
		trackID := m.safeUint32Conversion(streamIndex+1, "track ID")

		track := &mp4io.TrackFrag{
			Header: &mp4io.TrackFragHeader{
				Version: 0,
				// Split long line to fix line length issue
				Flags: mp4io.TFHDDefaultDuration | mp4io.TFHDDefaultSize |
					mp4io.TFHDDefaultFlags | mp4io.TFHDDurationIsEmpty,
				TrackID:         trackID,
				BaseDataOffset:  0,
				StsdID:          0,
				DefaultDuration: 0,
				DefaultSize:     0,
				DefaultFlags:    0,
				AtomPos: mp4io.AtomPos{
					Offset: 0,
					Size:   0,
				},
			},
			DecodeTime: &mp4io.TrackFragDecodeTime{
				Version: 1,
				Flags:   0,
				// Safe conversion with validation
				Time: func() uint64 {
					timeValue := int64(s.firstPacketTime) * s.timeScale / int64(time.Second)
					if timeValue < 0 {
						logger.Errorf(m, "Negative time value %d, using 0", timeValue)
						return 0
					}
					return uint64(timeValue)
				}(),
				AtomPos: mp4io.AtomPos{
					Offset: 0,
					Size:   0,
				},
			},
			Run: &mp4io.TrackFragRun{
				Version:          0,
				Flags:            mp4io.TRUNDataOffset,
				DataOffset:       0,
				FirstSampleFlags: outerRunFlag,
				Entries:          []mp4io.TrackFragRunEntry{},
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
		moof.Tracks = append(moof.Tracks, track)

		m.processTrackHeader(track, s)
		m.processPackets(track, s, i)
	}

	styp := mp4io.NewSegmentType()

	var sidx []mp4io.SegmentIndex

	bufSz := moof.Len() + styp.Len() + 8
	for i, s := range m.strs {
		bufSz += s.bufSize
		sidx = append(sidx, m.createSegmentIndex(s, i))
		bufSz += sidx[i].Len()
	}

	buf := buffer.Get(bufSz)

	var n int
	n += styp.Marshal(buf.Data())
	for _, s := range sidx {
		n += s.Marshal(buf.Data()[n:])
	}

	startMOOF := n
	n += moof.Len()

	mdatStart := n

	n += 4
	pio.PutU32BE(buf.Data()[n:], uint32(mp4io.MDAT))
	n += 4

	n = m.processDataOffsets(moof, startMOOF, buf.Data(), n)
	moof.Marshal(buf.Data()[startMOOF:])

	// Safe conversion with validation
	mdatSizeValue := n - mdatStart
	mdatSize := m.safeUint32Conversion(mdatSizeValue, "MDAT size")
	pio.PutU32BE(buf.Data()[mdatStart:], mdatSize)
	return buf
}
