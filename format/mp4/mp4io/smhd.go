package mp4io

import "github.com/ugparu/gomedia/utils/bits/pio"

const SMHD = Tag(0x736d6864)

func (self SoundMediaInfo) Tag() Tag {
	return SMHD
}

type SoundMediaInfo struct {
	Version uint8
	Flags   uint32
	Balance int16
	AtomPos
}

func (self SoundMediaInfo) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(SMHD))
	n += self.marshal(b[8:]) + 8
	pio.PutU32BE(b[0:], uint32(n))
	return
}
func (self SoundMediaInfo) marshal(b []byte) (n int) {
	pio.PutU8(b[n:], self.Version)
	n += 1
	pio.PutU24BE(b[n:], self.Flags)
	n += 3
	pio.PutI16BE(b[n:], self.Balance)
	n += 2
	n += 2
	return
}
func (self SoundMediaInfo) Len() (n int) {
	n += 8
	n += 1
	n += 3
	n += 2
	n += 2
	return
}
func (self *SoundMediaInfo) Unmarshal(b []byte, offset int) (n int, err error) {
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
		err = parseErr("Balance", n+offset, err)
		return
	}
	self.Balance = pio.I16BE(b[n:])
	n += 2
	n += 2
	return
}
func (self SoundMediaInfo) Children() (r []Atom) {
	return
}
