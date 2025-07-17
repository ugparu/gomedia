package mp4io

import "github.com/ugparu/gomedia/utils/bits/pio"

const DINF = Tag(0x64696e66)

func (self DataInfo) Tag() Tag {
	return DINF
}

type DataInfo struct {
	Refer    *DataRefer
	Unknowns []Atom
	AtomPos
}

func (self DataInfo) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(DINF))
	n += self.marshal(b[8:]) + 8
	pio.PutU32BE(b[0:], uint32(n))
	return
}
func (self DataInfo) marshal(b []byte) (n int) {
	if self.Refer != nil {
		n += self.Refer.Marshal(b[n:])
	}
	for _, atom := range self.Unknowns {
		n += atom.Marshal(b[n:])
	}
	return
}
func (self DataInfo) Len() (n int) {
	n += 8
	if self.Refer != nil {
		n += self.Refer.Len()
	}
	for _, atom := range self.Unknowns {
		n += atom.Len()
	}
	return
}
func (self *DataInfo) Unmarshal(b []byte, offset int) (n int, err error) {
	(&self.AtomPos).setPos(offset, len(b))
	n += 8
	for n+8 < len(b) {
		tag := Tag(pio.U32BE(b[n+4:]))
		size := int(pio.U32BE(b[n:]))
		if len(b) < n+size {
			err = parseErr("TagSizeInvalid", n+offset, err)
			return
		}
		switch tag {
		case DREF:
			{
				atom := &DataRefer{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("dref", n+offset, err)
					return
				}
				self.Refer = atom
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
func (self DataInfo) Children() (r []Atom) {
	if self.Refer != nil {
		r = append(r, self.Refer)
	}
	r = append(r, self.Unknowns...)
	return
}
