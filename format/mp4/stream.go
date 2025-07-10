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
}

// timeToTS converts a duration to a timestamp based on the stream's time scale.
func (s *Stream) timeToTS(tm time.Duration) int64 {
	return int64(tm * time.Duration(s.timeScale) / time.Second)
}

// tsToTime converts a timestamp to a duration based on the stream's time scale.
func (s *Stream) tsToTime(ts int64) time.Duration {
	return time.Duration(ts) * time.Second / time.Duration(s.timeScale)
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
			NumberOfChannels: int16(codecPar.ChannelLayout().Count()), //nolint:gosec
			SampleSize:       16,                                      //nolint:gosec
			SampleRate:       float64(codecPar.SampleRate()),
			Conf: &mp4io.ElemStreamDesc{
				DecConfig:  codecPar.MPEG4AudioConfigBytes(),
				TrackId:    uint16(s.index + 1),
				MaxBitrate: 128000, // Default AAC bitrate
				AvgBitrate: 128000,
			},
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
		if s.syncSampleIndex+1 < len(entries) && uint32(s.sampleIndex+1) == //nolint:gosec
			entries[s.syncSampleIndex+1]-1 {
			s.syncSampleIndex++
		}
	}

	s.sampleIndex++
	return
}

func (s *Stream) readPacket(tm time.Duration, url string) (pkt gomedia.Packet, err error) {
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
		for _, nal := range nalus {
			naluType := nal[0] & 0x1f //nolint: mnd
			if naluType >= 1 && naluType <= 31 {
				buf := make([]byte, 4)                            //nolint:mnd // size of header
				binary.BigEndian.PutUint32(buf, uint32(len(nal))) //nolint:gosec

				h265Par, _ := s.CodecParameters.(*h265.CodecParameters)
				pkt = h265.NewPacket(isKeyFrame, tm, time.Now(), append(buf, nal...), url, h265Par)
			}
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

			binary.BigEndian.PutUint32(buf, uint32(len(pps)))               //nolint:gosec
			h264Pkt.Buffer = append(buf, append(pps, h264Pkt.Buffer...)...) //nolint:gocritic

			binary.BigEndian.PutUint32(buf, uint32(len(sps)))               //nolint:gosec
			h264Pkt.Buffer = append(buf, append(sps, h264Pkt.Buffer...)...) //nolint:gocritic
		case *h265.CodecParameters:
			h265Pkt, _ := pkt.(*h265.Packet)
			h265Par, _ := h265Pkt.CodecParameters().(*h265.CodecParameters)
			sps := h265Par.SPS()
			pps := h265Par.PPS()
			vps := h265Par.VPS()

			binary.BigEndian.PutUint32(buf, uint32(len(pps)))               //nolint:gosec
			h265Pkt.Buffer = append(buf, append(pps, h265Pkt.Buffer...)...) //nolint:gocritic

			binary.BigEndian.PutUint32(buf, uint32(len(sps)))               //nolint:gosec
			h265Pkt.Buffer = append(buf, append(sps, h265Pkt.Buffer...)...) //nolint:gocritic

			binary.BigEndian.PutUint32(buf, uint32(len(vps)))               //nolint:gosec
			h265Pkt.Buffer = append(buf, append(vps, h265Pkt.Buffer...)...) //nolint:gocritic
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

	// Update write position.
	s.muxer.writePosition += int64(len(pkt.Data()))
	return
}
