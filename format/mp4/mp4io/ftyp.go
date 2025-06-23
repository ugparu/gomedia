// nolint: all
package mp4io

import "github.com/ugparu/gomedia/utils/bits/pio"

const (
	FTYP                = Tag(0x66747970)
	baseFtypSize        = 16
	bytesPerBrand       = 4
	defaultMinorVersion = 0x200
)

func NewFileType() *FileType {
	return &FileType{
		MajorBrand:   pio.U32BE([]byte("isom")),
		MinorVersion: defaultMinorVersion,
		CompatibleBrands: []uint32{
			pio.U32BE([]byte("iso6")),
			pio.U32BE([]byte("avc1")),
			pio.U32BE([]byte("mp41")),
			pio.U32BE([]byte("mp70")),
		},
		AtomPos: AtomPos{
			Offset: 0,
			Size:   0,
		},
	}
}

type FileType struct {
	MajorBrand       uint32
	MinorVersion     uint32
	CompatibleBrands []uint32
	AtomPos
}

func (*FileType) Tag() Tag {
	return FTYP
}

func (f *FileType) Marshal(b []byte) (n int) {
	l := baseFtypSize + bytesPerBrand*len(f.CompatibleBrands)
	pio.PutU32BE(b, uint32(l))
	pio.PutU32BE(b[4:], uint32(FTYP))
	pio.PutU32BE(b[8:], f.MajorBrand)
	pio.PutU32BE(b[12:], f.MinorVersion)
	for i, v := range f.CompatibleBrands {
		pio.PutU32BE(b[baseFtypSize+bytesPerBrand*i:], v)
	}
	return l
}

func (f *FileType) Len() int {
	return baseFtypSize + bytesPerBrand*len(f.CompatibleBrands)
}

func (f *FileType) Unmarshal(b []byte, offset int) (n int, err error) {
	f.AtomPos.setPos(offset, len(b))
	n = 8
	if len(b) < n+8 {
		return 0, parseErr("MajorBrand", offset+n, nil)
	}
	f.MajorBrand = pio.U32BE(b[n:])
	n += 4
	f.MinorVersion = pio.U32BE(b[n:])
	n += 4
	for n < len(b)-3 {
		f.CompatibleBrands = append(f.CompatibleBrands, pio.U32BE(b[n:]))
		n += 4
	}
	return
}

func (*FileType) Children() []Atom {
	return nil
}
