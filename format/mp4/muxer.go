// Package mp4 provides functionality for working with MP4 files, specifically for muxing video streams.
package mp4

import (
	"fmt"
	"io"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/format/mp4/mp4io"
	"github.com/ugparu/gomedia/utils/bits/pio"
	"github.com/ugparu/gomedia/utils/buffer"
)

const (
	headerSize = 56 //nolint:mnd // ftyp(32) + free(8) + mdat_tag(16)
)

// MuxerOption is a functional option for configuring a Muxer.
type MuxerOption func(*Muxer)

// WithBuffer sets an external reusable buffer for I/O assembly.
// The buffer grows via Resize() if capacity is insufficient; across segments
// it stays at the high-water mark, avoiding re-allocation in steady state.
func WithBuffer(buf buffer.Buffer) MuxerOption {
	return func(m *Muxer) { m.buf = buf }
}

// Muxer represents an MP4 muxer that combines multiple media streams into a single MP4 file.
// If WithBuffer is used, writes are batched in memory until Flush/WriteTrailer.
// Without WithBuffer, packet payloads are written directly to writer (more syscalls, less RAM).
type Muxer struct {
	writer        io.WriteSeeker
	writePosition int64
	streams       []*Stream
	buf           buffer.Buffer // reusable buffer for I/O assembly
	bufUsed       int           // bytes written into buf so far
	flushed       bool          // true after first Flush() call
}

// NewMuxer creates a new Muxer instance with the given writer.
// Use WithBuffer to enable in-memory batching; by default writes go directly to writer.
func NewMuxer(writer io.WriteSeeker, opts ...MuxerOption) *Muxer {
	m := &Muxer{
		writer: writer,
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

func (mux *Muxer) GetWritePosition() int64 {
	return mux.writePosition
}

// GetPreLastPacket returns the pre-last packet (the packet that will be written on next WritePacket call)
// for the given stream index. Returns nil if no packet is buffered for that stream.
func (mux *Muxer) GetPreLastPacket(streamIndex uint8) gomedia.Packet {
	if int(streamIndex) >= len(mux.streams) {
		return nil
	}
	return mux.streams[streamIndex].lastPacket
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
		Flags:      0,
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
		Type:    [4]byte{}, // pre_defined = 0 per ISO 14496-12 §8.4.3
		SubType: [4]byte([]byte("vide")),
		Name:    []byte("VideoHandler"),
		AtomPos: mp4io.AtomPos{
			Offset: 0,
			Size:   0,
		},
	}
	stream.trackAtom.Media.Info = &mp4io.MediaInfo{
		Data: &mp4io.DataInfo{
			Refer: &mp4io.DataRefer{
				Version: 0,
				Flags:   0,
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
		stream.trackAtom.Media.Info.Video = new(mp4io.VideoMediaInfo)
		vPar, _ := codec.(gomedia.VideoCodecParameters)
		stream.trackAtom.Header.TrackWidth = float64(vPar.Width())
		stream.trackAtom.Header.TrackHeight = float64(vPar.Height())
	case gomedia.AAC:
		stream.trackAtom.Media.Info.Sound = new(mp4io.SoundMediaInfo)
	}
	stream.muxer = mux
	mux.streams = append(mux.streams, stream)

	return
}

// Mux combines the specified video and audio streams into a single MP4 file.
// With WithBuffer, header is staged in memory and written on Flush/WriteTrailer.
// Without WithBuffer, header is written immediately.
func (mux *Muxer) Mux(streams gomedia.CodecParametersPair) (err error) {
	mux.streams = []*Stream{}
	mux.bufUsed = 0
	mux.flushed = false
	mux.writePosition = 0

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

	var hdr [headerSize]byte
	mux.marshalHeader(hdr[:])
	if mux.buf != nil {
		mux.buf.Resize(headerSize)
		copy(mux.buf.Data()[:headerSize], hdr[:])
		mux.bufUsed = headerSize
	} else {
		if _, err = mux.writer.Write(hdr[:]); err != nil {
			return
		}
		mux.flushed = true // header already written in direct-write mode
	}

	mux.writePosition = headerSize

	// Prepare video streams for muxing.
	for _, stream := range mux.streams {
		if stream.Type().IsVideo() {
			stream.sample.CompositionOffset = new(mp4io.CompositionOffset)
		}
	}
	return
}

func (mux *Muxer) marshalHeader(dst []byte) {
	ftyp := mp4io.NewFileType()
	ftyp.Marshal(dst)
	off := ftyp.Len() // 32

	free := mp4io.FreeType{}
	free.Marshal(dst[off:])
	off += free.Len() // +8 = 40

	pio.PutU32BE(dst[off:], 1)
	pio.PutU32BE(dst[off+4:], uint32(mp4io.MDAT)) //nolint:mnd
}

const maxExtras = 3 //nolint:mnd // VPS + SPS + PPS at most

// writePayload writes parameter-set NALUs (with 4-byte length prefix each)
// followed by packet data. In buffered mode data is appended to the internal
// buffer; in direct mode it is written straight to the underlying writer.
func (mux *Muxer) writePayload(extras *[maxExtras][]byte, numExtras int, pktData []byte) error {
	if mux.buf != nil {
		return mux.appendPayload(extras, numExtras, pktData)
	}
	return mux.writePayloadDirect(extras, numExtras, pktData)
}

func (mux *Muxer) appendPayload(extras *[maxExtras][]byte, numExtras int, pktData []byte) error {
	totalSize := len(pktData)
	for i := range numExtras {
		totalSize += 4 + len(extras[i]) //nolint:mnd // 4-byte NALU length prefix
	}
	needed := mux.bufUsed + totalSize
	mux.buf.Resize(needed)
	out := mux.buf.Data()
	off := mux.bufUsed

	for i := range numExtras {
		pio.PutU32BE(out[off:], uint32(len(extras[i]))) //nolint:gosec
		off += 4                                         //nolint:mnd
		copy(out[off:], extras[i])
		off += len(extras[i])
	}
	copy(out[off:], pktData)
	mux.bufUsed = needed
	return nil
}

func (mux *Muxer) writePayloadDirect(extras *[maxExtras][]byte, numExtras int, pktData []byte) error {
	var hdr [4]byte //nolint:mnd
	for i := range numExtras {
		pio.PutU32BE(hdr[:], uint32(len(extras[i]))) //nolint:gosec
		if _, err := mux.writer.Write(hdr[:]); err != nil {
			return err
		}
		if _, err := mux.writer.Write(extras[i]); err != nil {
			return err
		}
	}
	_, err := mux.writer.Write(pktData)
	return err
}

// WritePacket writes a media packet to the muxer's in-memory buffer.
func (mux *Muxer) WritePacket(pkt gomedia.Packet) (err error) {
	idx := int(pkt.StreamIndex())
	if idx >= len(mux.streams) {
		return fmt.Errorf("mp4: stream index %d out of range (have %d streams)", idx, len(mux.streams))
	}
	return mux.streams[idx].writePacket(pkt)
}

// Flush writes buffered data to the underlying writer in a single Write call.
// On the first call it includes the MP4 header (ftyp + free + mdat tag with
// placeholder size). Subsequent calls write only newly accumulated packet data.
// After Flush has been called, WriteTrailer will need to seek back to patch
// the mdat size.
func (mux *Muxer) Flush() error {
	if mux.buf == nil || mux.bufUsed == 0 {
		return nil
	}
	_, err := mux.writer.Write(mux.buf.Data()[:mux.bufUsed])
	mux.bufUsed = 0
	mux.flushed = true
	return err
}

// buildMoov constructs the MOOV atom and returns its serialized bytes.
func (mux *Muxer) buildMoov() ([]byte, error) {
	moov := new(mp4io.Movie)
	moov.Header = mp4io.NewMovieHeader()
	moov.Header.NextTrackID = int32(len(mux.streams) + 1) //nolint:gosec

	maxDur := time.Duration(0)
	for _, stream := range mux.streams {
		if err := stream.fillTrackAtom(); err != nil {
			return nil, err
		}
		dur := stream.tsToTime(stream.duration)
		stream.trackAtom.Header.Duration = timeToTS(dur, int64(moov.Header.TimeScale))
		if dur > maxDur {
			maxDur = dur
		}
		moov.Tracks = append(moov.Tracks, stream.trackAtom)
	}
	moov.Header.Duration = timeToTS(maxDur, int64(moov.Header.TimeScale))

	b := make([]byte, moov.Len())
	moov.Marshal(b)
	return b, nil
}

// WriteTrailer completes the MP4 file by writing the trailer and necessary metadata.
// If Flush was never called, the entire MP4 is assembled in memory and written in
// a single Write call with no seeks. If Flush was called, remaining data is flushed
// and then the mdat size is patched via seek.
func (mux *Muxer) WriteTrailer() (err error) {
	// Write remaining packets for each stream.
	for _, stream := range mux.streams {
		if stream.lastPacket != nil {
			if err = stream.writePacket(stream.lastPacket); err != nil {
				stream.lastPacket.Release()
				stream.lastPacket = nil
				return
			}
			stream.lastPacket = nil
		}
	}

	moovBytes, err := mux.buildMoov()
	if err != nil {
		return err
	}

	mdatSize := mux.writePosition - 40 //nolint:mnd // headerSize(56) - mdat_box_header(16) = 40

	if !mux.flushed {
		return mux.writeTrailerFast(moovBytes, mdatSize)
	}
	return mux.writeTrailerSeek(moovBytes, mdatSize)
}

// writeTrailerFast assembles the entire MP4 into the reusable buffer and writes
// it in a single Write call. No seeks are performed.
func (mux *Muxer) writeTrailerFast(moovBytes []byte, mdatSize int64) error {
	if mux.buf == nil {
		return mux.writeTrailerSeek(moovBytes, mdatSize)
	}

	totalSize := mux.bufUsed + len(moovBytes)
	mux.buf.Resize(totalSize)
	out := mux.buf.Data()

	// Patch mdat extended size in the header region (offset 48).
	const mdatExtSizeOffset = 48 //nolint:mnd // ftyp(32) + free(8) + mdat_tag(8) = 48
	pio.PutU64BE(out[mdatExtSizeOffset:], uint64(mdatSize))

	copy(out[mux.bufUsed:], moovBytes)
	mux.bufUsed = 0

	_, err := mux.writer.Write(out[:totalSize])
	return err
}

// writeTrailerSeek flushes remaining data, seeks back to patch the mdat size,
// then writes the moov atom at the end.
func (mux *Muxer) writeTrailerSeek(moovBytes []byte, mdatSize int64) error {
	if err := mux.Flush(); err != nil {
		return err
	}

	const mdatExtSizeOffset = 48 //nolint:mnd // ftyp(32) + free(8) + mdat_tag(8) = 48
	if _, err := mux.writer.Seek(mdatExtSizeOffset, io.SeekStart); err != nil {
		return err
	}
	var tagHdr [8]byte //nolint:mnd
	pio.PutU64BE(tagHdr[:], uint64(mdatSize))
	if _, err := mux.writer.Write(tagHdr[:]); err != nil {
		return err
	}

	if _, err := mux.writer.Seek(mux.writePosition, io.SeekStart); err != nil {
		return err
	}
	_, err := mux.writer.Write(moovBytes)
	return err
}
