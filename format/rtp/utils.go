package rtp

import "encoding/binary"

// writeSizePrefix writes a big-endian uint32 length prefix into buf at offset.
// Used to embed a 4-byte NAL-unit size header directly into the ring slab,
// eliminating the heap allocation that the old binSize helper caused.
func writeSizePrefix(buf []byte, offset, n int) {
	binary.BigEndian.PutUint32(buf[offset:], uint32(n)) //nolint:gosec
}
