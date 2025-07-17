package mp4io

import "github.com/ugparu/gomedia/utils/bits/pio"

const MFHD = Tag(0x6d666864)

func (self MovieFragHeader) Tag() Tag {
	return MFHD
}

type MovieFragHeader struct {
	Version uint8
	Flags   uint32
	Seqnum  uint32
	AtomPos
}

func (self MovieFragHeader) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(MFHD))
	n += self.marshal(b[8:]) + 8
	pio.PutU32BE(b[0:], uint32(n))
	return
}
func (self MovieFragHeader) marshal(b []byte) (n int) {
	pio.PutU8(b[n:], self.Version)
	n += 1
	pio.PutU24BE(b[n:], self.Flags)
	n += 3
	pio.PutU32BE(b[n:], self.Seqnum)
	n += 4
	return
}
func (self MovieFragHeader) Len() (n int) {
	n += 8
	n += 1
	n += 3
	n += 4
	return
}
func (self *MovieFragHeader) Unmarshal(b []byte, offset int) (n int, err error) {
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
		err = parseErr("Seqnum", n+offset, err)
		return
	}
	self.Seqnum = pio.U32BE(b[n:])
	n += 4
	return
}
func (self MovieFragHeader) Children() (r []Atom) {
	return
}
