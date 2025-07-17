package mp4io

import "github.com/ugparu/gomedia/utils/bits/pio"

const STSD = Tag(0x73747364)

func (self SampleDesc) Tag() Tag {
	return STSD
}

type SampleDesc struct {
	Version  uint8
	AVC1Desc *AVC1Desc
	HV1Desc  *HV1Desc
	MJPGDesc *MJPGDesc
	MP4ADesc *MP4ADesc
	Unknowns []Atom
	AtomPos
}

func (self SampleDesc) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(STSD))
	n += self.marshal(b[8:]) + 8
	pio.PutU32BE(b[0:], uint32(n))
	return
}
func (self SampleDesc) marshal(b []byte) (n int) {
	pio.PutU8(b[n:], self.Version)
	n += 1
	n += 3
	_childrenNR := 0
	if self.AVC1Desc != nil {
		_childrenNR++
	}
	if self.HV1Desc != nil {
		_childrenNR++
	}
	if self.MJPGDesc != nil {
		_childrenNR++
	}
	if self.MP4ADesc != nil {
		_childrenNR++
	}
	_childrenNR += len(self.Unknowns)
	pio.PutI32BE(b[n:], int32(_childrenNR))
	n += 4
	if self.AVC1Desc != nil {
		n += self.AVC1Desc.Marshal(b[n:])
	}
	if self.HV1Desc != nil {
		n += self.HV1Desc.Marshal(b[n:])
	}
	if self.MJPGDesc != nil {
		n += self.MJPGDesc.Marshal(b[n:])
	}
	if self.MP4ADesc != nil {
		n += self.MP4ADesc.Marshal(b[n:])
	}
	for _, atom := range self.Unknowns {
		n += atom.Marshal(b[n:])
	}
	return
}
func (self SampleDesc) Len() (n int) {
	n += 8
	n += 1
	n += 3
	n += 4
	if self.AVC1Desc != nil {
		n += self.AVC1Desc.Len()
	}
	if self.HV1Desc != nil {
		n += self.HV1Desc.Len()
	}
	if self.MJPGDesc != nil {
		n += self.MJPGDesc.Len()
	}
	if self.MP4ADesc != nil {
		n += self.MP4ADesc.Len()
	}
	for _, atom := range self.Unknowns {
		n += atom.Len()
	}
	return
}
func (self *SampleDesc) Unmarshal(b []byte, offset int) (n int, err error) {
	(&self.AtomPos).setPos(offset, len(b))
	n += 8
	if len(b) < n+1 {
		err = parseErr("Version", n+offset, err)
		return
	}
	self.Version = pio.U8(b[n:])
	n += 1
	n += 3
	n += 4
	for n+8 < len(b) {
		tag := Tag(pio.U32BE(b[n+4:]))
		size := int(pio.U32BE(b[n:]))
		if len(b) < n+size {
			err = parseErr("TagSizeInvalid", n+offset, err)
			return
		}
		switch tag {
		case AVC1:
			{
				atom := &AVC1Desc{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("avc1", n+offset, err)
					return
				}
				self.AVC1Desc = atom
			}
		case HEV1:
			{
				atom := &HV1Desc{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("hec1", n+offset, err)
					return
				}
				self.HV1Desc = atom
			}
		case MJPG:
			{
				atom := &MJPGDesc{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("mjpg", n+offset, err)
					return
				}
				self.MJPGDesc = atom
			}
		case MP4A:
			{
				atom := &MP4ADesc{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("mp4a", n+offset, err)
					return
				}
				self.MP4ADesc = atom
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
func (self SampleDesc) Children() (r []Atom) {
	if self.AVC1Desc != nil {
		r = append(r, self.AVC1Desc)
	}
	if self.HV1Desc != nil {
		r = append(r, self.HV1Desc)
	}
	if self.MJPGDesc != nil {
		r = append(r, self.MJPGDesc)
	}
	if self.MP4ADesc != nil {
		r = append(r, self.MP4ADesc)
	}
	r = append(r, self.Unknowns...)
	return
}
