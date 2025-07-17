package mp4io

import "github.com/ugparu/gomedia/utils/bits/pio"

const STBL = Tag(0x7374626c)

func (self SampleTable) Tag() Tag {
	return STBL
}

type SampleTable struct {
	SampleDesc        *SampleDesc
	TimeToSample      *TimeToSample
	CompositionOffset *CompositionOffset
	SampleToChunk     *SampleToChunk
	SyncSample        *SyncSample
	ChunkOffset       *ChunkOffset
	SampleSize        *SampleSize
	AtomPos
}

func (self SampleTable) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(STBL))
	n += self.marshal(b[8:]) + 8
	pio.PutU32BE(b[0:], uint32(n))
	return
}
func (self SampleTable) marshal(b []byte) (n int) {
	if self.SampleDesc != nil {
		n += self.SampleDesc.Marshal(b[n:])
	}
	if self.TimeToSample != nil {
		n += self.TimeToSample.Marshal(b[n:])
	}
	if self.CompositionOffset != nil {
		n += self.CompositionOffset.Marshal(b[n:])
	}
	if self.SampleToChunk != nil {
		n += self.SampleToChunk.Marshal(b[n:])
	}
	if self.SyncSample != nil {
		n += self.SyncSample.Marshal(b[n:])
	}
	if self.ChunkOffset != nil {
		n += self.ChunkOffset.Marshal(b[n:])
	}
	if self.SampleSize != nil {
		n += self.SampleSize.Marshal(b[n:])
	}
	return
}
func (self SampleTable) Len() (n int) {
	n += 8
	if self.SampleDesc != nil {
		n += self.SampleDesc.Len()
	}
	if self.TimeToSample != nil {
		n += self.TimeToSample.Len()
	}
	if self.CompositionOffset != nil {
		n += self.CompositionOffset.Len()
	}
	if self.SampleToChunk != nil {
		n += self.SampleToChunk.Len()
	}
	if self.SyncSample != nil {
		n += self.SyncSample.Len()
	}
	if self.ChunkOffset != nil {
		n += self.ChunkOffset.Len()
	}
	if self.SampleSize != nil {
		n += self.SampleSize.Len()
	}
	return
}
func (self *SampleTable) Unmarshal(b []byte, offset int) (n int, err error) {
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
		case STSD:
			{
				atom := &SampleDesc{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("stsd", n+offset, err)
					return
				}
				self.SampleDesc = atom
			}
		case STTS:
			{
				atom := &TimeToSample{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("stts", n+offset, err)
					return
				}
				self.TimeToSample = atom
			}
		case CTTS:
			{
				atom := &CompositionOffset{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("ctts", n+offset, err)
					return
				}
				self.CompositionOffset = atom
			}
		case STSC:
			{
				atom := &SampleToChunk{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("stsc", n+offset, err)
					return
				}
				self.SampleToChunk = atom
			}
		case STSS:
			{
				atom := &SyncSample{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("stss", n+offset, err)
					return
				}
				self.SyncSample = atom
			}
		case STCO:
			{
				atom := &ChunkOffset{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("stco", n+offset, err)
					return
				}
				self.ChunkOffset = atom
			}
		case STSZ:
			{
				atom := &SampleSize{}
				if _, err = atom.Unmarshal(b[n:n+size], offset+n); err != nil {
					err = parseErr("stsz", n+offset, err)
					return
				}
				self.SampleSize = atom
			}
		}
		n += size
	}
	return
}
func (self SampleTable) Children() (r []Atom) {
	if self.SampleDesc != nil {
		r = append(r, self.SampleDesc)
	}
	if self.TimeToSample != nil {
		r = append(r, self.TimeToSample)
	}
	if self.CompositionOffset != nil {
		r = append(r, self.CompositionOffset)
	}
	if self.SampleToChunk != nil {
		r = append(r, self.SampleToChunk)
	}
	if self.SyncSample != nil {
		r = append(r, self.SyncSample)
	}
	if self.ChunkOffset != nil {
		r = append(r, self.ChunkOffset)
	}
	if self.SampleSize != nil {
		r = append(r, self.SampleSize)
	}
	return
}
