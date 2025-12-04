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

// isH265Slice checks if a NALU is a H.265 slice
func isH265Slice(nal []byte) bool {
	if len(nal) < h265MinNALUSize {
		return false
	}
	naluType := (nal[0] >> 1) & h265NALTypeMask
	// H.265 slice types (VCL NAL units)
	return naluType >= h265.NalUnitCodedSliceTrailR && naluType <= h265.NalUnitReservedVcl31
}

// isH265FirstSliceInPicture checks if a H.265 slice is the first slice in a picture
func isH265FirstSliceInPicture(nal []byte) bool {
	if len(nal) < h265MinNALUSize || !isH265Slice(nal) {
		return false
	}
	// Check first_slice_segment_in_pic_flag (bit 7 of the third byte)
	return nal[2]>>h265FirstSliceBit&1 == 1
}

// fillTrackAtom fills the Track Atom with information based on the stream's codec parameters.
func (s *Stream) fillTrackAtom() (err error) {
	// Set time scale and duration in the media header of the Track Atom.
	s.trackAtom.Media.Header.TimeScale = int32(s.timeScale) //nolint:gosec
	s.trackAtom.Media.Header.Duration = int32(s.duration)   //nolint:gosec

	const defaultDPI = 72
	const defaultDepth = 24

	// Customize settings based on the codec type (specifically handling H.264 codec parameters).
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
			VorizontalResolution: defaultDPI,
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
			NumberOfChannels: int16(codecPar.ChannelLayout().Count()),         //nolint:gosec
			SampleSize:       int16(codecPar.SampleFormat().BytesPerSample()), //nolint:gosec
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
	if s.sample.SampleSize.SampleSize != 0 {
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

func (s *Stream) readPacket(tm time.Duration, url string) (pkt gomedia.Packet, err error) {

	// Check if we have a buffered H.265 sliced packet to return first
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
		nalus, _ := nal.SplitNALUs(data)
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
		nalus, _ := nal.SplitNALUs(data)
		h265Par, _ := s.CodecParameters.(*h265.CodecParameters)

		for _, nal := range nalus {
			naluType := (nal[0] >> 1) & h265NALTypeMask

			// Handle parameter sets (VPS, SPS, PPS) and other non-slice NALUs
			if naluType == h265.NalUnitVps || naluType == h265.NalUnitSps || naluType == h265.NalUnitPps ||
				naluType == h265.NalUnitAccessUnitDelimiter || naluType == h265.NalUnitPrefixSei || naluType == h265.NalUnitSuffixSei {
				// Non-slice NALUs: add to current sliced packet if exists, otherwise create new packet
				buf := make([]byte, 4)                            //nolint:mnd // size of header
				binary.BigEndian.PutUint32(buf, uint32(len(nal))) //nolint:gosec
				naluWithHeader := append(buf, nal...)

				if s.h265SlicedPacket != nil {
					// Add to existing sliced packet buffer
					existingData := s.h265SlicedPacket.Buffer.Data()
					newData := append(existingData, naluWithHeader...)
					s.h265SlicedPacket.Buffer.Resize(len(newData))
					s.h265SlicedPacket.Buffer.Write(newData)
				} else {
					// Create new packet for parameter sets
					pkt = h265.NewPacket(false, tm, time.Now(), naluWithHeader, url, h265Par)
				}
				continue
			}

			// Handle slice NALUs (VCL NAL units)
			if isH265Slice(nal) {
				buf := make([]byte, 4)                            //nolint:mnd // size of header
				binary.BigEndian.PutUint32(buf, uint32(len(nal))) //nolint:gosec
				naluWithHeader := append(buf, nal...)

				// Check if this slice indicates a key frame
				sliceIsKey := isKeyFrame || h265.IsKey(naluType)

				if isH265FirstSliceInPicture(nal) {
					// First slice in picture: finalize previous packet and start new one
					if s.h265SlicedPacket != nil {
						pkt = s.h265SlicedPacket
					}
					s.h265SlicedPacket = h265.NewPacket(sliceIsKey, tm, time.Now(), naluWithHeader, url, h265Par)
					s.h265BufferHasKey = sliceIsKey
				} else if s.h265SlicedPacket != nil {
					// Subsequent slice: add to current buffered packet
					existingData := s.h265SlicedPacket.Buffer.Data()
					newData := append(existingData, naluWithHeader...)
					s.h265SlicedPacket.Buffer.Resize(len(newData))
					s.h265SlicedPacket.Buffer.Write(newData)
					s.h265SlicedPacket.IsKeyFrm = s.h265SlicedPacket.IsKeyFrm || sliceIsKey
					s.h265BufferHasKey = s.h265BufferHasKey || sliceIsKey
				} else {
					// Unexpected: slice without first slice in picture
					// Treat as new frame
					s.h265SlicedPacket = h265.NewPacket(sliceIsKey, tm, time.Now(), naluWithHeader, url, h265Par)
					s.h265BufferHasKey = sliceIsKey
				}
			}
		}

		// If we have a packet to return but no new sliced packet was started,
		// it means we finished processing all slices for the current frame
		if pkt == nil && s.h265SlicedPacket != nil {
			// Check if we should return the buffered packet
			// This happens when we've read all NALUs in the sample and have a complete frame
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

	// For H.265, if we don't have a packet yet but we're still buffering slices,
	// recursively read the next sample to continue building the frame
	if pkt == nil && s.CodecParameters.Type() == gomedia.H265 && s.h265SlicedPacket != nil {
		return s.readPacket(tm, url)
	}

	return
}

// writePacket writes a media packet to the stream.
func (s *Stream) writePacket(nPkt gomedia.Packet) (err error) {
	defer func() {
		s.lastPacket = nPkt
	}()

	if s.lastPacket == nil {
		return
	}

	// if s.lastPacket.Duration() == 0 {
	s.lastPacket.SetDuration(nPkt.Timestamp() - s.lastPacket.Timestamp())
	// }

	pkt := s.lastPacket

	if vPacket, casted := pkt.(gomedia.VideoPacket); casted && vPacket.IsKeyFrame() {
		buf := make([]byte, 4) //nolint:mnd // size of header

		switch pkt.(gomedia.VideoPacket).CodecParameters().(type) {
		case *h264.CodecParameters:
			h264Pkt, _ := pkt.(*h264.Packet)
			h264Par, _ := h264Pkt.CodecParameters().(*h264.CodecParameters)
			sps := h264Par.SPS()
			pps := h264Par.PPS()

			existingData := h264Pkt.Buffer.Data()
			binary.BigEndian.PutUint32(buf, uint32(len(pps))) //nolint:gosec
			newData := append(buf, append(pps, existingData...)...)

			buf = make([]byte, 4)                             //nolint:mnd // size of header
			binary.BigEndian.PutUint32(buf, uint32(len(sps))) //nolint:gosec
			newData = append(buf, append(sps, newData...)...)
			h264Pkt.Buffer.Resize(len(newData))
			h264Pkt.Buffer.Write(newData)
		case *h265.CodecParameters:
			h265Pkt, _ := pkt.(*h265.Packet)
			h265Par, _ := h265Pkt.CodecParameters().(*h265.CodecParameters)
			sps := h265Par.SPS()
			pps := h265Par.PPS()
			vps := h265Par.VPS()

			existingData := h265Pkt.Buffer.Data()
			binary.BigEndian.PutUint32(buf, uint32(len(pps))) //nolint:gosec
			newData := append(buf, append(pps, existingData...)...)

			buf = make([]byte, 4)                             //nolint:mnd // size of header
			binary.BigEndian.PutUint32(buf, uint32(len(sps))) //nolint:gosec
			newData = append(buf, append(sps, newData...)...)

			buf = make([]byte, 4)                             //nolint:mnd // size of header
			binary.BigEndian.PutUint32(buf, uint32(len(vps))) //nolint:gosec
			newData = append(buf, append(vps, newData...)...)
			h265Pkt.Buffer.Resize(len(newData))
			h265Pkt.Buffer.Write(newData)
		}
	}

	// Write the packet data to the buffered writer.
	if _, err = s.muxer.bufferedWriter.Write(pkt.Data()); err != nil {
		return
	}

	// For video packets, update sync sample information.
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
		offset := uint32(1)
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

	// Update duration and sample index.
	s.duration += int64(sDr)
	s.sampleIndex++
	s.sample.ChunkOffset.Entries = append(s.sample.ChunkOffset.Entries, uint32(s.muxer.writePosition)) //nolint:gosec
	s.sample.SampleSize.Entries = append(s.sample.SampleSize.Entries, uint32(len(pkt.Data())))         //nolint:gosec

	// f, ok := s.muxer.writer.(*os.File)
	// if ok {
	// 	if err = s.muxer.bufferedWriter.Flush(); err != nil {
	// 		return
	// 	}

	// 	if err = s.lastPacket.SwitchToMmap(f, s.muxer.lastPacketStart, int64(len(s.lastPacket.Data()))); err != nil {
	// 		return
	// 	}
	// }

	// Update write position.
	s.muxer.writePosition += int64(len(pkt.Data()))
	return
}
