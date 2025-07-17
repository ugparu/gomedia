// nolint: all
package mp4io

import (
	"github.com/ugparu/gomedia/utils/bits/pio"
)

type TimeToSampleEntry struct {
	Count    uint32
	Duration uint32
}

func GetTimeToSampleEntry(b []byte) (self TimeToSampleEntry) {
	self.Count = pio.U32BE(b[0:])
	self.Duration = pio.U32BE(b[4:])
	return
}
func PutTimeToSampleEntry(b []byte, self TimeToSampleEntry) {
	pio.PutU32BE(b[0:], self.Count)
	pio.PutU32BE(b[4:], self.Duration)
}

const LenTimeToSampleEntry = 8

type SampleToChunkEntry struct {
	FirstChunk      uint32
	SamplesPerChunk uint32
	SampleDescId    uint32
}

func GetSampleToChunkEntry(b []byte) (self SampleToChunkEntry) {
	self.FirstChunk = pio.U32BE(b[0:])
	self.SamplesPerChunk = pio.U32BE(b[4:])
	self.SampleDescId = pio.U32BE(b[8:])
	return
}
func PutSampleToChunkEntry(b []byte, self SampleToChunkEntry) {
	pio.PutU32BE(b[0:], self.FirstChunk)
	pio.PutU32BE(b[4:], self.SamplesPerChunk)
	pio.PutU32BE(b[8:], self.SampleDescId)
}

const LenSampleToChunkEntry = 12

type CompositionOffsetEntry struct {
	Count  uint32
	Offset uint32
}

func GetCompositionOffsetEntry(b []byte) (self CompositionOffsetEntry) {
	self.Count = pio.U32BE(b[0:])
	self.Offset = pio.U32BE(b[4:])
	return
}
func PutCompositionOffsetEntry(b []byte, self CompositionOffsetEntry) {
	pio.PutU32BE(b[0:], self.Count)
	pio.PutU32BE(b[4:], self.Offset)
}

const LenCompositionOffsetEntry = 8

const MOOV = Tag(0x6d6f6f76)

func (m Movie) Tag() Tag {
	return MOOV
}

type Movie struct {
	Header      *MovieHeader
	MovieExtend *MovieExtend
	Tracks      []*Track
	Unknowns    []Atom
	AtomPos
}

func (m Movie) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(MOOV))
	n += m.marshal(b[8:]) + 8
	pio.PutU32BE(b[0:], uint32(n))
	return
}

func (m Movie) marshal(b []byte) (n int) {
	if m.Header != nil {
		n += m.Header.Marshal(b[n:])
	}
	for _, atom := range m.Tracks {
		sz := atom.Marshal(b[n:])
		n += sz
	}
	if m.MovieExtend != nil {
		n += m.MovieExtend.Marshal(b[n:])
	}
	for _, atom := range m.Unknowns {
		n += atom.Marshal(b[n:])
	}
	return
}

func (m Movie) Len() (n int) {
	n += 8
	if m.Header != nil {
		n += m.Header.Len()
	}
	if m.MovieExtend != nil {
		n += m.MovieExtend.Len()
	}
	for _, atom := range m.Tracks {
		n += atom.Len()
	}
	for _, atom := range m.Unknowns {
		n += atom.Len()
	}
	return
}

func (m *Movie) Unmarshal(b []byte, offset int) (n int, err error) {
	(&m.AtomPos).setPos(offset, len(b))
	n += 8
	for n+8 < len(b) {
		tag := Tag(pio.U32BE(b[n+4:]))
		size := int(pio.U32BE(b[n:]))
		if len(b) < n+size {
			err = parseErr("TagSizeInvalid", n+offset, err)
			return
		}
		switch tag {
		case MVHD:
			{
				atom := &MovieHeader{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("mvhd", n+offset, err)
					return
				}
				m.Header = atom
			}
		case MVEX:
			{
				atom := &MovieExtend{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("mvex", n+offset, err)
					return
				}
				m.MovieExtend = atom
			}
		case TRAK:
			{
				atom := &Track{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("trak", n+offset, err)
					return
				}
				m.Tracks = append(m.Tracks, atom)
			}
		default:
			{
				atom := &Dummy{Tag_: tag, Data: b[n : n+size]}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("", n+offset, err)
					return
				}
				m.Unknowns = append(m.Unknowns, atom)
			}
		}
		n += size
	}
	return
}

func (m Movie) Children() (r []Atom) {
	if m.Header != nil {
		r = append(r, m.Header)
	}
	if m.MovieExtend != nil {
		r = append(r, m.MovieExtend)
	}
	for _, atom := range m.Tracks {
		r = append(r, atom)
	}
	r = append(r, m.Unknowns...)
	return
}
