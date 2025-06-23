// nolint: all
package mp4io

import (
	"time"

	"github.com/ugparu/gomedia/utils/bits/pio"
)

const (
	MVHD       = Tag(0x6d766864)
	mvhdSize   = 108
	mvhdSizeV1 = 120
	ts         = 1000
)

func NewMovieHeader() *MovieHeader {
	now := time.Now().UTC()
	return &MovieHeader{
		Version:         0,
		Flags:           0,
		CreateTime:      now,
		ModifyTime:      now,
		TimeScale:       ts,
		Duration:        0,
		PreferredRate:   1,
		PreferredVolume: 1,
		Matrix: [9]int32{
			0x00010000, 0, 0,
			0, 0x00010000, 0,
			0, 0, 0x00010000,
		},
		NextTrackID: 0,
		AtomPos: AtomPos{
			Offset: 0,
			Size:   0,
		},
	}
}

type MovieHeader struct {
	Version         byte      // 0 or 1; 1 signals time is 64-bit
	Flags           uint32    // 3 bytes
	CreateTime      time.Time // seconds since midnight, Jan 1, 1904, in UTC
	ModifyTime      time.Time // seconds since midnight, Jan 1, 1904, in UTC
	TimeScale       int32     // time units per second
	Duration        int64     // duration of the movie in time units
	PreferredRate   float64   // preferred rate during playback; 1.0 is normal
	PreferredVolume float64   // preferred playback volume; 1.0 is normal
	Matrix          [9]int32  // transformation matrix for the video
	NextTrackID     int32
	AtomPos
}

func (mvhd MovieHeader) Tag() Tag {
	return MVHD
}

func (mvhd MovieHeader) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(MVHD))
	n += mvhd.marshal(b[8:]) + HeaderSize
	pio.PutU32BE(b[0:], uint32(n))
	return
}
func (mvhd MovieHeader) marshal(b []byte) (n int) {
	pio.PutU8(b[n:], mvhd.Version)
	n += 1
	pio.PutU24BE(b[n:], mvhd.Flags)
	n += 3
	if mvhd.Version == 1 {
		PutTime64(b[n:], mvhd.CreateTime)
		n += 8
		PutTime64(b[n:], mvhd.ModifyTime)
		n += 8
	} else {
		PutTime32(b[n:], mvhd.CreateTime)
		n += 4
		PutTime32(b[n:], mvhd.ModifyTime)
		n += 4
	}
	pio.PutI32BE(b[n:], mvhd.TimeScale)
	n += 4
	if mvhd.Version == 1 {
		pio.PutI64BE(b[n:], mvhd.Duration)
		n += 8
	} else {
		pio.PutI32BE(b[n:], int32(mvhd.Duration))
		n += 4
	}
	PutFixed32(b[n:], mvhd.PreferredRate)
	n += 4
	PutFixed16(b[n:], mvhd.PreferredVolume)
	n += 2
	n += 10
	for _, entry := range mvhd.Matrix {
		pio.PutI32BE(b[n:], entry)
		n += 4
	}
	n += 24
	pio.PutI32BE(b[n:], mvhd.NextTrackID)
	n += 4
	return
}
func (mvhd MovieHeader) Len() int {
	if mvhd.Version == 1 {
		return mvhdSizeV1
	}
	return mvhdSize
}
func (mvhd *MovieHeader) Unmarshal(b []byte, offset int) (n int, err error) {
	(&mvhd.AtomPos).setPos(offset, len(b))
	n += 8
	if len(b) < n+1 {
		err = parseErr("Version", n+offset, err)
		return
	}
	mvhd.Version = pio.U8(b[n:])
	n += 1
	if len(b) < n+3 {
		err = parseErr("Flags", n+offset, err)
		return
	}
	mvhd.Flags = pio.U24BE(b[n:])
	n += 3

	if mvhd.Version == 1 {
		if len(b) < n+8 {
			err = parseErr("CreateTime", n+offset, err)
			return
		}
		mvhd.CreateTime = GetTime64(b[n:])
		n += 8
		if len(b) < n+8 {
			err = parseErr("ModifyTime", n+offset, err)
			return
		}
		mvhd.ModifyTime = GetTime64(b[n:])
		n += 8
		if len(b) < n+4 {
			err = parseErr("TimeScale", n+offset, err)
			return
		}
	} else {
		if len(b) < n+4 {
			err = parseErr("CreateTime", n+offset, err)
			return
		}
		mvhd.CreateTime = GetTime32(b[n:])
		n += 4
		if len(b) < n+4 {
			err = parseErr("ModifyTime", n+offset, err)
			return
		}
		mvhd.ModifyTime = GetTime32(b[n:])
		n += 4
		if len(b) < n+4 {
			err = parseErr("TimeScale", n+offset, err)
			return
		}
	}
	mvhd.TimeScale = pio.I32BE(b[n:])
	n += 4
	if len(b) < n+4 {
		err = parseErr("Duration", n+offset, err)
		return
	}
	if mvhd.Version == 1 {
		mvhd.Duration = pio.I64BE(b[n:])
		n += 8
		if len(b) < n+8 {
			err = parseErr("PreferredRate", n+offset, err)
			return
		}
	} else {
		mvhd.Duration = int64(pio.I32BE(b[n:]))
		n += 4
		if len(b) < n+4 {
			err = parseErr("PreferredRate", n+offset, err)
			return
		}
	}

	mvhd.PreferredRate = GetFixed32(b[n:])
	n += 4
	if len(b) < n+2 {
		err = parseErr("PreferredVolume", n+offset, err)
		return
	}
	mvhd.PreferredVolume = GetFixed16(b[n:])
	n += 2
	n += 10
	if len(b) < n+4*len(mvhd.Matrix) {
		err = parseErr("Matrix", n+offset, err)
		return
	}
	for i := range mvhd.Matrix {
		mvhd.Matrix[i] = pio.I32BE(b[n:])
		n += 4
	}
	n += 24
	if len(b) < n+4 {
		err = parseErr("NextTrackID", n+offset, err)
		return
	}
	mvhd.NextTrackID = pio.I32BE(b[n:])
	n += 4
	return
}
func (mvhd MovieHeader) Children() (r []Atom) {
	return
}
