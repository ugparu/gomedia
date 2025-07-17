package mp4io

import "github.com/ugparu/gomedia/utils/bits/pio"

const MOOF = Tag(0x6d6f6f66)

func (self MovieFrag) Tag() Tag {
	return MOOF
}

type MovieFrag struct {
	Header   *MovieFragHeader
	Tracks   []*TrackFrag
	Unknowns []Atom
	AtomPos
}

func (self MovieFrag) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(MOOF))
	n += self.marshal(b[8:]) + 8
	pio.PutU32BE(b[0:], uint32(n))
	return
}

func (self MovieFrag) marshal(b []byte) (n int) {
	if self.Header != nil {
		n += self.Header.Marshal(b[n:])
	}
	for _, atom := range self.Tracks {
		n += atom.Marshal(b[n:])
	}
	for _, atom := range self.Unknowns {
		n += atom.Marshal(b[n:])
	}
	return
}

func (self MovieFrag) Len() (n int) {
	n += 8
	if self.Header != nil {
		n += self.Header.Len()
	}
	for _, atom := range self.Tracks {
		n += atom.Len()
	}
	for _, atom := range self.Unknowns {
		n += atom.Len()
	}
	return
}

func (self *MovieFrag) Unmarshal(b []byte, offset int) (n int, err error) {
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
		case MFHD:
			{
				atom := &MovieFragHeader{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("mfhd", n+offset, err)
					return
				}
				self.Header = atom
			}
		case TRAF:
			{
				atom := &TrackFrag{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("traf", n+offset, err)
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

func (self MovieFrag) Children() (r []Atom) {
	if self.Header != nil {
		r = append(r, self.Header)
	}
	for _, atom := range self.Tracks {
		r = append(r, atom)
	}
	r = append(r, self.Unknowns...)
	return
}
