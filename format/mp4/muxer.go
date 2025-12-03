// Package mp4 provides functionality for working with MP4 files, specifically for muxing video streams.
package mp4

import (
	"bufio"
	"fmt"
	"io"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/format/mp4/mp4io"
	"github.com/ugparu/gomedia/utils/bits/pio"
)

// Muxer represents an MP4 muxer that combines multiple media streams into a single MP4 file.
type Muxer struct {
	writer          io.WriteSeeker // The underlying writer for the MP4 file.
	bufferedWriter  *bufio.Writer  // Buffered writer for efficient write operations.
	writePosition   int64          // Current write position in the file.
	streams         []*Stream      // List of media streams to be muxed.
	lastPacketStart int64          // Start offset of the last written packet.
}

// NewMuxer creates a new Muxer instance with the given writer.
func NewMuxer(writer io.WriteSeeker) *Muxer {
	return &Muxer{
		writer:         writer,
		bufferedWriter: bufio.NewWriterSize(writer, pio.RecommendBufioSize),
		writePosition:  0,
		streams:        nil,
	}
}

// newStream creates a new media stream based on the provided codec parameters and adds it to the muxer.
func (mux *Muxer) newStream(codec gomedia.CodecParameters) (err error) {
	// Check if the codec type is supported.
	switch codec.Type() {
	case gomedia.H264, gomedia.H265, gomedia.MJPEG, gomedia.AAC:
		// Supported codecs.
	default:
		err = fmt.Errorf("mp4: codec type=%v is not supported", codec.Type())
		return
	}

	// Create a new stream with default sample table and track atom settings.
	stream := new(Stream)
	stream.CodecParameters = codec
	stream.timeScale = 90000 //nolint: mnd

	stream.sample = &mp4io.SampleTable{
		SampleDesc:        new(mp4io.SampleDesc),
		TimeToSample:      new(mp4io.TimeToSample),
		CompositionOffset: new(mp4io.CompositionOffset),
		SampleToChunk: &mp4io.SampleToChunk{Version: 0, Flags: 0,
			Entries: []mp4io.SampleToChunkEntry{
				{FirstChunk: 1, SampleDescId: 1, SamplesPerChunk: 1}},
			AtomPos: mp4io.AtomPos{Offset: 0, Size: 0}},
		SyncSample:  new(mp4io.SyncSample),
		ChunkOffset: new(mp4io.ChunkOffset),
		SampleSize:  new(mp4io.SampleSize),
		AtomPos: mp4io.AtomPos{
			Offset: 0,
			Size:   0,
		},
	}

	now := time.Now()

	stream.trackAtom = new(mp4io.Track)

	const trackFlags = 0x0003
	stream.trackAtom.Header = &mp4io.TrackHeader{
		Version:        0,
		Flags:          trackFlags,
		CreateTime:     now,
		ModifyTime:     now,
		TrackId:        int32(len(mux.streams) + 1), //nolint:gosec
		Duration:       0,
		Layer:          0,
		AlternateGroup: 0,
		Volume:         0,
		Matrix:         [9]int32{0x00010000, 0, 0, 0, 0x00010000, 0, 0, 0, 0x00010000},
		TrackWidth:     0,
		TrackHeight:    0,
		AtomPos: mp4io.AtomPos{
			Offset: 0,
			Size:   0,
		},
	}

	stream.trackAtom.Media = new(mp4io.Media)
	stream.trackAtom.Media.Header = &mp4io.MediaHeader{
		Version:    0,
		Flags:      trackFlags,
		CreateTime: now,
		ModifyTime: now,
		TimeScale:  int32(stream.timeScale), //nolint: gosec
		Duration:   0,
		Language:   21956, //nolint: mnd
		Quality:    0,
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
	stream.trackAtom.Media.Info = &mp4io.MediaInfo{
		Sound: new(mp4io.SoundMediaInfo),
		Video: new(mp4io.VideoMediaInfo),
		Data: &mp4io.DataInfo{
			Refer: &mp4io.DataRefer{
				Version: 0,
				Flags:   trackFlags,
				Url: &mp4io.DataReferUrl{
					Version: 0,
					Flags:   0x000001, //nolint: mnd
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

	// Customize settings based on the codec type.
	switch codec.Type() {
	case gomedia.H264, gomedia.H265, gomedia.MJPEG:
		stream.sample.SyncSample = new(mp4io.SyncSample)
		vPar, _ := codec.(gomedia.VideoCodecParameters)
		stream.trackAtom.Header.TrackWidth = float64(vPar.Width())
		stream.trackAtom.Header.TrackHeight = float64(vPar.Height())
	}
	stream.muxer = mux
	mux.streams = append(mux.streams, stream)

	return
}

// Mux combines the specified video and audio streams into a single MP4 file.
func (mux *Muxer) Mux(streams gomedia.CodecParametersPair) (err error) {
	// Clear existing streams.
	mux.streams = []*Stream{}

	if streams.VideoCodecParameters != nil {
		if err = mux.newStream(streams.VideoCodecParameters); err != nil {
			return
		}
	}

	if streams.AudioCodecParameters != nil {
		if err = mux.newStream(streams.AudioCodecParameters); err != nil {
			return
		}
	}

	// Write the ftyp atom.
	ftyp := mp4io.NewFileType()

	buffer := make([]byte, ftyp.Len())

	ftyp.Marshal(buffer)

	if _, err = mux.writer.Write(buffer); err != nil {
		return
	}
	mux.writePosition += int64(ftyp.Len())

	free := mp4io.FreeType{
		AtomPos: mp4io.AtomPos{
			Offset: 0,
			Size:   0,
		},
	}

	buffer = buffer[:8]

	free.Marshal(buffer)

	if _, err = mux.writer.Write(buffer); err != nil {
		return
	}
	mux.writePosition += int64(free.Len())

	buffer = buffer[:16]

	pio.PutU32BE(buffer, 1)
	pio.PutU32BE(buffer[4:], uint32(mp4io.MDAT))
	if _, err = mux.writer.Write(buffer); err != nil {
		return
	}
	mux.writePosition += 16

	// Prepare video streams for muxing.
	for _, stream := range mux.streams {
		if stream.Type().IsVideo() {
			stream.sample.CompositionOffset = new(mp4io.CompositionOffset)
		}
	}
	return
}

// WritePacket writes a media packet to the muxer.
func (mux *Muxer) WritePacket(pkt gomedia.Packet) (err error) {
	mux.lastPacketStart = mux.writePosition
	return mux.streams[pkt.StreamIndex()].writePacket(pkt)
}

// WriteTrailer completes the MP4 file by writing the trailer and necessary metadata.
func (mux *Muxer) WriteTrailer() (err error) {
	// Write remaining packets for each stream.
	for _, stream := range mux.streams {
		if stream.lastPacket != nil {
			if err = stream.writePacket(stream.lastPacket); err != nil {
				return
			}
			stream.lastPacket = nil
		}
	}

	// Create the Movie Atom (MOOV) with metadata.
	moov := new(mp4io.Movie)
	moov.Header = mp4io.NewMovieHeader()
	moov.Header.NextTrackID = int32(len(mux.streams) + 1) //nolint:gosec

	// Calculate maximum duration and time scale for the movie.
	maxDur := time.Duration(0)
	for _, stream := range mux.streams {
		// Fill track atom settings for each stream.
		if err = stream.fillTrackAtom(); err != nil {
			return
		}
		dur := stream.tsToTime(stream.duration)
		stream.trackAtom.Header.Duration = int32(timeToTS(dur, int64(moov.Header.TimeScale))) //nolint:gosec
		if dur > maxDur {
			maxDur = dur
		}
		moov.Tracks = append(moov.Tracks, stream.trackAtom)
	}
	moov.Header.Duration = timeToTS(maxDur, int64(moov.Header.TimeScale))

	// Flush buffered writer.
	if err = mux.bufferedWriter.Flush(); err != nil {
		return
	}

	// Update the size of MDAT in the file.
	var mdatSize int64
	if mdatSize, err = mux.writer.Seek(0, 1); err != nil {
		return
	}
	mdatSize -= 40

	if _, err = mux.writer.Seek(40, 0); err != nil { //nolint: mnd
		return
	}

	if _, err = mux.writer.Seek(48, 0); err != nil { //nolint: mnd
		return
	}
	tagHdr := make([]byte, 8)              //nolint: mnd
	pio.PutU64BE(tagHdr, uint64(mdatSize)) //nolint:gosec
	if _, err = mux.writer.Write(tagHdr); err != nil {
		return
	}

	// Move to the end of the file and write the MOOV atom.
	if _, err = mux.writer.Seek(0, 2); err != nil { //nolint: mnd
		return
	}
	b := make([]byte, moov.Len())
	moov.Marshal(b)
	if _, err = mux.writer.Write(b); err != nil {
		return
	}

	return
}
