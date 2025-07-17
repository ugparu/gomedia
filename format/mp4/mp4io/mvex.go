package mp4io

import "github.com/ugparu/gomedia/utils/bits/pio"

const MVEX = Tag(0x6d766578)

func (self MovieExtend) Tag() Tag {
	return MVEX
}

type MovieExtend struct {
	Tracks   []*TrackExtend
	Unknowns []Atom
	AtomPos
}

func (self MovieExtend) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(MVEX))
	n += self.marshal(b[8:]) + 8
	pio.PutU32BE(b[0:], uint32(n))
	return
}
func (self MovieExtend) marshal(b []byte) (n int) {
	for _, atom := range self.Tracks {
		n += atom.Marshal(b[n:])
	}
	for _, atom := range self.Unknowns {
		n += atom.Marshal(b[n:])
	}
	return
}
func (self MovieExtend) Len() (n int) {
	n += 8
	for _, atom := range self.Tracks {
		n += atom.Len()
	}
	for _, atom := range self.Unknowns {
		n += atom.Len()
	}
	return
}
func (self *MovieExtend) Unmarshal(b []byte, offset int) (n int, err error) {
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
		case TREX:
			{
				atom := &TrackExtend{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("trex", n+offset, err)
					return
				}
				self.Tracks = append(self.Tracks, atom)
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
func (self MovieExtend) Children() (r []Atom) {
	for _, atom := range self.Tracks {
		r = append(r, atom)
	}
	r = append(r, self.Unknowns...)
	return
}
