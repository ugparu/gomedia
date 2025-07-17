package mp4io

import "github.com/ugparu/gomedia/utils/bits/pio"

const MDIA = Tag(0x6d646961)

func (self Media) Tag() Tag {
	return MDIA
}

type Media struct {
	Header   *MediaHeader
	Handler  *HandlerRefer
	Info     *MediaInfo
	Unknowns []Atom
	AtomPos
}

func (self Media) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(MDIA))
	n += self.marshal(b[8:]) + 8
	pio.PutU32BE(b[0:], uint32(n))
	return
}
func (self Media) marshal(b []byte) (n int) {
	if self.Header != nil {
		n += self.Header.Marshal(b[n:])
	}
	if self.Handler != nil {
		n += self.Handler.Marshal(b[n:])
	}
	if self.Info != nil {
		n += self.Info.Marshal(b[n:])
	}
	for _, atom := range self.Unknowns {
		n += atom.Marshal(b[n:])
	}
	return
}
func (self Media) Len() (n int) {
	n += 8
	if self.Header != nil {
		n += self.Header.Len()
	}
	if self.Handler != nil {
		n += self.Handler.Len()
	}
	if self.Info != nil {
		n += self.Info.Len()
	}
	for _, atom := range self.Unknowns {
		n += atom.Len()
	}
	return
}
func (self *Media) Unmarshal(b []byte, offset int) (n int, err error) {
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
		case MDHD:
			{
				atom := &MediaHeader{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("mdhd", n+offset, err)
					return
				}
				self.Header = atom
			}
		case HDLR:
			{
				atom := &HandlerRefer{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("hdlr", n+offset, err)
					return
				}
				self.Handler = atom
			}
		case MINF:
			{
				atom := &MediaInfo{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("minf", n+offset, err)
					return
				}
				self.Info = atom
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
func (self Media) Children() (r []Atom) {
	if self.Header != nil {
		r = append(r, self.Header)
	}
	if self.Handler != nil {
		r = append(r, self.Handler)
	}
	if self.Info != nil {
		r = append(r, self.Info)
	}
	r = append(r, self.Unknowns...)
	return
}
