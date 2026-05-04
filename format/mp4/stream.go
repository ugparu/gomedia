package mp4

import (
	"encoding/binary"
	"io"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/aac"
	"github.com/ugparu/gomedia/codec/h264"
	"github.com/ugparu/gomedia/codec/h265"
	"github.com/ugparu/gomedia/codec/mjpeg"
	"github.com/ugparu/gomedia/format/mp4/mp4io"
	"github.com/ugparu/gomedia/utils/nal"
)

// timeToTS converts a duration to a timestamp based on the stream's time scale.
func timeToTS(tm time.Duration, timeScale int64) int64 {
	return int64(tm * time.Duration(timeScale) / time.Second)
}

type Stream struct {
	gomedia.CodecParameters // Embedding CodecParameters interface for codec-specific functionality.

	trackAtom *mp4io.Track // Track Atom containing information about the media track.
	index     int          // Index of the stream within the Muxer.

	lastPacket gomedia.Packet // Last processed packet in the stream.

	timeScale int64 // Time scale for the stream.
	duration  int64 // Duration of the stream.

	muxer *Muxer // Reference to the Muxer associated with the stream.

	demuxer *Demuxer // Reference to the Demuxer associated with the stream.

	sample      *mp4io.SampleTable // Sample table containing information about individual samples.
	sampleIndex int                // Index of the current sample within the stream.

	sampleOffsetInChunk int64 // Offset of the current sample within the chunk.
	syncSampleIndex     int   // Index of the current sample in the list of sync samples.

	dts int64 // Decoding timestamp of the stream.

	// Variables related to time-to-sample (stts) box.
	sttsEntryIndex         int
	sampleIndexInSttsEntry int

	// Variables related to composition time-to-sample (ctts) box.
	cttsEntryIndex         int
	sampleIndexInCttsEntry int

	// Variables related to sample chunk grouping.
	chunkGroupIndex    int
	chunkIndex         int
	sampleIndexInChunk int

	sttsEntry *mp4io.TimeToSampleEntry      // Current time-to-sample entry.
	cttsEntry *mp4io.CompositionOffsetEntry // Current composition time-to-sample entry.

	// H.265 sliced packet buffering support
	h265SlicedPacket *h265.Packet // Buffer for accumulating H.265 slices that belong to the same frame
	h265BufferHasKey bool         // Track if the current buffered frame contains key frame slices

	// Tracking for mmap support - where the packet data starts (after SPS/PPS/VPS for keyframes)
	lastPacketDataOffset int64
	lastPacketDataSize   int64

	naluBuf [][]byte // Reusable buffer for SplitNALUs to avoid per-packet allocation.
}

// timeToTS converts a duration to a timestamp based on the stream's time scale.
func (s *Stream) timeToTS(tm time.Duration) int64 {
	return int64(tm * time.Duration(s.timeScale) / time.Second)
}

// tsToTime converts a timestamp to a duration based on the stream's time scale.
func (s *Stream) tsToTime(ts int64) time.Duration {
	return time.Duration(ts) * time.Second / time.Duration(s.timeScale)
}

const (
	h265MinNALUSize   = 3    // Minimum size for H.265 NALU
	h265NALTypeMask   = 0x3f // Mask to extract NAL unit type from H.265 header
	h265FirstSliceBit = 7    // Bit position for first_slice_segment_in_pic_flag
)

// isH265Slice reports whether nal is an H.265 VCL slice (nal_unit_types 0-31 per
// ITU-T H.265 Table 7-1).
func isH265Slice(nal []byte) bool {
	if len(nal) < h265MinNALUSize {
		return false
	}
	naluType := (nal[0] >> 1) & h265NALTypeMask
	return naluType >= h265.NalUnitCodedSliceTrailR && naluType <= h265.NalUnitReservedVcl31
}

// isH265FirstSliceInPicture reads first_slice_segment_in_pic_flag from the slice
// segment header so callers know where one AU ends and the next begins.
func isH265FirstSliceInPicture(nal []byte) bool {
	if len(nal) < h265MinNALUSize || !isH265Slice(nal) {
		return false
	}
	return nal[2]>>h265FirstSliceBit&1 == 1
}

// fillTrackAtom populates the SampleDescription for this stream's codec so the
// track atom reflects the configured width/height/codec config. Called after
// stts/stsc/stco entries have been accumulated.
func (s *Stream) fillTrackAtom() (err error) {
	s.trackAtom.Media.Header.TimeScale = int32(s.timeScale) //nolint:gosec
	s.trackAtom.Media.Header.Duration = s.duration

	const defaultDPI = 72
	const defaultDepth = 24

	switch codecPar := s.CodecParameters.(type) {
	case *h264.CodecParameters:
		width, height := codecPar.Width(), codecPar.Height()

		s.sample.SampleDesc.AVC1Desc = &mp4io.AVC1Desc{
			DataRefIdx:           1,
			Version:              0,
			Revision:             0,
			Vendor:               0,
			TemporalQuality:      0,
			SpatialQuality:       0,
			Width:                int16(width),  //nolint:gosec
			Height:               int16(height), //nolint:gosec
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
	case *h265.CodecParameters:
		width, height := codecPar.Width(), codecPar.Height()
		s.sample.SampleDesc.HV1Desc = &mp4io.HV1Desc{
			DataRefIdx:           1,
			Version:              0,
			Revision:             0,
			Vendor:               0,
			TemporalQuality:      0,
			SpatialQuality:       0,
			Width:                int16(width),  //nolint:gosec
			Height:               int16(height), //nolint:gosec
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
	case *mjpeg.CodecParameters:
		width, height := codecPar.Width(), codecPar.Height()

		s.sample.SampleDesc.MJPGDesc = &mp4io.MJPGDesc{
			DataRefIdx:           1,
			Version:              0,
			Revision:             0,
			Vendor:               0,
			TemporalQuality:      0,
			SpatialQuality:       0,
			Width:                int16(width),  //nolint:gosec
			Height:               int16(height), //nolint:gosec
			HorizontalResolution: defaultDPI,
			VerticalResolution:   defaultDPI,
			FrameCount:           1,
			CompressorName:       [32]byte{},
			Depth:                defaultDepth,
			ColorTableId:         -1,
			Unknowns:             []mp4io.Atom{},
			AtomPos: mp4io.AtomPos{
				Offset: 0,
				Size:   0,
			},
		}
	case *aac.CodecParameters:
		s.sample.SampleDesc.MP4ADesc = &mp4io.MP4ADesc{
			DataRefIdx:       1,
			NumberOfChannels: int16(codecPar.ChannelLayout().Count()),             //nolint:gosec
			SampleSize:       int16(codecPar.SampleFormat().BytesPerSample()) * 8, //nolint:gosec,mnd // bits per sample per ISO 14496-12 §12.2.3
			SampleRate:       float64(codecPar.SampleRate()),
			Conf: &mp4io.ElemStreamDesc{
				DecConfig: codecPar.MPEG4AudioConfigBytes(),
			},
		}

		s.trackAtom.Header.Volume = 1
		s.trackAtom.Header.AlternateGroup = 1
		s.trackAtom.Media.Handler = &mp4io.HandlerRefer{
			SubType: [4]byte{'s', 'o', 'u', 'n'},
			Name:    []byte("Sound Handler"),
		}
		s.trackAtom.Media.Info.Sound = &mp4io.SoundMediaInfo{}
	}

	return
}

func (s *Stream) isSampleValid() bool {
	if s.chunkIndex >= len(s.sample.ChunkOffset.Entries) {
		return false
	}
	if s.chunkGroupIndex >= len(s.sample.SampleToChunk.Entries) {
		return false
	}
	if s.sttsEntryIndex >= len(s.sample.TimeToSample.Entries) {
		return false
	}
	if s.sample.CompositionOffset != nil && len(s.sample.CompositionOffset.Entries) > 0 {
		if s.cttsEntryIndex >= len(s.sample.CompositionOffset.Entries) {
			return false
		}
	}
	if s.sample.SyncSample != nil {
		if s.syncSampleIndex >= len(s.sample.SyncSample.Entries) {
			return false
		}
	}
	if s.sample.SampleSize.SampleSize == 0 {
		if s.sampleIndex >= len(s.sample.SampleSize.Entries) {
			return false
		}
	}
	return true
}

func (s *Stream) incSampleIndex() (duration int64) {
	s.sampleIndexInChunk++
	if uint32(s.sampleIndexInChunk) == //nolint:gosec
		s.sample.SampleToChunk.Entries[s.chunkGroupIndex].SamplesPerChunk {
		s.chunkIndex++
		s.sampleIndexInChunk = 0
		s.sampleOffsetInChunk = int64(0)
	} else {
		if s.sample.SampleSize.SampleSize != 0 {
			s.sampleOffsetInChunk += int64(s.sample.SampleSize.SampleSize)
		} else {
			s.sampleOffsetInChunk += int64(s.sample.SampleSize.Entries[s.sampleIndex])
		}
	}

	if s.chunkGroupIndex+1 < len(s.sample.SampleToChunk.Entries) &&
		uint32(s.chunkIndex+1) == //nolint:gosec
			s.sample.SampleToChunk.Entries[s.chunkGroupIndex+1].FirstChunk {
		s.chunkGroupIndex++
	}

	sttsEntry := s.sample.TimeToSample.Entries[s.sttsEntryIndex]
	duration = int64(sttsEntry.Duration)
	s.sampleIndexInSttsEntry++
	s.dts += duration
	if uint32(s.sampleIndexInSttsEntry) == sttsEntry.Count { //nolint:gosec
		s.sampleIndexInSttsEntry = 0
		s.sttsEntryIndex++
	}

	if s.sample.CompositionOffset != nil && len(s.sample.CompositionOffset.Entries) > 0 {
		s.sampleIndexInCttsEntry++
		if uint32(s.sampleIndexInCttsEntry) == //nolint:gosec
			s.sample.CompositionOffset.Entries[s.cttsEntryIndex].Count {
			s.sampleIndexInCttsEntry = 0
			s.cttsEntryIndex++
		}
	}

	if s.sample.SyncSample != nil {
		entries := s.sample.SyncSample.Entries
		if s.syncSampleIndex+1 < len(entries) && uint32(s.sampleIndex+1) == entries[s.syncSampleIndex+1]-1 {
			s.syncSampleIndex++
		}
	}

	s.sampleIndex++
	return
}

// readPacket returns the next access unit for this stream. For H.265 the packet
// spans all slice NALUs that share a picture (first_slice_segment_in_pic_flag
// marks the boundary), so one MP4 sample can span multiple returned slices or
// multiple MP4 samples can coalesce into one returned packet.
func (s *Stream) readPacket(tm time.Duration, url string) (pkt gomedia.Packet, err error) {
	for {
		// Flush any still-buffered H.265 picture once samples are exhausted.
		if s.h265SlicedPacket != nil && !s.isSampleValid() {
			pkt = s.h265SlicedPacket
			s.h265SlicedPacket = nil
			s.h265BufferHasKey = false
			return
		}

		if !s.isSampleValid() {
			err = io.EOF
			return
		}

		chunkOffset := s.sample.ChunkOffset.Entries[s.chunkIndex]
		var sampleSize uint32
		if s.sample.SampleSize.SampleSize != 0 {
			sampleSize = s.sample.SampleSize.SampleSize
		} else {
			sampleSize = s.sample.SampleSize.Entries[s.sampleIndex]
		}

		sampleOffset := int64(chunkOffset) + s.sampleOffsetInChunk
		data := make([]byte, sampleSize)
		if err = s.demuxer.readat(sampleOffset, data); err != nil {
			return
		}

		isKeyFrame := false
		if s.sample.SyncSample != nil {
			if uint32(s.sampleIndex) == //nolint:gosec
				s.sample.SyncSample.Entries[s.syncSampleIndex]-1 {
				isKeyFrame = true
			}
		}
		switch s.CodecParameters.Type() {
		case gomedia.H264:
			nalus, _ := nal.SplitNALUs(data, s.naluBuf)
			for _, nal := range nalus {
				naluType := nal[0] & 0x1f //nolint: mnd
				if naluType >= 1 && naluType <= 5 {
					buf := make([]byte, 4)                            //nolint:mnd // size of header
					binary.BigEndian.PutUint32(buf, uint32(len(nal))) //nolint:gosec

					h264Par, _ := s.CodecParameters.(*h264.CodecParameters)
					pkt = h264.NewPacket(isKeyFrame, tm, time.Now(), append(buf, nal...), url, h264Par)
				}
			}
		case gomedia.H265:
			nalus, _ := nal.SplitNALUs(data, s.naluBuf)
			h265Par, _ := s.CodecParameters.(*h265.CodecParameters)

			for _, nal := range nalus {
				naluType := (nal[0] >> 1) & h265NALTypeMask

				// Non-VCL NALs (VPS/SPS/PPS/AUD/SEI) attach to the picture in progress.
				if naluType == h265.NalUnitVps || naluType == h265.NalUnitSps || naluType == h265.NalUnitPps ||
					naluType == h265.NalUnitAccessUnitDelimiter || naluType == h265.NalUnitPrefixSei || naluType == h265.NalUnitSuffixSei {
					buf := make([]byte, 4)                            //nolint:mnd // length prefix
					binary.BigEndian.PutUint32(buf, uint32(len(nal))) //nolint:gosec
					naluWithHeader := append(buf, nal...)

					if s.h265SlicedPacket != nil {
						oldLen := s.h265SlicedPacket.Len()
						s.h265SlicedPacket.Buf = append(s.h265SlicedPacket.Buf[:oldLen], naluWithHeader...)
					} else {
						pkt = h265.NewPacket(false, tm, time.Now(), naluWithHeader, url, h265Par)
					}
					continue
				}

				if isH265Slice(nal) {
					buf := make([]byte, 4)                            //nolint:mnd // length prefix
					binary.BigEndian.PutUint32(buf, uint32(len(nal))) //nolint:gosec
					naluWithHeader := append(buf, nal...)

					sliceIsKey := isKeyFrame || h265.IsKey(naluType)

					switch {
					case isH265FirstSliceInPicture(nal):
						// Boundary: flush the previous picture and start a fresh one.
						if s.h265SlicedPacket != nil {
							pkt = s.h265SlicedPacket
						}
						s.h265SlicedPacket = h265.NewPacket(sliceIsKey, tm, time.Now(), naluWithHeader, url, h265Par)
						s.h265BufferHasKey = sliceIsKey
					case s.h265SlicedPacket != nil:
						oldLen := s.h265SlicedPacket.Len()
						s.h265SlicedPacket.Buf = append(s.h265SlicedPacket.Buf[:oldLen], naluWithHeader...)
						s.h265SlicedPacket.IsKeyFrm = s.h265SlicedPacket.IsKeyFrm || sliceIsKey
						s.h265BufferHasKey = s.h265BufferHasKey || sliceIsKey
					default:
						// Non-first slice without a buffered picture — recover by
						// treating it as the start of a new picture.
						s.h265SlicedPacket = h265.NewPacket(sliceIsKey, tm, time.Now(), naluWithHeader, url, h265Par)
						s.h265BufferHasKey = sliceIsKey
					}
				}
			}

			if pkt == nil && s.h265SlicedPacket != nil {
				pkt = s.h265SlicedPacket
				s.h265SlicedPacket = nil
				s.h265BufferHasKey = false
			}
		case gomedia.AAC:
			aacPar, _ := s.CodecParameters.(*aac.CodecParameters)
			duration := (1024 * time.Second / time.Duration(aacPar.SampleRate())) //nolint:gosec
			pkt = aac.NewPacket(data, tm, url, time.Now(), aacPar, duration)
		case gomedia.MJPEG:
			mjpegPar, _ := s.CodecParameters.(*mjpeg.CodecParameters)
			pkt = mjpeg.NewPacket(isKeyFrame, tm, time.Now(), data, url, mjpegPar)
		}

		s.incSampleIndex()

		// H.265 only: keep draining samples until we have a complete picture.
		if pkt == nil && s.CodecParameters.Type() == gomedia.H265 && s.h265SlicedPacket != nil {
			continue
		}

		return
	}
}

// writePacket records a pending write for the previous packet and updates sample tables.
// No I/O is performed; actual writing is deferred to Flush/WriteTrailer.
func (s *Stream) writePacket(nPkt gomedia.Packet) (err error) {
	defer func() {
		s.lastPacket = nPkt
	}()

	if s.lastPacket == nil {
		return
	}

	s.lastPacket.SetDuration(nPkt.Timestamp() - s.lastPacket.Timestamp())

	pkt := s.lastPacket
	_ = pkt.Data() // ensure data slice is accessible (no-op for ring-backed packets)
	pktSize := pkt.Len()

	var pw pendingWrite
	pw.pkt = pkt

	if vPacket, casted := pkt.(gomedia.VideoPacket); casted && vPacket.IsKeyFrame() {
		switch pkt.(gomedia.VideoPacket).CodecParameters().(type) {
		case *h264.CodecParameters:
			h264Pkt, _ := pkt.(*h264.Packet)
			h264Par, _ := h264Pkt.CodecParameters().(*h264.CodecParameters)
			pw.extras[0] = h264Par.SPS()
			pw.extras[1] = h264Par.PPS()
			pw.numExtras = 2
			pw.extraSize = len(pw.extras[0]) + len(pw.extras[1]) + 8 //nolint:mnd // two 4-byte length prefixes
		case *h265.CodecParameters:
			h265Pkt, _ := pkt.(*h265.Packet)
			h265Par, _ := h265Pkt.CodecParameters().(*h265.CodecParameters)
			pw.extras[0] = h265Par.VPS()
			pw.extras[1] = h265Par.SPS()
			pw.extras[2] = h265Par.PPS()
			pw.numExtras = 3
			pw.extraSize = len(pw.extras[0]) + len(pw.extras[1]) + len(pw.extras[2]) + 12 //nolint:mnd // three 4-byte length prefixes
		}
		pktSize += pw.extraSize
	}
	pw.totalSize = pktSize

	s.lastPacketDataOffset = s.muxer.writePosition + int64(pw.extraSize)
	s.lastPacketDataSize = int64(pkt.Len())

	s.muxer.pending = append(s.muxer.pending, pw)
	s.muxer.pendingSize += pktSize

	if vPkt, casted := pkt.(gomedia.VideoPacket); casted {
		if vPkt.IsKeyFrame() && s.sample.SyncSample != nil {
			s.sample.SyncSample.Entries = append(s.sample.SyncSample.Entries, uint32(s.sampleIndex+1)) //nolint:gosec
		}
	}

	sDr := uint32(s.timeToTS(s.lastPacket.Duration())) //nolint:gosec
	if s.sttsEntry == nil || sDr != s.sttsEntry.Duration {
		s.sample.TimeToSample.Entries = append(s.sample.TimeToSample.Entries, mp4io.TimeToSampleEntry{
			Count:    0,
			Duration: sDr,
		})
		s.sttsEntry = &s.sample.TimeToSample.Entries[len(s.sample.TimeToSample.Entries)-1]
	}
	s.sttsEntry.Count++

	if s.sample.CompositionOffset != nil {
		offset := uint32(0) //nolint:mnd // CTS == DTS (no B-frame reordering)
		if s.cttsEntry == nil || offset != s.cttsEntry.Offset {
			table := s.sample.CompositionOffset
			table.Entries = append(table.Entries, mp4io.CompositionOffsetEntry{
				Count:  0,
				Offset: offset,
			})
			s.cttsEntry = &table.Entries[len(table.Entries)-1]
		}
		s.cttsEntry.Count++
	}

	s.duration += int64(sDr)
	s.sampleIndex++
	s.sample.ChunkOffset.Entries = append(s.sample.ChunkOffset.Entries, uint64(s.muxer.writePosition)) //nolint:gosec
	s.sample.SampleSize.Entries = append(s.sample.SampleSize.Entries, uint32(pktSize))                 //nolint:gosec

	s.muxer.writePosition += int64(pktSize)
	return
}
