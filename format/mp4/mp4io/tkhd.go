package mp4io

import (
	"time"

	"github.com/ugparu/gomedia/utils/bits/pio"
)

const TKHD = Tag(0x746b6864)

func (self TrackHeader) Tag() Tag {
	return TKHD
}

type TrackHeader struct {
	Version        uint8
	Flags          uint32
	CreateTime     time.Time
	ModifyTime     time.Time
	TrackId        int32
	Duration       int32
	Layer          int16
	AlternateGroup int16
	Volume         float64
	Matrix         [9]int32
	TrackWidth     float64
	TrackHeight    float64
	AtomPos
}

func (self TrackHeader) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(TKHD))
	n += self.marshal(b[8:]) + 8
	pio.PutU32BE(b[0:], uint32(n))
	return
}
func (self TrackHeader) marshal(b []byte) (n int) {
	pio.PutU8(b[n:], self.Version)
	n += 1
	pio.PutU24BE(b[n:], self.Flags)
	n += 3
	PutTime32(b[n:], self.CreateTime)
	n += 4
	PutTime32(b[n:], self.ModifyTime)
	n += 4
	pio.PutI32BE(b[n:], self.TrackId)
	n += 4
	n += 4
	pio.PutI32BE(b[n:], self.Duration)
	n += 4
	n += 8
	pio.PutI16BE(b[n:], self.Layer)
	n += 2
	pio.PutI16BE(b[n:], self.AlternateGroup)
	n += 2
	PutFixed16(b[n:], self.Volume)
	n += 2
	n += 2
	for _, entry := range self.Matrix {
		pio.PutI32BE(b[n:], entry)
		n += 4
	}
	PutFixed32(b[n:], self.TrackWidth)
	n += 4
	PutFixed32(b[n:], self.TrackHeight)
	n += 4
	return
}
func (self TrackHeader) Len() (n int) {
	n += 8
	n += 1
	n += 3
	n += 4
	n += 4
	n += 4
	n += 4
	n += 4
	n += 8
	n += 2
	n += 2
	n += 2
	n += 2
	n += 4 * len(self.Matrix[:])
	n += 4
	n += 4
	return
}
func (self *TrackHeader) Unmarshal(b []byte, offset int) (n int, err error) {
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
		err = parseErr("TrackId", n+offset, err)
		return
	}
	self.TrackId = pio.I32BE(b[n:])
	n += 4
	n += 4
	if len(b) < n+4 {
		err = parseErr("Duration", n+offset, err)
		return
	}
	self.Duration = pio.I32BE(b[n:])
	n += 4
	n += 8
	if len(b) < n+2 {
		err = parseErr("Layer", n+offset, err)
		return
	}
	self.Layer = pio.I16BE(b[n:])
	n += 2
	if len(b) < n+2 {
		err = parseErr("AlternateGroup", n+offset, err)
		return
	}
	self.AlternateGroup = pio.I16BE(b[n:])
	n += 2
	if len(b) < n+2 {
		err = parseErr("Volume", n+offset, err)
		return
	}
	self.Volume = GetFixed16(b[n:])
	n += 2
	n += 2
	if len(b) < n+4*len(self.Matrix) {
		err = parseErr("Matrix", n+offset, err)
		return
	}
	for i := range self.Matrix {
		self.Matrix[i] = pio.I32BE(b[n:])
		n += 4
	}
	if len(b) < n+4 {
		err = parseErr("TrackWidth", n+offset, err)
		return
	}
	self.TrackWidth = GetFixed32(b[n:])
	n += 4
	if len(b) < n+4 {
		err = parseErr("TrackHeight", n+offset, err)
		return
	}
	self.TrackHeight = GetFixed32(b[n:])
	n += 4
	return
}
func (self TrackHeader) Children() (r []Atom) {
	return
}
