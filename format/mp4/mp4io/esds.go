package mp4io

import (
	"github.com/ugparu/gomedia/utils/bits/pio"
)

const (
	MP4ESDescrTag          = 3
	MP4DecConfigDescrTag   = 4
	MP4DecSpecificDescrTag = 5
	MP4SLConfigDescrTag    = 6
)

const ESDS = Tag(0x65736473)

type ElemStreamDesc struct {
	TrackId    uint16
	MaxBitrate uint32
	AvgBitrate uint32
	DecConfig  []byte
	AtomPos
}

func (esds ElemStreamDesc) Tag() Tag {
	return ESDS
}

func (esds ElemStreamDesc) Children() []Atom {
	return nil
}

func (esds ElemStreamDesc) fillLength(b []byte, length int) (n int) {
	for i := 3; i > 0; i-- {
		b[n] = uint8(length>>uint(7*i))&0x7f | 0x80
		n++
	}
	b[n] = uint8(length & 0x7f)
	n++
	return
}

func (esds ElemStreamDesc) Len() (n int) {
	return 43 + len(esds.DecConfig)
}

func encodeLength(v uint32) []byte {
	b := make([]byte, 4)
	b[3] = byte(v & 0x7F) // младшие 7 бит
	b[2] = 0x80 | byte((v>>7)&0x7F)
	b[1] = 0x80 | byte((v>>14)&0x7F)
	b[0] = 0x80 | byte((v>>21)&0x7F)
	return b
}

func (esds ElemStreamDesc) Marshal(b []byte) (n int) {
	pio.PutU32BE(b[4:], uint32(ESDS))
	n += 8
	pio.PutU32BE(b[n:], 0) // Version
	n += 4

	pio.PutU8(b[n:], MP4ESDescrTag)
	n++

	length := encodeLength(39)
	copy(b[n:], length)
	n += len(length)

	pio.PutU16BE(b[n:], esds.TrackId)
	n += 2

	// flags
	n++

	pio.PutU8(b[n:], MP4DecConfigDescrTag)
	n++

	length = encodeLength(31)
	copy(b[n:], length)
	n += len(length)

	pio.PutU8(b[n:], 0x40) // objectid (AAC)
	n++
	pio.PutU8(b[n:], 0x15) // streamtype
	n++

	// buffer size db
	n += 3

	// max bitrate
	pio.PutU32BE(b[n:], esds.MaxBitrate)
	n += 4
	// avg bitrate
	pio.PutU32BE(b[n:], esds.AvgBitrate)
	n += 4

	// DecSpecific Descriptor
	pio.PutU8(b[n:], MP4DecSpecificDescrTag)
	n++

	length = encodeLength(13)
	copy(b[n:], length)
	n += len(length)

	copy(b[n:], esds.DecConfig)
	n += len(esds.DecConfig)

	pio.PutU32BE(b[0:], uint32(n))
	return
}

func (esds *ElemStreamDesc) Unmarshal(b []byte, offset int) (n int, err error) {
	if len(b) < n+12 {
		err = parseErr("hdr", offset+n, err)
		return
	}
	(&esds.AtomPos).setPos(offset, len(b))
	n += 8
	n += 4
	return esds.parseDesc(b[n:], offset+n)
}

func (esds *ElemStreamDesc) parseDesc(b []byte, offset int) (n int, err error) {
	var hdrlen int
	var datalen int
	var tag uint8
	if hdrlen, tag, datalen, err = esds.parseDescHdr(b, offset); err != nil {
		return
	}
	n += hdrlen

	if len(b) < n+datalen {
		err = parseErr("datalen", offset+n, err)
		return
	}

	switch tag {
	case MP4ESDescrTag:
		if len(b) < n+3 {
			err = parseErr("MP4ESDescrTag", offset+n, err)
			return
		}
		if _, err = esds.parseDesc(b[n+3:], offset+n+3); err != nil {
			return
		}

	case MP4DecConfigDescrTag:
		const size = 2 + 3 + 4 + 4
		if len(b) < n+size {
			err = parseErr("MP4DecSpecificDescrTag", offset+n, err)
			return
		}
		if _, err = esds.parseDesc(b[n+size:], offset+n+size); err != nil {
			return
		}

	case MP4DecSpecificDescrTag:
		esds.DecConfig = b[n : n+datalen]
	}

	n += datalen
	return
}

func (esds *ElemStreamDesc) parseLength(b []byte, offset int) (n int, length int, err error) {
	for n < 4 {
		if len(b) < n+1 {
			err = parseErr("len", offset+n, err)
			return
		}
		c := b[n]
		n++
		length = (length << 7) | (int(c) & 0x7f)
		if c&0x80 == 0 {
			break
		}
	}
	return
}

func (esds *ElemStreamDesc) parseDescHdr(b []byte, offset int) (n int, tag uint8, datalen int, err error) {
	if len(b) < n+1 {
		err = parseErr("tag", offset+n, err)
		return
	}
	tag = b[n]
	n++
	var lenlen int
	if lenlen, datalen, err = esds.parseLength(b[n:], offset+n); err != nil {
		return
	}
	n += lenlen
	return
}
