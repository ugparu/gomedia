package mp4io

import "github.com/ugparu/gomedia/utils/bits/pio"

const MOOV = Tag(0x6d6f6f76)

func (m Movie) Tag() Tag {
	return MOOV
}

type Movie struct {
	Header      *MovieHeader
	MovieExtend *MovieExtend
	Tracks      []*Track
	Unknowns    []Atom
	AtomPos
}

func (m Movie) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(MOOV))
	n += m.marshal(b[8:]) + 8
	pio.PutU32BE(b[0:], uint32(n))
	return
}

func (m Movie) marshal(b []byte) (n int) {
	if m.Header != nil {
		n += m.Header.Marshal(b[n:])
	}
	for _, atom := range m.Tracks {
		sz := atom.Marshal(b[n:])
		n += sz
	}
	if m.MovieExtend != nil {
		n += m.MovieExtend.Marshal(b[n:])
	}
	for _, atom := range m.Unknowns {
		n += atom.Marshal(b[n:])
	}
	return
}

func (m Movie) Len() (n int) {
	n += 8
	if m.Header != nil {
		n += m.Header.Len()
	}
	if m.MovieExtend != nil {
		n += m.MovieExtend.Len()
	}
	for _, atom := range m.Tracks {
		n += atom.Len()
	}
	for _, atom := range m.Unknowns {
		n += atom.Len()
	}
	return
}

func (m *Movie) Unmarshal(b []byte, offset int) (n int, err error) {
	(&m.AtomPos).setPos(offset, len(b))
	n += 8
	for n+8 < len(b) {
		tag := Tag(pio.U32BE(b[n+4:]))
		size := int(pio.U32BE(b[n:]))
		if len(b) < n+size {
			err = parseErr("TagSizeInvalid", n+offset, err)
			return
		}
		switch tag {
		case MVHD:
			{
				atom := &MovieHeader{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("mvhd", n+offset, err)
					return
				}
				m.Header = atom
			}
		case MVEX:
			{
				atom := &MovieExtend{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("mvex", n+offset, err)
					return
				}
				m.MovieExtend = atom
			}
		case TRAK:
			{
				atom := &Track{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("trak", n+offset, err)
					return
				}
				m.Tracks = append(m.Tracks, atom)
			}
		default:
			{
				atom := &Dummy{Tag_: tag, Data: b[n : n+size]}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("", n+offset, err)
					return
				}
				m.Unknowns = append(m.Unknowns, atom)
			}
		}
		n += size
	}
	return
}

func (m Movie) Children() (r []Atom) {
	if m.Header != nil {
		r = append(r, m.Header)
	}
	if m.MovieExtend != nil {
		r = append(r, m.MovieExtend)
	}
	for _, atom := range m.Tracks {
		r = append(r, atom)
	}
	r = append(r, m.Unknowns...)
	return
}
