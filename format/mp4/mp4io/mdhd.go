package mp4io

import (
	"time"

	"github.com/ugparu/gomedia/utils/bits/pio"
)

const MDHD = Tag(0x6d646864)

func (self MediaHeader) Tag() Tag {
	return MDHD
}

type MediaHeader struct {
	Version    uint8
	Flags      uint32
	CreateTime time.Time
	ModifyTime time.Time
	TimeScale  int32
	Duration   int32
	Language   int16
	Quality    int16
	AtomPos
}

func (self MediaHeader) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(MDHD))
	n += self.marshal(b[8:]) + 8
	pio.PutU32BE(b[0:], uint32(n))
	return
}
func (self MediaHeader) marshal(b []byte) (n int) {
	pio.PutU8(b[n:], self.Version)
	n += 1
	pio.PutU24BE(b[n:], self.Flags)
	n += 3
	PutTime32(b[n:], self.CreateTime)
	n += 4
	PutTime32(b[n:], self.ModifyTime)
	n += 4
	pio.PutI32BE(b[n:], self.TimeScale)
	n += 4
	pio.PutI32BE(b[n:], self.Duration)
	n += 4
	pio.PutI16BE(b[n:], self.Language)
	n += 2
	pio.PutI16BE(b[n:], self.Quality)
	n += 2
	return
}
func (self MediaHeader) Len() (n int) {
	n += 8
	n += 1
	n += 3
	n += 4
	n += 4
	n += 4
	n += 4
	n += 2
	n += 2
	return
}
func (self *MediaHeader) Unmarshal(b []byte, offset int) (n int, err error) {
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
		err = parseErr("CreateTime", n+offset, err)
		return
	}
	self.CreateTime = GetTime32(b[n:])
	n += 4
	if len(b) < n+4 {
		err = parseErr("ModifyTime", n+offset, err)
		return
	}
	self.ModifyTime = GetTime32(b[n:])
	n += 4
	if len(b) < n+4 {
		err = parseErr("TimeScale", n+offset, err)
		return
	}
	self.TimeScale = pio.I32BE(b[n:])
	n += 4
	if len(b) < n+4 {
		err = parseErr("Duration", n+offset, err)
		return
	}
	self.Duration = pio.I32BE(b[n:])
	n += 4
	if len(b) < n+2 {
		err = parseErr("Language", n+offset, err)
		return
	}
	self.Language = pio.I16BE(b[n:])
	n += 2
	if len(b) < n+2 {
		err = parseErr("Quality", n+offset, err)
		return
	}
	self.Quality = pio.I16BE(b[n:])
	n += 2
	return
}
func (self MediaHeader) Children() (r []Atom) {
	return
}
