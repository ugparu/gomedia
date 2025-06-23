// nolint: all
package mp4io

import (
	"errors"

	"github.com/ugparu/gomedia/utils/bits/pio"
)

const (
	SIDX           = Tag(0x73696478)
	baseSIDXSize   = 32
	baseSIDXSizeV1 = 40
	ReferenceSize  = 12
)

type Reference struct {
	ReferenceType      int32
	ReferencedSize     int32
	SubsegmentDuration int32
	StartsWithSAP      int32
	SAPType            int32
	SAPDeltaTime       int32
}

type SegmentIndex struct {
	Version     byte  // 0 or 1; 1 signals time is 64-bit
	Flags       int32 // 3 bytes
	RefernceID  int32 // ID of the reference (track) that points to the segment
	Timescale   int32
	EarliestPT  int64
	FirstOffset int64
	Entries     []Reference
	AtomPos
}

func (sidx SegmentIndex) Tag() Tag {
	return SIDX
}

func (sidx SegmentIndex) Len() int {
	n := baseSIDXSize
	if sidx.Version == 1 {
		n = baseSIDXSizeV1
	}
	return n + len(sidx.Entries)*ReferenceSize
}

func (sidx SegmentIndex) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(SIDX))
	n += sidx.marshal(b[8:]) + HeaderSize
	pio.PutU32BE(b[0:], uint32(n))
	return
}

func (sidx *SegmentIndex) marshal(b []byte) (n int) {
	pio.PutU8(b[n:], sidx.Version)
	n += 1
	pio.PutU32BE(b[n:], uint32(sidx.Flags))
	n += 3
	pio.PutU32BE(b[n:], uint32(sidx.RefernceID))
	n += 4
	pio.PutU32BE(b[n:], uint32(sidx.Timescale))
	n += 4
	if sidx.Version == 0 {
		pio.PutU32BE(b[n:], uint32(sidx.EarliestPT))
		n += 4
		pio.PutU32BE(b[n:], uint32(sidx.FirstOffset))
		n += 4
	} else {
		pio.PutU64BE(b[n:], uint64(sidx.EarliestPT))
		n += 8
		pio.PutU64BE(b[n:], uint64(sidx.FirstOffset))
		n += 8
	}
	n += 2
	pio.PutU16BE(b[n:], uint16(len(sidx.Entries)))
	n += 2
	for _, e := range sidx.Entries {
		pio.PutU32BE(b[n:], uint32(e.ReferenceType<<31|e.ReferencedSize))
		n += 4
		pio.PutU32BE(b[n:], uint32(e.SubsegmentDuration))
		n += 4
		pio.PutU32BE(b[n:], uint32(e.StartsWithSAP<<31|e.SAPType<<28|e.SAPDeltaTime))
		n += 4
	}
	return
}

func (sidx *SegmentIndex) Unmarshal(b []byte, offset int) (n int, err error) {
	return 0, errors.New("not implemented")
}

func (sidx *SegmentIndex) Children() []Atom {
	return nil
}
