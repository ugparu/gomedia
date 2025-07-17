package mp4io

import "github.com/ugparu/gomedia/utils/bits/pio"

const MP4A = Tag(0x6d703461)

func (self MP4ADesc) Tag() Tag {
	return MP4A
}

type MP4ADesc struct {
	DataRefIdx       int16
	Version          int16
	RevisionLevel    int16
	Vendor           int32
	NumberOfChannels int16
	SampleSize       int16
	CompressionId    int16
	SampleRate       float64
	Conf             *ElemStreamDesc
	Unknowns         []Atom
	AtomPos
}

func (self MP4ADesc) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(MP4A))
	n += self.marshal(b[8:]) + 8
	pio.PutU32BE(b[0:], uint32(n))
	return
}
func (self MP4ADesc) marshal(b []byte) (n int) {
	n += 6
	pio.PutI16BE(b[n:], self.DataRefIdx)
	n += 2
	pio.PutI16BE(b[n:], self.Version)
	n += 2
	pio.PutI16BE(b[n:], self.RevisionLevel)
	n += 2
	pio.PutI32BE(b[n:], self.Vendor)
	n += 4
	pio.PutI16BE(b[n:], self.NumberOfChannels)
	n += 2
	pio.PutI16BE(b[n:], self.SampleSize)
	n += 2
	pio.PutI16BE(b[n:], self.CompressionId)
	n += 2
	n += 2
	PutFixed32(b[n:], self.SampleRate)
	n += 4
	if self.Conf != nil {
		n += self.Conf.Marshal(b[n:])
	}
	for _, atom := range self.Unknowns {
		n += atom.Marshal(b[n:])
	}
	return
}
func (self MP4ADesc) Len() (n int) {
	n += 8
	n += 6
	n += 2
	n += 2
	n += 2
	n += 4
	n += 2
	n += 2
	n += 2
	n += 2
	n += 4
	if self.Conf != nil {
		n += self.Conf.Len()
	}
	for _, atom := range self.Unknowns {
		n += atom.Len()
	}
	return
}
func (self *MP4ADesc) Unmarshal(b []byte, offset int) (n int, err error) {
	(&self.AtomPos).setPos(offset, len(b))
	n += 8
	n += 6
	if len(b) < n+2 {
		err = parseErr("DataRefIdx", n+offset, err)
		return
	}
	self.DataRefIdx = pio.I16BE(b[n:])
	n += 2
	if len(b) < n+2 {
		err = parseErr("Version", n+offset, err)
		return
	}
	self.Version = pio.I16BE(b[n:])
	n += 2
	if len(b) < n+2 {
		err = parseErr("RevisionLevel", n+offset, err)
		return
	}
	self.RevisionLevel = pio.I16BE(b[n:])
	n += 2
	if len(b) < n+4 {
		err = parseErr("Vendor", n+offset, err)
		return
	}
	self.Vendor = pio.I32BE(b[n:])
	n += 4
	if len(b) < n+2 {
		err = parseErr("NumberOfChannels", n+offset, err)
		return
	}
	self.NumberOfChannels = pio.I16BE(b[n:])
	n += 2
	if len(b) < n+2 {
		err = parseErr("SampleSize", n+offset, err)
		return
	}
	self.SampleSize = pio.I16BE(b[n:])
	n += 2
	if len(b) < n+2 {
		err = parseErr("CompressionId", n+offset, err)
		return
	}
	self.CompressionId = pio.I16BE(b[n:])
	n += 2
	n += 2
	if len(b) < n+4 {
		err = parseErr("SampleRate", n+offset, err)
		return
	}
	self.SampleRate = GetFixed32(b[n:])
	n += 4
	for n+8 < len(b) {
		tag := Tag(pio.U32BE(b[n+4:]))
		size := int(pio.U32BE(b[n:]))
		if len(b) < n+size {
			err = parseErr("TagSizeInvalid", n+offset, err)
			return
		}
		switch tag {
		case ESDS:
			{
				atom := &ElemStreamDesc{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("esds", n+offset, err)
					return
				}
				self.Conf = atom
			}
		default:
			{
				atom := &Dummy{Tag_: tag, Data: b[n : n+size]}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("", n+offset, err)
					return
				}
				self.Unknowns = append(self.Unknowns, atom)
			}
		}
		n += size
	}
	return
}
func (self MP4ADesc) Children() (r []Atom) {
	if self.Conf != nil {
		r = append(r, self.Conf)
	}
	r = append(r, self.Unknowns...)
	return
}
