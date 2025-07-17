package mp4io

import "github.com/ugparu/gomedia/utils/bits/pio"

const TRAF = Tag(0x74726166)

func (self TrackFrag) Tag() Tag {
	return TRAF
}

type TrackFrag struct {
	Header     *TrackFragHeader
	DecodeTime *TrackFragDecodeTime
	Run        *TrackFragRun
	Unknowns   []Atom
	AtomPos
}

func (self TrackFrag) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(TRAF))
	n += self.marshal(b[8:]) + 8
	pio.PutU32BE(b[0:], uint32(n))
	return
}
func (self TrackFrag) marshal(b []byte) (n int) {
	if self.Header != nil {
		n += self.Header.Marshal(b[n:])
	}
	if self.DecodeTime != nil {
		n += self.DecodeTime.Marshal(b[n:])
	}
	if self.Run != nil {
		n += self.Run.Marshal(b[n:])
	}
	for _, atom := range self.Unknowns {
		n += atom.Marshal(b[n:])
	}
	return
}
func (self TrackFrag) Len() (n int) {
	n += 8
	if self.Header != nil {
		n += self.Header.Len()
	}
	if self.DecodeTime != nil {
		n += self.DecodeTime.Len()
	}
	if self.Run != nil {
		n += self.Run.Len()
	}
	for _, atom := range self.Unknowns {
		n += atom.Len()
	}
	return
}
func (self *TrackFrag) Unmarshal(b []byte, offset int) (n int, err error) {
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
		case TFHD:
			{
				atom := &TrackFragHeader{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("tfhd", n+offset, err)
					return
				}
				self.Header = atom
			}
		case TFDT:
			{
				atom := &TrackFragDecodeTime{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("tfdt", n+offset, err)
					return
				}
				self.DecodeTime = atom
			}
		case TRUN:
			{
				atom := &TrackFragRun{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("trun", n+offset, err)
					return
				}
				self.Run = atom
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
func (self TrackFrag) Children() (r []Atom) {
	if self.Header != nil {
		r = append(r, self.Header)
	}
	if self.DecodeTime != nil {
		r = append(r, self.DecodeTime)
	}
	if self.Run != nil {
		r = append(r, self.Run)
	}
	r = append(r, self.Unknowns...)
	return
}
