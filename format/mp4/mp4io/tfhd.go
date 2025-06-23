// nolint: all
package mp4io

import (
	"github.com/ugparu/gomedia/utils/bits/pio"
)

const (
	TFHD                  = Tag(0x74666864)
	TFHDBaseDataOffset    = uint32(0x01)
	TFHDStsdID            = uint32(0x02)
	TFHDDefaultDuration   = uint32(0x08)
	TFHDDefaultSize       = uint32(0x10)
	TFHDDefaultFlags      = uint32(0x20)
	TFHDDurationIsEmpty   = uint32(0x20000) //
	TFHDDefaultBaseIsMOOF = uint32(0x10000)
)

type TrackFragHeader struct {
	Version         uint8
	Flags           uint32
	TrackID         uint32
	BaseDataOffset  uint64
	StsdID          uint32
	DefaultDuration uint32
	DefaultSize     uint32
	DefaultFlags    uint32
	AtomPos
}

func (TrackFragHeader) Tag() Tag {
	return TFHD
}

func (tfhd TrackFragHeader) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(TFHD))
	n += tfhd.marshal(b[8:]) + HeaderSize
	pio.PutU32BE(b[0:], uint32(n))
	return
}
func (tfhd TrackFragHeader) marshal(b []byte) (n int) {
	pio.PutU8(b[n:], tfhd.Version)
	n += 1
	pio.PutU24BE(b[n:], tfhd.Flags)
	n += 3
	pio.PutU32BE(b[n:], tfhd.TrackID)
	n += 4
	if tfhd.Flags&TFHDBaseDataOffset != 0 {
		{
			pio.PutU64BE(b[n:], tfhd.BaseDataOffset)
			n += 8
		}
	}
	if tfhd.Flags&TFHDStsdID != 0 {
		{
			pio.PutU32BE(b[n:], tfhd.StsdID)
			n += 4
		}
	}
	if tfhd.Flags&TFHDDefaultDuration != 0 {
		{
			pio.PutU32BE(b[n:], tfhd.DefaultDuration)
			n += 4
		}
	}
	if tfhd.Flags&TFHDDefaultSize != 0 {
		{
			pio.PutU32BE(b[n:], tfhd.DefaultSize)
			n += 4
		}
	}
	if tfhd.Flags&TFHDDefaultFlags != 0 {
		{
			pio.PutU32BE(b[n:], tfhd.DefaultFlags)
			n += 4
		}
	}
	if tfhd.Flags&TFHDDurationIsEmpty != 0 {
		{
			n += 4
		}
	}

	return
}
func (tfhd TrackFragHeader) Len() (n int) {
	n += 8
	n += 1
	n += 3
	n += 4
	if tfhd.Flags&TFHDBaseDataOffset != 0 {
		{
			n += 8
		}
	}
	if tfhd.Flags&TFHDStsdID != 0 {
		{
			n += 4
		}
	}
	if tfhd.Flags&TFHDDefaultDuration != 0 {
		{
			n += 4
		}
	}
	if tfhd.Flags&TFHDDefaultSize != 0 {
		{
			n += 4
		}
	}
	if tfhd.Flags&TFHDDefaultFlags != 0 {
		{
			n += 4
		}
	}
	if tfhd.Flags&TFHDDurationIsEmpty != 0 {
		{
			n += 4
		}
	}
	return
}
func (tfhd *TrackFragHeader) Unmarshal(b []byte, offset int) (n int, err error) {
	(&tfhd.AtomPos).setPos(offset, len(b))
	n += 8
	if len(b) < n+1 {
		err = parseErr("Version", n+offset, err)
		return
	}
	tfhd.Version = pio.U8(b[n:])
	n += 1
	if len(b) < n+3 {
		err = parseErr("Flags", n+offset, err)
		return
	}
	tfhd.Flags = pio.U24BE(b[n:])
	n += 3
	if tfhd.Flags&TFHDBaseDataOffset != 0 {
		{
			if len(b) < n+8 {
				err = parseErr("BaseDataOffset", n+offset, err)
				return
			}
			tfhd.BaseDataOffset = pio.U64BE(b[n:])
			n += 8
		}
	}
	if tfhd.Flags&TFHDStsdID != 0 {
		{
			if len(b) < n+4 {
				err = parseErr("StsdId", n+offset, err)
				return
			}
			tfhd.StsdID = pio.U32BE(b[n:])
			n += 4
		}
	}
	if tfhd.Flags&TFHDDefaultDuration != 0 {
		{
			if len(b) < n+4 {
				err = parseErr("DefaultDuration", n+offset, err)
				return
			}
			tfhd.DefaultDuration = pio.U32BE(b[n:])
			n += 4
		}
	}
	if tfhd.Flags&TFHDDefaultSize != 0 {
		{
			if len(b) < n+4 {
				err = parseErr("DefaultSize", n+offset, err)
				return
			}
			tfhd.DefaultSize = pio.U32BE(b[n:])
			n += 4
		}
	}
	if tfhd.Flags&TFHDDefaultFlags != 0 {
		{
			if len(b) < n+4 {
				err = parseErr("DefaultFlags", n+offset, err)
				return
			}
			tfhd.DefaultFlags = pio.U32BE(b[n:])
			n += 4
		}
	}
	return
}
func (tfhd TrackFragHeader) Children() (r []Atom) {
	return
}
