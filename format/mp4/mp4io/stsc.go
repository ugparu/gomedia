package mp4io

import "github.com/ugparu/gomedia/utils/bits/pio"

const STSC = Tag(0x73747363)

func (self SampleToChunk) Tag() Tag {
	return STSC
}

type SampleToChunk struct {
	Version uint8
	Flags   uint32
	Entries []SampleToChunkEntry
	AtomPos
}

func (self SampleToChunk) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(STSC))
	n += self.marshal(b[8:]) + 8
	pio.PutU32BE(b[0:], uint32(n))
	return
}
func (self SampleToChunk) marshal(b []byte) (n int) {
	pio.PutU8(b[n:], self.Version)
	n += 1
	pio.PutU24BE(b[n:], self.Flags)
	n += 3
	pio.PutU32BE(b[n:], uint32(len(self.Entries)))
	n += 4
	for _, entry := range self.Entries {
		PutSampleToChunkEntry(b[n:], entry)
		n += LenSampleToChunkEntry
	}
	return
}
func (self SampleToChunk) Len() (n int) {
	n += 8
	n += 1
	n += 3
	n += 4
	n += LenSampleToChunkEntry * len(self.Entries)
	return
}
func (self *SampleToChunk) Unmarshal(b []byte, offset int) (n int, err error) {
	(&self.AtomPos).setPos(offset, len(b))
	n += 8
	if len(b) < n+1 {
		err = parseErr("Version", n+offset, err)
		return
	}
	self.Version = pio.U8(b[n:])
	n += 1
	if len(b) < n+3 {
		err = parseErr("Flags", n+offset, err)
		return
	}
	self.Flags = pio.U24BE(b[n:])
	n += 3
	var _len_Entries uint32
	_len_Entries = pio.U32BE(b[n:])
	n += 4
	self.Entries = make([]SampleToChunkEntry, _len_Entries)
	if len(b) < n+LenSampleToChunkEntry*len(self.Entries) {
		err = parseErr("SampleToChunkEntry", n+offset, err)
		return
	}
	for i := range self.Entries {
		self.Entries[i] = GetSampleToChunkEntry(b[n:])
		n += LenSampleToChunkEntry
	}
	return
}
func (self SampleToChunk) Children() (r []Atom) {
	return
}
