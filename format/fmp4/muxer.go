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

const (
	maxInt32Value = int64(^uint32(0) >> 1)
	minInt32Value = -maxInt32Value - 1
)

// safeInt32Conversion clamps val to the int32 range, logging an error when saturation happens.
func safeInt32Conversion(log logger.Logger, src any, val int64, name string) int32 {
	if val >= minInt32Value && val <= maxInt32Value {
		//nolint:gosec // This is a safe int32 conversion
		return int32(val)
	}
	if val < minInt32Value {
		log.Errorf(src, "%s value %d is too small for int32, capping at %d", name, val, minInt32Value)
		return int32(minInt32Value)
	}
	log.Errorf(src, "%s value %d is too large for int32, capping at %d", name, val, maxInt32Value)
	return int32(maxInt32Value)
}

type Muxer struct {
	strs   []*Stream
	params gomedia.CodecParametersPair
	log    logger.Logger
}

func NewMuxer(log logger.Logger) *Muxer {
	return &Muxer{
		strs: []*Stream{},
		params: gomedia.CodecParametersPair{
			SourceID:             "",
			AudioCodecParameters: nil,
			VideoCodecParameters: nil,
		},
		log: log,
	}
}

// safeUint32Conversion clamps val to the uint32 range, logging when out-of-range inputs are replaced with 0.
func (m *Muxer) safeUint32Conversion(val int, name string) uint32 {
	if val < 0 || val > int(^uint32(0)) {
		m.log.Errorf(m, "%s value %d is outside uint32 range, using 0", name, val)
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
	stream.log = m.log
	stream.timeScale = 90000
	if codec.Type() == gomedia.AAC {
		// MP4 track timescale must match the audio sample rate so CTS/DTS are in sample units.
		if aacCodec, ok := codec.(*aac.CodecParameters); ok {
			stream.timeScale = int64(aacCodec.SampleRate()) //nolint:gosec // sample rate always fits in int64
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

	timeScaleInt32 := safeInt32Conversion(m.log, m, stream.timeScale, "timeScale")

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

	switch codec.Type() {
	case gomedia.H264, gomedia.H265:
		stream.trackAtom.Media.Handler = &mp4io.HandlerRefer{
			Version: 0,
			Flags:   0,
			Type:    [4]byte{},
			SubType: [4]byte([]byte("vide")),
			Name:    []byte("VideoHandler"),
			AtomPos: mp4io.AtomPos{
				Offset: 0,
				Size:   0,
			},
		}
		stream.sample.SyncSample = new(mp4io.SyncSample)
	case gomedia.AAC:
		stream.trackAtom.Header.Volume = 1
		stream.trackAtom.Header.AlternateGroup = 1
		stream.trackAtom.Media.Handler = &mp4io.HandlerRefer{
			Version: 0,
			Flags:   0,
			Type:    [4]byte{},
			SubType: [4]byte{'s', 'o', 'u', 'n'},
			Name:    []byte("SoundHandler"),
			AtomPos: mp4io.AtomPos{
				Offset: 0,
				Size:   0,
			},
		}
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

func (m *Muxer) GetInit() buffer.Buffer {
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

	nextTrackID := safeInt32Conversion(m.log, m, int64(len(m.strs)+1), "next track ID")
	moov.Header.NextTrackID = nextTrackID

	for i, stream := range m.strs {
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
	idx := int(pkt.StreamIndex())
	if idx >= len(m.strs) {
		return fmt.Errorf("fmp4: stream index %d out of range (have %d streams)", idx, len(m.strs))
	}
	return m.strs[idx].writePacket(pkt)
}

// processMuxer tears down all stream buffers and rebuilds them from the current
// CodecParametersPair. Called after each fragment to reset per-fragment state.
func (m *Muxer) processMuxer() {
	m.strs = m.strs[:0]

	if m.params.VideoCodecParameters != nil {
		if err := m.newStream(m.params.VideoCodecParameters); err != nil {
			m.log.Errorf(m, "newStream error: %v", err)
		}
	}

	if m.params.AudioCodecParameters != nil {
		if err := m.newStream(m.params.AudioCodecParameters); err != nil {
			m.log.Errorf(m, "newStream error: %v", err)
		}
	}
}

// processTrackHeader fills the traf/tfhd/tfdt atoms for one stream. Split out
// of GetMP4Fragment purely to keep cyclomatic complexity within the linter budget.
func (m *Muxer) processTrackHeader(track *mp4io.TrackFrag, s *Stream) {
	if len(s.packets) == 0 {
		return
	}

	durationTS := s.timeToTS(s.packets[0].Duration())
	if durationTS < 0 || durationTS > int64(^uint32(0)) {
		m.log.Errorf(m, "Duration time value %d is outside uint32 range", durationTS)
		track.Header.DefaultDuration = 0
	} else {
		track.Header.DefaultDuration = uint32(durationTS)
	}

	pktSize := s.packets[0].Len()
	if pktSize < 0 || pktSize > int(^uint32(0)) {
		m.log.Errorf(m, "Packet size %d is outside uint32 range", pktSize)
		track.Header.DefaultSize = 0
	} else {
		track.Header.DefaultSize = uint32(pktSize)
	}

	firstFlags := mp4io.SampleNoDependencies
	if vPkt, casted := s.packets[0].(gomedia.VideoPacket); casted && !vPkt.IsKeyFrame() {
		firstFlags = mp4io.SampleNonKeyframe
	}
	if len(s.packets) > 1 {
		track.Header.DefaultFlags = mp4io.SampleNoDependencies
		if vPkt, casted := s.packets[1].(gomedia.VideoPacket); casted && !vPkt.IsKeyFrame() {
			track.Header.DefaultFlags = mp4io.SampleNonKeyframe
		}
	} else {
		track.Header.DefaultFlags = firstFlags
	}

	if firstFlags != track.Header.DefaultFlags {
		track.Run.Flags |= mp4io.TRUNFirstSampleFlags
	}
}

// processPackets translates each buffered packet into a trun entry for the moof.
func (m *Muxer) processPackets(track *mp4io.TrackFrag, s *Stream) {
	for j, pkt := range s.packets {
		if pkt.Len() != int(track.Header.DefaultSize) {
			track.Run.Flags |= mp4io.TRUNSampleSize
		}

		pktDurationTS := s.timeToTS(pkt.Duration())
		if pktDurationTS < 0 || pktDurationTS > int64(^uint32(0)) {
			m.log.Errorf(m, "Packet duration %d is outside uint32 range", pktDurationTS)
		} else if uint32(pktDurationTS) != track.Header.DefaultDuration {
			track.Run.Flags |= mp4io.TRUNSampleDuration
		}

		entryRunFlag := mp4io.SampleNoDependencies
		if vPkt, casted := pkt.(gomedia.VideoPacket); casted && !vPkt.IsKeyFrame() {
			entryRunFlag = mp4io.SampleNonKeyframe
		}

		if j != 0 && entryRunFlag != track.Header.DefaultFlags {
			track.Run.Flags |= mp4io.TRUNSampleFlags
		}

		var entryDuration uint32
		if pktDurationTS < 0 || pktDurationTS > int64(^uint32(0)) {
			m.log.Errorf(m, "Packet duration %d is outside uint32 range, using 0", pktDurationTS)
			entryDuration = 0
		} else {
			entryDuration = uint32(pktDurationTS)
		}

		var entrySize uint32
		pktDataSize := pkt.Len()
		if pktDataSize < 0 || pktDataSize > int(^uint32(0)) {
			m.log.Errorf(m, "Packet data size %d is outside uint32 range, using 0", pktDataSize)
			entrySize = 0
		} else {
			entrySize = uint32(pktDataSize)
		}

		runEntry := mp4io.TrackFragRunEntry{
			Duration: entryDuration,
			Size:     entrySize,
			Cts:      0,
			Flags:    entryRunFlag,
		}
		track.Run.Entries = append(track.Run.Entries, runEntry)
	}
}

// processDataOffsets fills each trun's data_offset (relative to the moof start) and
// appends the payload bytes of every packet into out. The offsets must be patched
// after the moof size is known, which is why this runs after the header scaffolding.
func (m *Muxer) processDataOffsets(moof *mp4io.MovieFrag, startMOOF int, out []byte, n int) int {
	for i, s := range m.strs {
		offset := n - startMOOF
		dataOffset := m.safeUint32Conversion(offset, "data offset")
		moof.Tracks[i].Run.DataOffset = dataOffset

		for _, pkt := range s.packets {
			n += copy(out[n:], pkt.Data())
		}
	}
	return n
}

// GetMP4Fragment assembles one fMP4 fragment (styp + moof + mdat) from the packets
// buffered since the previous call and returns it as a pooled buffer. Buffers are
// reset afterwards via processMuxer.
func (m *Muxer) GetMP4Fragment(idx int) buffer.Buffer {
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

	for _, s := range m.strs {
		outerRunFlag := mp4io.SampleNoDependencies
		if len(s.packets) > 0 {
			if vPkt, casted := s.packets[0].(gomedia.VideoPacket); casted && !vPkt.IsKeyFrame() {
				outerRunFlag = mp4io.SampleNonKeyframe
			}
		}

		streamIndex := int(s.StreamIndex())
		trackID := m.safeUint32Conversion(streamIndex+1, "track ID")

		track := &mp4io.TrackFrag{
			Header: &mp4io.TrackFragHeader{
				Version: 0,
				Flags: mp4io.TFHDDefaultDuration | mp4io.TFHDDefaultSize |
					mp4io.TFHDDefaultFlags | mp4io.TFHDDefaultBaseIsMOOF,
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
				Time: func() uint64 {
					timeValue := s.timeToTS(s.firstPacketTime)
					if timeValue < 0 {
						m.log.Errorf(m, "Negative time value %d, using 0", timeValue)
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
		m.processPackets(track, s)
	}

	styp := mp4io.NewSegmentType()

	// SIDX boxes are omitted: they are not required for LL-HLS streaming
	// (the manifest provides all timing information) and the previous
	// per-track SIDX had incorrect FirstOffset values with multiple tracks,
	// which could confuse some players.
	var totalDataSize int
	for _, s := range m.strs {
		totalDataSize += s.bufSize
	}

	bufSz := moof.Len() + styp.Len() + 8 + totalDataSize //nolint:mnd // 8 = mdat box header

	buf := buffer.Get(bufSz)

	var n int
	n += styp.Marshal(buf.Data())

	startMOOF := n
	n += moof.Len()

	mdatStart := n

	n += 4
	pio.PutU32BE(buf.Data()[n:], uint32(mp4io.MDAT))
	n += 4

	n = m.processDataOffsets(moof, startMOOF, buf.Data(), n)
	moof.Marshal(buf.Data()[startMOOF:])

	mdatSizeValue := n - mdatStart
	mdatSize := m.safeUint32Conversion(mdatSizeValue, "MDAT size")
	pio.PutU32BE(buf.Data()[mdatStart:], mdatSize)
	return buf
}
