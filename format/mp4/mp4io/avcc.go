package mp4io

import "github.com/ugparu/gomedia/utils/bits/pio"

const AVCC = Tag(0x61766343)

func (self AVC1Conf) Tag() Tag {
	return AVCC
}

type AVC1Conf struct {
	Data []byte
	AtomPos
}

func (self AVC1Conf) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(AVCC))
	n += self.marshal(b[8:]) + 8
	pio.PutU32BE(b[0:], uint32(n))
	return
}
func (self AVC1Conf) marshal(b []byte) (n int) {
	copy(b[n:], self.Data[:])
	n += len(self.Data[:])
	return
}
func (self AVC1Conf) Len() (n int) {
	n += 8
	n += len(self.Data[:])
	return
}
func (self *AVC1Conf) Unmarshal(b []byte, offset int) (n int, err error) {
	(&self.AtomPos).setPos(offset, len(b))
	n += 8
	self.Data = b[n:]
	n += len(b[n:])
	return
}
func (self AVC1Conf) Children() (r []Atom) {
	return
}
