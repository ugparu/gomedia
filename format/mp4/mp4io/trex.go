package mp4io

import "github.com/ugparu/gomedia/utils/bits/pio"

const TREX = Tag(0x74726578)

func (self TrackExtend) Tag() Tag {
	return TREX
}

type TrackExtend struct {
	Version               uint8
	Flags                 uint32
	TrackId               uint32
	DefaultSampleDescIdx  uint32
	DefaultSampleDuration uint32
	DefaultSampleSize     uint32
	DefaultSampleFlags    uint32
	AtomPos
}

func (self TrackExtend) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(TREX))
	n += self.marshal(b[8:]) + 8
	pio.PutU32BE(b[0:], uint32(n))
	return
}
func (self TrackExtend) marshal(b []byte) (n int) {
	pio.PutU8(b[n:], self.Version)
	n += 1
	pio.PutU24BE(b[n:], self.Flags)
	n += 3
	pio.PutU32BE(b[n:], self.TrackId)
	n += 4
	pio.PutU32BE(b[n:], self.DefaultSampleDescIdx)
	n += 4
	pio.PutU32BE(b[n:], self.DefaultSampleDuration)
	n += 4
	pio.PutU32BE(b[n:], self.DefaultSampleSize)
	n += 4
	pio.PutU32BE(b[n:], self.DefaultSampleFlags)
	n += 4
	return
}
func (self TrackExtend) Len() (n int) {
	n += 8
	n += 1
	n += 3
	n += 4
	n += 4
	n += 4
	n += 4
	n += 4
	return
}
func (self *TrackExtend) Unmarshal(b []byte, offset int) (n int, err error) {
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
		err = parseErr("TrackId", n+offset, err)
		return
	}
	self.TrackId = pio.U32BE(b[n:])
	n += 4
	if len(b) < n+4 {
		err = parseErr("DefaultSampleDescIdx", n+offset, err)
		return
	}
	self.DefaultSampleDescIdx = pio.U32BE(b[n:])
	n += 4
	if len(b) < n+4 {
		err = parseErr("DefaultSampleDuration", n+offset, err)
		return
	}
	self.DefaultSampleDuration = pio.U32BE(b[n:])
	n += 4
	if len(b) < n+4 {
		err = parseErr("DefaultSampleSize", n+offset, err)
		return
	}
	self.DefaultSampleSize = pio.U32BE(b[n:])
	n += 4
	if len(b) < n+4 {
		err = parseErr("DefaultSampleFlags", n+offset, err)
		return
	}
	self.DefaultSampleFlags = pio.U32BE(b[n:])
	n += 4
	return
}
func (self TrackExtend) Children() (r []Atom) {
	return
}
