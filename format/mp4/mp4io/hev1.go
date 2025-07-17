package mp4io

import "github.com/ugparu/gomedia/utils/bits/pio"

// 0x31766568
const HEV1 = Tag(0x68657631)

func (self HV1Desc) Tag() Tag {
	return HEV1
}

type HV1Desc struct {
	DataRefIdx           int16
	Version              int16
	Revision             int16
	Vendor               int32
	TemporalQuality      int32
	SpatialQuality       int32
	Width                int16
	Height               int16
	HorizontalResolution float64
	VorizontalResolution float64
	FrameCount           int16
	CompressorName       [32]byte
	Depth                int16
	ColorTableId         int16
	Conf                 *HV1Conf
	Unknowns             []Atom
	AtomPos
}

func (self HV1Desc) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(HEV1))
	n += self.marshal(b[8:]) + 8
	pio.PutU32BE(b[0:], uint32(n))
	return
}

func (self HV1Desc) marshal(b []byte) (n int) {
	n += 6
	pio.PutI16BE(b[n:], self.DataRefIdx)
	n += 2
	pio.PutI16BE(b[n:], self.Version)
	n += 2
	pio.PutI16BE(b[n:], self.Revision)
	n += 2
	pio.PutI32BE(b[n:], self.Vendor)
	n += 4
	pio.PutI32BE(b[n:], self.TemporalQuality)
	n += 4
	pio.PutI32BE(b[n:], self.SpatialQuality)
	n += 4
	pio.PutI16BE(b[n:], self.Width)
	n += 2
	pio.PutI16BE(b[n:], self.Height)
	n += 2
	PutFixed32(b[n:], self.HorizontalResolution)
	n += 4
	PutFixed32(b[n:], self.VorizontalResolution)
	n += 4
	n += 4
	pio.PutI16BE(b[n:], self.FrameCount)
	n += 2
	copy(b[n:], self.CompressorName[:])
	n += len(self.CompressorName[:])
	pio.PutI16BE(b[n:], self.Depth)
	n += 2
	pio.PutI16BE(b[n:], self.ColorTableId)
	n += 2
	if self.Conf != nil {
		n += self.Conf.Marshal(b[n:])
	}
	for _, atom := range self.Unknowns {
		n += atom.Marshal(b[n:])
	}
	return
}

func (self HV1Desc) Len() (n int) {
	n += 8
	n += 6
	n += 2
	n += 2
	n += 2
	n += 4
	n += 4
	n += 4
	n += 2
	n += 2
	n += 4
	n += 4
	n += 4
	n += 2
	n += len(self.CompressorName[:])
	n += 2
	n += 2
	if self.Conf != nil {
		n += self.Conf.Len()
	}
	for _, atom := range self.Unknowns {
		n += atom.Len()
	}
	return
}

func (self *HV1Desc) Unmarshal(b []byte, offset int) (n int, err error) {
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
		err = parseErr("Revision", n+offset, err)
		return
	}
	self.Revision = pio.I16BE(b[n:])
	n += 2
	if len(b) < n+4 {
		err = parseErr("Vendor", n+offset, err)
		return
	}
	self.Vendor = pio.I32BE(b[n:])
	n += 4
	if len(b) < n+4 {
		err = parseErr("TemporalQuality", n+offset, err)
		return
	}
	self.TemporalQuality = pio.I32BE(b[n:])
	n += 4
	if len(b) < n+4 {
		err = parseErr("SpatialQuality", n+offset, err)
		return
	}
	self.SpatialQuality = pio.I32BE(b[n:])
	n += 4
	if len(b) < n+2 {
		err = parseErr("Width", n+offset, err)
		return
	}
	self.Width = pio.I16BE(b[n:])
	n += 2
	if len(b) < n+2 {
		err = parseErr("Height", n+offset, err)
		return
	}
	self.Height = pio.I16BE(b[n:])
	n += 2
	if len(b) < n+4 {
		err = parseErr("HorizontalResolution", n+offset, err)
		return
	}
	self.HorizontalResolution = GetFixed32(b[n:])
	n += 4
	if len(b) < n+4 {
		err = parseErr("VorizontalResolution", n+offset, err)
		return
	}
	self.VorizontalResolution = GetFixed32(b[n:])
	n += 4
	n += 4
	if len(b) < n+2 {
		err = parseErr("FrameCount", n+offset, err)
		return
	}
	self.FrameCount = pio.I16BE(b[n:])
	n += 2
	if len(b) < n+len(self.CompressorName) {
		err = parseErr("CompressorName", n+offset, err)
		return
	}
	copy(self.CompressorName[:], b[n:])
	n += len(self.CompressorName)
	if len(b) < n+2 {
		err = parseErr("Depth", n+offset, err)
		return
	}
	self.Depth = pio.I16BE(b[n:])
	n += 2
	if len(b) < n+2 {
		err = parseErr("ColorTableId", n+offset, err)
		return
	}
	self.ColorTableId = pio.I16BE(b[n:])
	n += 2
	for n+8 < len(b) {
		tag := Tag(pio.U32BE(b[n+4:]))
		size := int(pio.U32BE(b[n:]))
		if len(b) < n+size {
			err = parseErr("TagSizeInvalid", n+offset, err)
			return
		}
		switch tag {
		case HVCC:
			{
				atom := &HV1Conf{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("hvcC", n+offset, err)
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

func (self HV1Desc) Children() (r []Atom) {
	if self.Conf != nil {
		r = append(r, self.Conf)
	}
	r = append(r, self.Unknowns...)
	return
}
