package mp4io

import "github.com/ugparu/gomedia/utils/bits/pio"

const TRAK = Tag(0x7472616b)

func (self Track) Tag() Tag {
	return TRAK
}

type Track struct {
	Header   *TrackHeader
	Media    *Media
	Unknowns []Atom
	AtomPos
}

func (self Track) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(TRAK))
	n += self.marshal(b[8:]) + 8
	pio.PutU32BE(b[0:], uint32(n))
	return
}
func (self Track) marshal(b []byte) (n int) {
	if self.Header != nil {
		n += self.Header.Marshal(b[n:])
	}
	if self.Media != nil {
		n += self.Media.Marshal(b[n:])
	}
	for _, atom := range self.Unknowns {
		n += atom.Marshal(b[n:])
	}
	return
}
func (self Track) Len() (n int) {
	n += 8
	if self.Header != nil {
		n += self.Header.Len()
	}
	if self.Media != nil {
		n += self.Media.Len()
	}
	for _, atom := range self.Unknowns {
		n += atom.Len()
	}
	return
}
func (self *Track) Unmarshal(b []byte, offset int) (n int, err error) {
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
		case TKHD:
			{
				atom := &TrackHeader{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("tkhd", n+offset, err)
					return
				}
				self.Header = atom
			}
		case MDIA:
			{
				atom := &Media{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("mdia", n+offset, err)
					return
				}
				self.Media = atom
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
func (self Track) Children() (r []Atom) {
	if self.Header != nil {
		r = append(r, self.Header)
	}
	if self.Media != nil {
		r = append(r, self.Media)
	}
	r = append(r, self.Unknowns...)
	return
}
