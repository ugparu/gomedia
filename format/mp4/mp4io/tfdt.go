package mp4io

import "github.com/ugparu/gomedia/utils/bits/pio"

const TFDT = Tag(0x74666474)

func (self TrackFragDecodeTime) Tag() Tag {
	return TFDT
}

type TrackFragDecodeTime struct {
	Version uint8
	Flags   uint32
	Time    uint64
	AtomPos
}

func (self TrackFragDecodeTime) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(TFDT))
	n += self.marshal(b[8:]) + 8
	pio.PutU32BE(b[0:], uint32(n))
	return
}
func (self TrackFragDecodeTime) marshal(b []byte) (n int) {
	pio.PutU8(b[n:], self.Version)
	n += 1
	pio.PutU24BE(b[n:], self.Flags)
	n += 3
	if self.Version != 0 {
		pio.PutU64BE(b[n:], self.Time)
		n += 8
	} else {
		pio.PutU32BE(b[n:], uint32(self.Time))
		n += 4
	}
	return
}
func (self TrackFragDecodeTime) Len() (n int) {
	n += 8
	n += 1
	n += 3
	if self.Version != 0 {
		n += 8
	} else {

		n += 4
	}
	return
}
func (self *TrackFragDecodeTime) Unmarshal(b []byte, offset int) (n int, err error) {
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
	if self.Version != 0 {
		self.Time = pio.U64BE(b[n:])
		n += 8
	} else {

		self.Time = pio.U64BE(b[n:])
		n += 4
	}
	return
}
func (self TrackFragDecodeTime) Children() (r []Atom) {
	return
}
