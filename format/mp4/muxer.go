// Package mp4 provides functionality for working with MP4 files, specifically for muxing video streams.
package mp4

import (
	"fmt"
	"io"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/format/mp4/mp4io"
	"github.com/ugparu/gomedia/utils/bits/pio"
)

const (
	headerSize = 56 //nolint:mnd // ftyp(32) + free(8) + mdat_tag(16)
	maxExtras  = 3  //nolint:mnd // VPS + SPS + PPS at most
)

// pendingWrite stores a deferred mdat write: parameter-set NALUs + packet data.
// The muxer holds a reference to the packet to keep the ring allocator slot alive.
type pendingWrite struct {
	pkt       gomedia.Packet
	extras    [maxExtras][]byte
	numExtras int
	extraSize int
	totalSize int // extraSize + pkt.Len()
}

// MuxerOption is a functional option for configuring a Muxer.
type MuxerOption func(*Muxer)

// WithBatchedDump makes Flush/WriteTrailer assemble all pending data into a
// single temporary buffer and write it in one syscall. Without this option
// the muxer writes directly from packet data (zero-copy, more syscalls).
// The temporary buffer is short-lived and collected by GC quickly.
func WithBatchedDump() MuxerOption {
	return func(m *Muxer) { m.batchedDump = true }
}

// Muxer represents an MP4 muxer that combines multiple media streams into a single MP4 file.
// Packets are accumulated via WritePacket; actual I/O is deferred until Flush or WriteTrailer.
type Muxer struct {
	writer        io.WriteSeeker
	writePosition int64
	streams       []*Stream
	batchedDump   bool           // assemble into single buffer before writing
	pending       []pendingWrite // accumulated writes awaiting Flush
	pendingSize   int            // total mdat bytes across all pending entries
	flushed       bool           // true after first Flush() call
}

// NewMuxer creates a new Muxer instance with the given writer.
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

// Mux initializes the muxer for the given streams. No I/O is performed;
// the header and packet data are written at Flush/WriteTrailer time.
func (mux *Muxer) Mux(streams gomedia.CodecParametersPair) (err error) {
	mux.releasePending()
	mux.streams = []*Stream{}
	mux.flushed = false
	mux.writePosition = headerSize

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

// WritePacket accumulates the packet for later writing at Flush/WriteTrailer.
// The muxer takes ownership of the packet and will Release it at flush time.
func (mux *Muxer) WritePacket(pkt gomedia.Packet) (err error) {
	idx := int(pkt.StreamIndex())
	if idx >= len(mux.streams) {
		return fmt.Errorf("mp4: stream index %d out of range (have %d streams)", idx, len(mux.streams))
	}
	return mux.streams[idx].writePacket(pkt)
}

// ReleasePending releases all accumulated packets without writing.
// Use this to discard a segment on error or when the segment is too short.
func (mux *Muxer) ReleasePending() { mux.releasePending() }

func (mux *Muxer) releasePending() {
	for i := range mux.pending {
		if mux.pending[i].pkt != nil {
			mux.pending[i].pkt.Release()
			mux.pending[i].pkt = nil
		}
	}
	mux.pending = mux.pending[:0]
	mux.pendingSize = 0
	for _, s := range mux.streams {
		if s.lastPacket != nil {
			s.lastPacket.Release()
			s.lastPacket = nil
		}
	}
}

// Flush writes accumulated data to the underlying writer.
// On the first call it includes the MP4 header (with placeholder mdat size).
// After Flush, WriteTrailer will seek back to patch the mdat size.
// All flushed packets are released.
func (mux *Muxer) Flush() error {
	if len(mux.pending) == 0 && mux.flushed {
		return nil
	}
	if mux.batchedDump {
		return mux.flushBatched()
	}
	return mux.flushDirect()
}

func (mux *Muxer) flushDirect() error {
	if !mux.flushed {
		var hdr [headerSize]byte
		mux.marshalHeader(hdr[:])
		if _, err := mux.writer.Write(hdr[:]); err != nil {
			return err
		}
		mux.flushed = true
	}
	if err := mux.writePendingDirect(); err != nil {
		return err
	}
	return nil
}

func (mux *Muxer) flushBatched() error {
	total := mux.pendingSize
	if !mux.flushed {
		total += headerSize
	}
	if total == 0 {
		return nil
	}

	buf := make([]byte, total)
	off := 0
	if !mux.flushed {
		mux.marshalHeader(buf[:headerSize])
		off = headerSize
		mux.flushed = true
	}
	off = mux.copyPendingInto(buf, off)

	_, err := mux.writer.Write(buf[:off])
	return err
}

// writePendingDirect writes all pending entries directly from packet data
// and releases the packets. Used by the non-batched flush/trailer paths.
func (mux *Muxer) writePendingDirect() error {
	var hdr [4]byte //nolint:mnd
	for i := range mux.pending {
		pw := &mux.pending[i]
		for j := range pw.numExtras {
			pio.PutU32BE(hdr[:], uint32(len(pw.extras[j]))) //nolint:gosec
			if _, err := mux.writer.Write(hdr[:]); err != nil {
				return err
			}
			if _, err := mux.writer.Write(pw.extras[j]); err != nil {
				return err
			}
		}
		if _, err := mux.writer.Write(pw.pkt.Data()); err != nil {
			return err
		}
		pw.pkt.Release()
		pw.pkt = nil
	}
	mux.pending = mux.pending[:0]
	mux.pendingSize = 0
	return nil
}

// copyPendingInto copies all pending entries into dst starting at off,
// releases the packets, and returns the new offset.
func (mux *Muxer) copyPendingInto(dst []byte, off int) int {
	for i := range mux.pending {
		pw := &mux.pending[i]
		for j := range pw.numExtras {
			pio.PutU32BE(dst[off:], uint32(len(pw.extras[j]))) //nolint:gosec
			off += 4                                            //nolint:mnd
			copy(dst[off:], pw.extras[j])
			off += len(pw.extras[j])
		}
		copy(dst[off:], pw.pkt.Data())
		off += pw.pkt.Len()
		pw.pkt.Release()
		pw.pkt = nil
	}
	mux.pending = mux.pending[:0]
	mux.pendingSize = 0
	return off
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
// All accumulated packets are released (even on error).
// If Flush was never called, the entire MP4 is written without seeks.
// If Flush was called earlier, remaining data is flushed and the mdat size is patched via seek.
func (mux *Muxer) WriteTrailer() (err error) {
	defer mux.releasePending()

	// Flush remaining lastPacket per stream into pending.
	for _, stream := range mux.streams {
		if stream.lastPacket != nil {
			if err = stream.writePacket(stream.lastPacket); err != nil {
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

// writeTrailerFast writes header (with correct mdat size) + all pending data + moov
// without any seeks. Used when Flush was never called.
func (mux *Muxer) writeTrailerFast(moovBytes []byte, mdatSize int64) error {
	if mux.batchedDump {
		return mux.writeTrailerFastBatched(moovBytes, mdatSize)
	}
	return mux.writeTrailerFastDirect(moovBytes, mdatSize)
}

func (mux *Muxer) writeTrailerFastDirect(moovBytes []byte, mdatSize int64) error {
	var hdr [headerSize]byte
	mux.marshalHeader(hdr[:])
	const mdatExtSizeOffset = 48 //nolint:mnd // ftyp(32) + free(8) + mdat_tag(8) = 48
	pio.PutU64BE(hdr[mdatExtSizeOffset:], uint64(mdatSize))

	if _, err := mux.writer.Write(hdr[:]); err != nil {
		return err
	}
	if err := mux.writePendingDirect(); err != nil {
		return err
	}
	_, err := mux.writer.Write(moovBytes)
	return err
}

func (mux *Muxer) writeTrailerFastBatched(moovBytes []byte, mdatSize int64) error {
	total := headerSize + mux.pendingSize + len(moovBytes)
	buf := make([]byte, total)

	mux.marshalHeader(buf[:headerSize])
	const mdatExtSizeOffset = 48 //nolint:mnd // ftyp(32) + free(8) + mdat_tag(8) = 48
	pio.PutU64BE(buf[mdatExtSizeOffset:], uint64(mdatSize))

	off := mux.copyPendingInto(buf, headerSize)
	copy(buf[off:], moovBytes)

	_, err := mux.writer.Write(buf[:total])
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
