package mp4io

import "github.com/ugparu/gomedia/utils/bits/pio"

const MINF = Tag(0x6d696e66)

func (self MediaInfo) Tag() Tag {
	return MINF
}

type MediaInfo struct {
	Sound    *SoundMediaInfo
	Video    *VideoMediaInfo
	Data     *DataInfo
	Sample   *SampleTable
	Unknowns []Atom
	AtomPos
}

func (self MediaInfo) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(MINF))
	n += self.marshal(b[8:]) + 8
	pio.PutU32BE(b[0:], uint32(n))
	return
}
func (self MediaInfo) marshal(b []byte) (n int) {
	if self.Sound != nil {
		n += self.Sound.Marshal(b[n:])
	}
	if self.Video != nil {
		n += self.Video.Marshal(b[n:])
	}
	if self.Data != nil {
		n += self.Data.Marshal(b[n:])
	}
	if self.Sample != nil {
		n += self.Sample.Marshal(b[n:])
	}
	for _, atom := range self.Unknowns {
		n += atom.Marshal(b[n:])
	}
	return
}
func (self MediaInfo) Len() (n int) {
	n += 8
	if self.Sound != nil {
		n += self.Sound.Len()
	}
	if self.Video != nil {
		n += self.Video.Len()
	}
	if self.Data != nil {
		n += self.Data.Len()
	}
	if self.Sample != nil {
		n += self.Sample.Len()
	}
	for _, atom := range self.Unknowns {
		n += atom.Len()
	}
	return
}
func (self *MediaInfo) Unmarshal(b []byte, offset int) (n int, err error) {
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
		case SMHD:
			{
				atom := &SoundMediaInfo{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("smhd", n+offset, err)
					return
				}
				self.Sound = atom
			}
		case VMHD:
			{
				atom := &VideoMediaInfo{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("vmhd", n+offset, err)
					return
				}
				self.Video = atom
			}
		case DINF:
			{
				atom := &DataInfo{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("dinf", n+offset, err)
					return
				}
				self.Data = atom
			}
		case STBL:
			{
				atom := &SampleTable{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("stbl", n+offset, err)
					return
				}
				self.Sample = atom
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
func (self MediaInfo) Children() (r []Atom) {
	if self.Sound != nil {
		r = append(r, self.Sound)
	}
	if self.Video != nil {
		r = append(r, self.Video)
	}
	if self.Data != nil {
		r = append(r, self.Data)
	}
	if self.Sample != nil {
		r = append(r, self.Sample)
	}
	r = append(r, self.Unknowns...)
	return
}
