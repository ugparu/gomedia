package mp4io

import "github.com/ugparu/gomedia/utils/bits/pio"

const HDLR = Tag(0x68646c72)

type HandlerRefer struct {
	Version     uint8
	Flags       uint32
	PreDefined  uint32
	HandlerType [4]byte
	Reserved    [3]uint32
	Name        []byte
	AtomPos
}

func (hdlr HandlerRefer) Tag() Tag {
	return HDLR
}

func (hdlr HandlerRefer) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(HDLR))
	n += hdlr.marshal(b[8:]) + 8
	pio.PutU32BE(b[0:], uint32(n))
	return
}
func (hdlr HandlerRefer) marshal(b []byte) (n int) {
	pio.PutU8(b[n:], hdlr.Version)
	n += 1
	pio.PutU24BE(b[n:], hdlr.Flags)
	n += 3
	pio.PutU32BE(b[n:], hdlr.PreDefined)
	n += 4
	copy(b[n:], hdlr.HandlerType[:])
	n += len(hdlr.HandlerType[:])
	pio.PutU32BE(b[n:], hdlr.Reserved[0])
	n += 4
	pio.PutU32BE(b[n:], hdlr.Reserved[1])
	n += 4
	pio.PutU32BE(b[n:], hdlr.Reserved[2])
	n += 4
	copy(b[n:], hdlr.Name[:])
	n += len(hdlr.Name[:])
	return
}
func (hdlr HandlerRefer) Len() (n int) {
	n += 8
	n += 1
	n += 3
	n += 4
	n += len(hdlr.HandlerType[:])
	n += 12 // 3 reserved fields * 4 bytes each
	n += len(hdlr.Name[:])
	return
}
func (hdlr *HandlerRefer) Unmarshal(b []byte, offset int) (n int, err error) {
	(&hdlr.AtomPos).setPos(offset, len(b))
	n += 8
	if len(b) < n+1 {
		err = parseErr("Version", n+offset, err)
		return
	}
	hdlr.Version = pio.U8(b[n:])
	n += 1
	if len(b) < n+3 {
		err = parseErr("Flags", n+offset, err)
		return
	}
	hdlr.Flags = pio.U24BE(b[n:])
	n += 3
	if len(b) < n+4 {
		err = parseErr("PreDefined", n+offset, err)
		return
	}
	hdlr.PreDefined = pio.U32BE(b[n:])
	n += 4
	if len(b) < n+len(hdlr.HandlerType) {
		err = parseErr("HandlerType", n+offset, err)
		return
	}
	copy(hdlr.HandlerType[:], b[n:])
	n += len(hdlr.HandlerType)
	if len(b) < n+12 {
		err = parseErr("Reserved", n+offset, err)
		return
	}
	hdlr.Reserved[0] = pio.U32BE(b[n:])
	n += 4
	hdlr.Reserved[1] = pio.U32BE(b[n:])
	n += 4
	hdlr.Reserved[2] = pio.U32BE(b[n:])
	n += 4
	hdlr.Name = b[n:]
	n += len(b[n:])
	return
}
func (hdlr HandlerRefer) Children() (r []Atom) {
	return
}
