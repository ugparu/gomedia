package mp4io

import "github.com/ugparu/gomedia/utils/bits/pio"

const URL = Tag(0x75726c20)

func (self DataReferUrl) Tag() Tag {
	return URL
}

type DataReferUrl struct {
	Version uint8
	Flags   uint32
	AtomPos
}

func (self DataReferUrl) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(URL))
	n += self.marshal(b[8:]) + 8
	pio.PutU32BE(b[0:], uint32(n))
	return
}
func (self DataReferUrl) marshal(b []byte) (n int) {
	pio.PutU8(b[n:], self.Version)
	n += 1
	pio.PutU24BE(b[n:], self.Flags)
	n += 3
	return
}
func (self DataReferUrl) Len() (n int) {
	n += 8
	n += 1
	n += 3
	return
}
func (self *DataReferUrl) Unmarshal(b []byte, offset int) (n int, err error) {
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
	return
}
func (self DataReferUrl) Children() (r []Atom) {
	return
}
