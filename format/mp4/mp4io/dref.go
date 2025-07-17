package mp4io

import "github.com/ugparu/gomedia/utils/bits/pio"

const DREF = Tag(0x64726566)

func (self DataRefer) Tag() Tag {
	return DREF
}

type DataRefer struct {
	Version uint8
	Flags   uint32
	Url     *DataReferUrl
	AtomPos
}

func (self DataRefer) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(DREF))
	n += self.marshal(b[8:]) + 8
	pio.PutU32BE(b[0:], uint32(n))
	return
}
func (self DataRefer) marshal(b []byte) (n int) {
	pio.PutU8(b[n:], self.Version)
	n += 1
	pio.PutU24BE(b[n:], self.Flags)
	n += 3
	_childrenNR := 0
	if self.Url != nil {
		_childrenNR++
	}
	pio.PutI32BE(b[n:], int32(_childrenNR))
	n += 4
	if self.Url != nil {
		n += self.Url.Marshal(b[n:])
	}
	return
}
func (self DataRefer) Len() (n int) {
	n += 8
	n += 1
	n += 3
	n += 4
	if self.Url != nil {
		n += self.Url.Len()
	}
	return
}
func (self *DataRefer) Unmarshal(b []byte, offset int) (n int, err error) {
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
	n += 4
	for n+8 < len(b) {
		tag := Tag(pio.U32BE(b[n+4:]))
		size := int(pio.U32BE(b[n:]))
		if len(b) < n+size {
			err = parseErr("TagSizeInvalid", n+offset, err)
			return
		}
		switch tag {
		case URL:
			{
				atom := &DataReferUrl{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("url ", n+offset, err)
					return
				}
				self.Url = atom
			}
		}
		n += size
	}
	return
}
func (self DataRefer) Children() (r []Atom) {
	if self.Url != nil {
		r = append(r, self.Url)
	}
	return
}
