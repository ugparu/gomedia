package mp4io

import "github.com/ugparu/gomedia/utils/bits/pio"

const VMHD = Tag(0x766d6864)

func (self VideoMediaInfo) Tag() Tag {
	return VMHD
}

type VideoMediaInfo struct {
	Version      uint8
	Flags        uint32
	GraphicsMode int16
	Opcolor      [3]int16
	AtomPos
}

func (self VideoMediaInfo) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(VMHD))
	n += self.marshal(b[8:]) + 8
	pio.PutU32BE(b[0:], uint32(n))
	return
}
func (self VideoMediaInfo) marshal(b []byte) (n int) {
	pio.PutU8(b[n:], self.Version)
	n += 1
	pio.PutU24BE(b[n:], self.Flags)
	n += 3
	pio.PutI16BE(b[n:], self.GraphicsMode)
	n += 2
	for _, entry := range self.Opcolor {
		pio.PutI16BE(b[n:], entry)
		n += 2
	}
	return
}
func (self VideoMediaInfo) Len() (n int) {
	n += 8
	n += 1
	n += 3
	n += 2
	n += 2 * len(self.Opcolor[:])
	return
}
func (self *VideoMediaInfo) Unmarshal(b []byte, offset int) (n int, err error) {
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
	if len(b) < n+2 {
		err = parseErr("GraphicsMode", n+offset, err)
		return
	}
	self.GraphicsMode = pio.I16BE(b[n:])
	n += 2
	if len(b) < n+2*len(self.Opcolor) {
		err = parseErr("Opcolor", n+offset, err)
		return
	}
	for i := range self.Opcolor {
		self.Opcolor[i] = pio.I16BE(b[n:])
		n += 2
	}
	return
}
func (self VideoMediaInfo) Children() (r []Atom) {
	return
}
