// nolint: all
package mp4io

import (
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"github.com/ugparu/gomedia/utils/bits/pio"
)

// Constants representing flags for sample properties.
const (
	SampleIsNonSync       uint32 = 0x00010000
	SampleHasDependencies uint32 = 0x01000000
	SampleNoDependencies  uint32 = 0x02000000

	SampleNonKeyframe = SampleHasDependencies | SampleIsNonSync

	HeaderSize = 8
)

func GetTime32(b []byte) (t time.Time) {
	sec := pio.U32BE(b)
	t = time.Date(1904, time.January, 1, 0, 0, 0, 0, time.UTC)
	t = t.Add(time.Second * time.Duration(sec))
	return
}

func PutTime32(b []byte, t time.Time) {
	dur := t.Sub(time.Date(1904, time.January, 1, 0, 0, 0, 0, time.UTC))
	sec := uint32(dur / time.Second)
	pio.PutU32BE(b, sec)
}

func GetTime64(b []byte) (t time.Time) {
	sec := pio.U64BE(b)
	t = time.Date(1904, time.January, 1, 0, 0, 0, 0, time.UTC)
	t = t.Add(time.Second * time.Duration(sec))
	return
}

func PutTime64(b []byte, t time.Time) {
	dur := t.Sub(time.Date(1904, time.January, 1, 0, 0, 0, 0, time.UTC))
	sec := uint64(dur / time.Second)
	pio.PutU64BE(b, sec)
}

func PutFixed16(b []byte, f float64) {
	intpart, fracpart := math.Modf(f)
	b[0] = uint8(intpart)
	b[1] = uint8(fracpart * 256.0)
}

func GetFixed16(b []byte) float64 {
	return float64(b[0]) + float64(b[1])/256.0
}

func PutFixed32(b []byte, f float64) {
	intpart, fracpart := math.Modf(f)
	pio.PutU16BE(b[0:2], uint16(intpart))
	pio.PutU16BE(b[2:4], uint16(fracpart*65536.0))
}

func GetFixed32(b []byte) float64 {
	return float64(pio.U16BE(b[0:2])) + float64(pio.U16BE(b[2:4]))/65536.0
}

type Tag uint32

func (self Tag) String() string {
	var b [4]byte
	pio.PutU32BE(b[:], uint32(self))
	for i := 0; i < 4; i++ {
		if b[i] == 0 {
			b[i] = ' '
		}
	}
	return string(b[:])
}

type Atom interface {
	Pos() (int, int)
	Tag() Tag
	Marshal([]byte) int
	Unmarshal([]byte, int) (int, error)
	Len() int
	Children() []Atom
}

type AtomPos struct {
	Offset int
	Size   int
}

func (self AtomPos) Pos() (int, int) {
	return self.Offset, self.Size
}

func (self *AtomPos) setPos(offset int, size int) {
	self.Offset, self.Size = offset, size
}

type Dummy struct {
	Data []byte
	Tag_ Tag
	AtomPos
}

func (self Dummy) Children() []Atom {
	return nil
}

func (self Dummy) Tag() Tag {
	return self.Tag_
}

func (self Dummy) Len() int {
	return len(self.Data)
}

func (self Dummy) Marshal(b []byte) int {
	copy(b, self.Data)
	return len(self.Data)
}

func (self *Dummy) Unmarshal(b []byte, offset int) (n int, err error) {
	(&self.AtomPos).setPos(offset, len(b))
	self.Data = b
	n = len(b)
	return
}

func StringToTag(tag string) Tag {
	var b [4]byte
	copy(b[:], []byte(tag))
	return Tag(pio.U32BE(b[:]))
}

func FindChildrenByName(root Atom, tag string) Atom {
	return FindChildren(root, StringToTag(tag))
}

func FindChildren(root Atom, tag Tag) Atom {
	if root.Tag() == tag {
		return root
	}
	for _, child := range root.Children() {
		if r := FindChildren(child, tag); r != nil {
			return r
		}
	}
	return nil
}

func ReadFileAtoms(r io.ReadSeeker) (atoms []Atom, err error) {
	for {
		offset, _ := r.Seek(0, 1)
		taghdr := make([]byte, 8)
		if _, err = io.ReadFull(r, taghdr); err != nil {
			if err == io.EOF {
				err = nil
			}
			return
		}
		size := int(pio.U32BE(taghdr[0:]))
		tag := Tag(pio.U32BE(taghdr[4:]))
		isExtendedSize := tag == MDAT && size == 1

		if isExtendedSize {
			sBuf := make([]byte, 8)
			if _, err = io.ReadFull(r, sBuf); err != nil {
				return
			}
			size = int(pio.I64BE(sBuf))
		}

		var atom Atom
		switch tag {
		case MOOV:
			atom = &Movie{}
		case MOOF:
			atom = &MovieFrag{}
		}

		if atom != nil {
			b := make([]byte, int(size))
			if _, err = io.ReadFull(r, b[8:]); err != nil {
				return
			}
			copy(b, taghdr)
			if _, err = atom.Unmarshal(b, int(offset)); err != nil {
				return
			}
			atoms = append(atoms, atom)
		} else {
			dummy := &Dummy{Tag_: tag}
			dummy.setPos(int(offset), int(size))
			atoms = append(atoms, dummy)
			seek := int64(size) - 8
			if isExtendedSize {
				seek -= 8
			}
			if _, err = r.Seek(seek, 1); err != nil {
				return
			}
		}
	}
}

func printatom(out io.Writer, root Atom, depth int) {
	offset, size := root.Pos()

	type stringintf interface {
		String() string
	}

	fmt.Fprintf(out,
		"%s%s offset=%d size=%d",
		strings.Repeat(" ", depth*2), root.Tag(), offset, size,
	)
	if str, ok := root.(stringintf); ok {
		fmt.Fprint(out, " ", str.String())
	}

	children := root.Children()
	for _, child := range children {
		printatom(out, child, depth+1)
	}
}

func FprintAtom(out io.Writer, root Atom) {
	printatom(out, root, 0)
}

func PrintAtom(root Atom) {
	FprintAtom(os.Stdout, root)
}

func (self TimeToSample) String() string {
	return fmt.Sprintf("entries=%d", len(self.Entries))
}

func (self SampleToChunk) String() string {
	return fmt.Sprintf("entries=%d", len(self.Entries))
}

func (self SampleSize) String() string {
	return fmt.Sprintf("entries=%d", len(self.Entries))
}

func (self SyncSample) String() string {
	return fmt.Sprintf("entries=%d", len(self.Entries))
}

func (self CompositionOffset) String() string {
	return fmt.Sprintf("entries=%d", len(self.Entries))
}

func (self ChunkOffset) String() string {
	return fmt.Sprintf("entries=%d", len(self.Entries))
}

func (self *Track) GetAVC1Conf() (conf *AVC1Conf) {
	atom := FindChildren(self, AVCC)
	conf, _ = atom.(*AVC1Conf)
	return
}

func (self *Track) GetHV1Conf() (conf *HV1Conf) {
	atom := FindChildren(self, HVCC)
	conf, _ = atom.(*HV1Conf)
	return
}

func (self *Track) GetMJPGDesc() (desc *MJPGDesc) {
	atom := FindChildren(self, MJPG)
	desc, _ = atom.(*MJPGDesc)
	return
}

func (self *Track) GetElemStreamDesc() (esds *ElemStreamDesc) {
	atom := FindChildren(self, ESDS)
	esds, _ = atom.(*ElemStreamDesc)
	return
}
