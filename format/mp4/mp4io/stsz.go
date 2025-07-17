package mp4io

import "github.com/ugparu/gomedia/utils/bits/pio"

const STSZ = Tag(0x7374737a)

func (self SampleSize) Tag() Tag {
	return STSZ
}

type SampleSize struct {
	Version    uint8
	Flags      uint32
	SampleSize uint32
	Entries    []uint32
	AtomPos
}

func (self SampleSize) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(STSZ))
	n += self.marshal(b[8:]) + 8
	pio.PutU32BE(b[0:], uint32(n))
	return
}
func (self SampleSize) marshal(b []byte) (n int) {
	pio.PutU8(b[n:], self.Version)
	n += 1
	pio.PutU24BE(b[n:], self.Flags)
	n += 3
	pio.PutU32BE(b[n:], self.SampleSize)
	n += 4
	if self.SampleSize != 0 {
		return
	}
	pio.PutU32BE(b[n:], uint32(len(self.Entries)))
	n += 4
	for _, entry := range self.Entries {
		pio.PutU32BE(b[n:], entry)
		n += 4
	}
	return
}
func (self SampleSize) Len() (n int) {
	n += 8
	n += 1
	n += 3
	n += 4
	if self.SampleSize != 0 {
		return
	}
	n += 4
	n += 4 * len(self.Entries)
	return
}
func (self *SampleSize) Unmarshal(b []byte, offset int) (n int, err error) {
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
	if len(b) < n+4 {
		err = parseErr("SampleSize", n+offset, err)
		return
	}
	self.SampleSize = pio.U32BE(b[n:])
	n += 4
	if self.SampleSize != 0 {
		return
	}
	var _len_Entries uint32
	_len_Entries = pio.U32BE(b[n:])
	n += 4
	self.Entries = make([]uint32, _len_Entries)
	if len(b) < n+4*len(self.Entries) {
		err = parseErr("uint32", n+offset, err)
		return
	}
	for i := range self.Entries {
		self.Entries[i] = pio.U32BE(b[n:])
		n += 4
	}
	return
}
func (self SampleSize) Children() (r []Atom) {
	return
}
