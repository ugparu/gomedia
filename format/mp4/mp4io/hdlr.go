package mp4io

import "github.com/ugparu/gomedia/utils/bits/pio"

const HDLR = Tag(0x68646c72)

func (self HandlerRefer) Tag() Tag {
	return HDLR
}

type HandlerRefer struct {
	Version uint8
	Flags   uint32
	Type    [4]byte
	SubType [4]byte
	Name    []byte
	AtomPos
}

func (self HandlerRefer) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(HDLR))
	n += self.marshal(b[8:]) + 8
	pio.PutU32BE(b[0:], uint32(n))
	return
}
const hdlrReservedSize = 12 // 3 × uint32 reserved per ISO 14496-12 §8.4.3

func (self HandlerRefer) marshal(b []byte) (n int) {
	pio.PutU8(b[n:], self.Version)
	n += 1
	pio.PutU24BE(b[n:], self.Flags)
	n += 3
	copy(b[n:], self.Type[:])
	n += len(self.Type[:])
	copy(b[n:], self.SubType[:])
	n += len(self.SubType[:])
	// 3 × uint32 reserved = 0
	pio.PutU32BE(b[n:], 0)
	pio.PutU32BE(b[n+4:], 0)
	pio.PutU32BE(b[n+8:], 0)
	n += hdlrReservedSize
	copy(b[n:], self.Name[:])
	n += len(self.Name[:])
	return
}
func (self HandlerRefer) Len() (n int) {
	n += 8
	n += 1
	n += 3
	n += len(self.Type[:])
	n += len(self.SubType[:])
	n += hdlrReservedSize
	n += len(self.Name[:])
	return
}
func (self *HandlerRefer) Unmarshal(b []byte, offset int) (n int, err error) {
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
	if len(b) < n+len(self.Type) {
		err = parseErr("Type", n+offset, err)
		return
	}
	copy(self.Type[:], b[n:])
	n += len(self.Type)
	if len(b) < n+len(self.SubType) {
		err = parseErr("SubType", n+offset, err)
		return
	}
	copy(self.SubType[:], b[n:])
	n += len(self.SubType)
	// Skip 3 × uint32 reserved
	if len(b) < n+hdlrReservedSize {
		err = parseErr("Reserved", n+offset, err)
		return
	}
	n += hdlrReservedSize
	self.Name = b[n:]
	n += len(b[n:])
	return
}
func (self HandlerRefer) Children() (r []Atom) {
	return
}
