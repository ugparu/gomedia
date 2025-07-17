package mp4io

import "github.com/ugparu/gomedia/utils/bits/pio"

const STTS = Tag(0x73747473)

func (self TimeToSample) Tag() Tag {
	return STTS
}

type TimeToSample struct {
	Version uint8
	Flags   uint32
	Entries []TimeToSampleEntry
	AtomPos
}

func (self TimeToSample) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(STTS))
	n += self.marshal(b[8:]) + 8
	pio.PutU32BE(b[0:], uint32(n))
	return
}
func (self TimeToSample) marshal(b []byte) (n int) {
	pio.PutU8(b[n:], self.Version)
	n += 1
	pio.PutU24BE(b[n:], self.Flags)
	n += 3
	pio.PutU32BE(b[n:], uint32(len(self.Entries)))
	n += 4
	for _, entry := range self.Entries {
		PutTimeToSampleEntry(b[n:], entry)
		n += LenTimeToSampleEntry
	}
	return
}
func (self TimeToSample) Len() (n int) {
	n += 8
	n += 1
	n += 3
	n += 4
	n += LenTimeToSampleEntry * len(self.Entries)
	return
}
func (self *TimeToSample) Unmarshal(b []byte, offset int) (n int, err error) {
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
	self.Entries = make([]TimeToSampleEntry, _len_Entries)
	if len(b) < n+LenTimeToSampleEntry*len(self.Entries) {
		err = parseErr("TimeToSampleEntry", n+offset, err)
		return
	}
	for i := range self.Entries {
		self.Entries[i] = GetTimeToSampleEntry(b[n:])
		n += LenTimeToSampleEntry
	}
	return
}
func (self TimeToSample) Children() (r []Atom) {
	return
}
