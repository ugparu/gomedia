package rtp

import (
	"encoding/binary"
)

func binSize(val int) []byte {
	buf := make([]byte, headerSize)
	binary.BigEndian.PutUint32(buf, uint32(val)) //nolint:gosec
	return buf
}
