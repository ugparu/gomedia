package mp4io

import "github.com/ugparu/gomedia/utils/bits/pio"

const CTTS = Tag(0x63747473)

func (self CompositionOffset) Tag() Tag {
	return CTTS
}

type CompositionOffset struct {
	Version uint8
	Flags   uint32
	Entries []CompositionOffsetEntry
	AtomPos
}

func (self CompositionOffset) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(CTTS))
	n += self.marshal(b[8:]) + 8
	pio.PutU32BE(b[0:], uint32(n))
	return
}
func (self CompositionOffset) marshal(b []byte) (n int) {
	pio.PutU8(b[n:], self.Version)
	n += 1
	pio.PutU24BE(b[n:], self.Flags)
	n += 3
	pio.PutU32BE(b[n:], uint32(len(self.Entries)))
	n += 4
	for _, entry := range self.Entries {
		PutCompositionOffsetEntry(b[n:], entry)
		n += LenCompositionOffsetEntry
	}
	return
}
func (self CompositionOffset) Len() (n int) {
	n += 8
	n += 1
	n += 3
	n += 4
	n += LenCompositionOffsetEntry * len(self.Entries)
	return
}
func (self *CompositionOffset) Unmarshal(b []byte, offset int) (n int, err error) {
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
	self.Entries = make([]CompositionOffsetEntry, _len_Entries)
	if len(b) < n+LenCompositionOffsetEntry*len(self.Entries) {
		err = parseErr("CompositionOffsetEntry", n+offset, err)
		return
	}
	for i := range self.Entries {
		self.Entries[i] = GetCompositionOffsetEntry(b[n:])
		n += LenCompositionOffsetEntry
	}
	return
}
func (self CompositionOffset) Children() (r []Atom) {
	return
}
