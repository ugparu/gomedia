// nolint: all
package mp4io

import "github.com/ugparu/gomedia/utils/bits/pio"

const FREE = Tag(0x66726565)
const freeSize = 8

type FreeType struct {
	AtomPos
}

func (*FreeType) Tag() Tag {
	return FREE
}

func (*FreeType) Marshal(b []byte) (n int) {
	pio.PutU32BE(b, freeSize)
	pio.PutU32BE(b[4:], uint32(FREE))
	return freeSize
}

func (*FreeType) Len() int {
	return freeSize
}

func (f *FreeType) Unmarshal(b []byte, offset int) (n int, err error) {
	n = len(b)
	f.AtomPos.setPos(offset, n)
	return n, nil
}

func (*FreeType) Children() []Atom {
	return nil
}
