// nolint: all
package mp4io

import "github.com/ugparu/gomedia/utils/bits/pio"

const (
	TRUN                 = Tag(0x7472756e)
	TRUNDataOffset       = 0x01
	TRUNFirstSampleFlags = 0x04
	TRUNSampleDuration   = 0x100
	TRUNSampleSize       = 0x200
	TRUNSampleFlags      = 0x400
	TRUNSampleCTS        = 0x800
)

type TrackFragRun struct {
	Version          uint8
	Flags            uint32
	DataOffset       uint32
	FirstSampleFlags uint32
	Entries          []TrackFragRunEntry
	AtomPos
}

func (tfr TrackFragRun) Tag() Tag {
	return TRUN
}

func (tfr TrackFragRun) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(TRUN))
	n += tfr.marshal(b[8:]) + HeaderSize
	pio.PutU32BE(b[0:], uint32(n))
	return
}

func (tfr TrackFragRun) marshal(b []byte) (n int) {
	pio.PutU8(b[n:], tfr.Version)
	n += 1
	pio.PutU24BE(b[n:], tfr.Flags)
	n += 3
	pio.PutU32BE(b[n:], uint32(len(tfr.Entries)))
	n += 4
	if tfr.Flags&TRUNDataOffset != 0 {
		{
			pio.PutU32BE(b[n:], tfr.DataOffset)
			n += 4
		}
	}
	if tfr.Flags&TRUNFirstSampleFlags != 0 {
		{
			pio.PutU32BE(b[n:], tfr.FirstSampleFlags)
			n += 4
		}
	}

	for _, entry := range tfr.Entries {
		if tfr.Flags&TRUNSampleDuration != 0 {
			pio.PutU32BE(b[n:], entry.Duration)
			n += 4
		}
		if tfr.Flags&TRUNSampleSize != 0 {
			pio.PutU32BE(b[n:], entry.Size)
			n += 4
		}
		if tfr.Flags&TRUNSampleFlags != 0 {
			pio.PutU32BE(b[n:], entry.Flags)
			n += 4
		}
		if tfr.Flags&TRUNSampleCTS != 0 {
			pio.PutU32BE(b[n:], entry.Cts)
			n += 4
		}
	}
	return
}

func (tfr TrackFragRun) Len() (n int) {
	n += 8
	n += 1
	n += 3
	n += 4
	if tfr.Flags&TRUNDataOffset != 0 {
		{
			n += 4
		}
	}
	if tfr.Flags&TRUNFirstSampleFlags != 0 {
		{
			n += 4
		}
	}

	for range tfr.Entries {
		if tfr.Flags&TRUNSampleDuration != 0 {
			n += 4
		}
		if tfr.Flags&TRUNSampleSize != 0 {
			n += 4
		}
		if tfr.Flags&TRUNSampleFlags != 0 {
			n += 4
		}
		if tfr.Flags&TRUNSampleCTS != 0 {
			n += 4
		}
	}
	return
}

func (tfr *TrackFragRun) Unmarshal(b []byte, offset int) (n int, err error) {
	(&tfr.AtomPos).setPos(offset, len(b))
	n += 8
	if len(b) < n+1 {
		err = parseErr("Version", n+offset, err)
		return
	}
	tfr.Version = pio.U8(b[n:])
	n += 1
	if len(b) < n+3 {
		err = parseErr("Flags", n+offset, err)
		return
	}
	tfr.Flags = pio.U24BE(b[n:])
	n += 3
	_lenEntries := pio.U32BE(b[n:])
	n += 4
	tfr.Entries = make([]TrackFragRunEntry, _lenEntries)
	if tfr.Flags&TRUNDataOffset != 0 {
		{
			if len(b) < n+4 {
				err = parseErr("DataOffset", n+offset, err)
				return
			}
			tfr.DataOffset = pio.U32BE(b[n:])
			n += 4
		}
	}
	if tfr.Flags&TRUNFirstSampleFlags != 0 {
		{
			if len(b) < n+4 {
				err = parseErr("FirstSampleFlags", n+offset, err)
				return
			}
			tfr.FirstSampleFlags = pio.U32BE(b[n:])
			n += 4
		}
	}

	for i := 0; i < int(_lenEntries); i++ {
		var flags uint32
		if i > 0 {
			flags = tfr.Flags
		} else {
			flags = tfr.FirstSampleFlags
		}
		entry := &tfr.Entries[i]
		if flags&TRUNSampleDuration != 0 {
			entry.Duration = pio.U32BE(b[n:])
			n += 4
		}
		if flags&TRUNSampleSize != 0 {
			entry.Size = pio.U32BE(b[n:])
			n += 4
		}
		if flags&TRUNSampleFlags != 0 {
			entry.Flags = pio.U32BE(b[n:])
			n += 4
		}
		if flags&TRUNSampleCTS != 0 {
			entry.Cts = pio.U32BE(b[n:])
			n += 4
		}
	}
	return
}

func (tfr TrackFragRun) Children() (r []Atom) {
	return
}

type TrackFragRunEntry struct {
	Duration uint32
	Size     uint32
	Flags    uint32
	Cts      uint32
}

func GetTrackFragRunEntry(b []byte) (self TrackFragRunEntry) {
	self.Duration = pio.U32BE(b[0:])
	self.Size = pio.U32BE(b[4:])
	self.Flags = pio.U32BE(b[8:])
	self.Cts = pio.U32BE(b[12:])
	return
}
func PutTrackFragRunEntry(b []byte, self TrackFragRunEntry) {
	pio.PutU32BE(b[0:], self.Duration)
	pio.PutU32BE(b[4:], self.Size)
	pio.PutU32BE(b[8:], self.Flags)
	pio.PutU32BE(b[12:], self.Cts)
}

const LenTrackFragRunEntry = 16
