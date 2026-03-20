package mp4io

import (
	"fmt"
	"math"

	"github.com/ugparu/gomedia/utils/bits/pio"
)

func (self ChunkOffset) String() string {
	return fmt.Sprintf("entries=%d", len(self.Entries))
}

const STCO = Tag(0x7374636f)
const CO64 = Tag(0x636f3634)

// ChunkOffset stores chunk offsets as uint64, supporting both stco (32-bit)
// and co64 (64-bit) per ISO 14496-12 §8.7.5. Marshal auto-selects the tag
// based on whether any offset exceeds uint32 range.
type ChunkOffset struct {
	Version uint8
	Flags   uint32
	Entries []uint64
	AtomPos
}

// needs64 returns true if any entry exceeds uint32 range.
func (self ChunkOffset) needs64() bool {
	for _, entry := range self.Entries {
		if entry > math.MaxUint32 {
			return true
		}
	}
	return false
}

func (self ChunkOffset) Tag() Tag {
	if self.needs64() {
		return CO64
	}
	return STCO
}

func (self ChunkOffset) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(self.Tag()))
	n += self.marshal(b[8:]) + 8
	pio.PutU32BE(b[0:], uint32(n))
	return
}
func (self ChunkOffset) marshal(b []byte) (n int) {
	use64 := self.needs64()
	pio.PutU8(b[n:], self.Version)
	n += 1
	pio.PutU24BE(b[n:], self.Flags)
	n += 3
	pio.PutU32BE(b[n:], uint32(len(self.Entries)))
	n += 4
	if use64 {
		for _, entry := range self.Entries {
			pio.PutU64BE(b[n:], entry)
			n += 8
		}
	} else {
		for _, entry := range self.Entries {
			pio.PutU32BE(b[n:], uint32(entry)) //nolint:gosec
			n += 4
		}
	}
	return
}
func (self ChunkOffset) Len() (n int) {
	n += 8
	n += 1
	n += 3
	n += 4
	if self.needs64() {
		n += 8 * len(self.Entries) //nolint:mnd
	} else {
		n += 4 * len(self.Entries)
	}
	return
}

// UnmarshalSTCO reads 32-bit chunk offsets (stco box).
func (self *ChunkOffset) UnmarshalSTCO(b []byte, offset int) (n int, err error) {
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
	var _len_Entries uint32
	_len_Entries = pio.U32BE(b[n:])
	n += 4
	self.Entries = make([]uint64, _len_Entries)
	if len(b) < n+4*len(self.Entries) {
		err = parseErr("uint32", n+offset, err)
		return
	}
	for i := range self.Entries {
		self.Entries[i] = uint64(pio.U32BE(b[n:]))
		n += 4
	}
	return
}

// UnmarshalCO64 reads 64-bit chunk offsets (co64 box).
func (self *ChunkOffset) UnmarshalCO64(b []byte, offset int) (n int, err error) {
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
	var _len_Entries uint32
	_len_Entries = pio.U32BE(b[n:])
	n += 4
	self.Entries = make([]uint64, _len_Entries)
	if len(b) < n+8*len(self.Entries) {
		err = parseErr("uint64", n+offset, err)
		return
	}
	for i := range self.Entries {
		self.Entries[i] = pio.U64BE(b[n:])
		n += 8
	}
	return
}

// Unmarshal defaults to 32-bit (stco) for backward compatibility.
func (self *ChunkOffset) Unmarshal(b []byte, offset int) (n int, err error) {
	return self.UnmarshalSTCO(b, offset)
}

func (self ChunkOffset) Children() (r []Atom) {
	return
}
