// Package bits provides a bit-level Reader and Writer for parsing and emitting
// bitstreams that are not aligned to byte boundaries (codec configuration
// descriptors, ADTS headers, AAC AudioSpecificConfig, etc.).
package bits

import (
	"io"
)

// Reader reads an arbitrary number of bits from an underlying io.Reader.
// Bits are consumed MSB-first: the first call to ReadBits returns the most
// significant bits of the first byte read from R.
type Reader struct {
	R    io.Reader
	n    int    // number of valid bits currently buffered in `bits`
	bits uint64 // right-aligned bit buffer
}

// ReadBits64 reads the next n bits (0 <= n <= 64) and returns them right-aligned.
func (r *Reader) ReadBits64(n int) (bits uint64, err error) {
	if r.n < n {
		var b [8]byte
		var got int
		want := (n - r.n + 7) / 8 //nolint:mnd // round up to whole bytes
		if got, err = r.R.Read(b[:want]); err != nil {
			return
		}
		if got < want {
			err = io.EOF
			return
		}
		for i := 0; i < got; i++ {
			r.bits <<= 8 //nolint:mnd // shift one byte into the buffer
			r.bits |= uint64(b[i])
		}
		r.n += got * 8 //nolint:mnd // 8 bits per byte
	}
	bits = r.bits >> uint(r.n-n)
	r.bits ^= bits << uint(r.n-n)
	r.n -= n
	return
}

// ReadBits reads the next n bits and returns them right-aligned in a uint.
func (r *Reader) ReadBits(n int) (bits uint, err error) {
	var bits64 uint64
	if bits64, err = r.ReadBits64(n); err != nil {
		return
	}
	bits = uint(bits64)
	return
}

// Read fills p byte-by-byte from the bitstream, re-aligning on byte boundaries.
// This satisfies io.Reader so the bit reader can be chained with byte-oriented
// decoders once the non-aligned header has been consumed.
func (r *Reader) Read(p []byte) (n int, err error) {
	for n < len(p) {
		const chunk = 8 // bytes per inner ReadBits64 call
		want := min(chunk, len(p)-n)
		var bits uint64
		if bits, err = r.ReadBits64(want * 8); err != nil { //nolint:mnd // bits per byte
			break
		}
		for i := range want {
			p[n+i] = byte(bits >> uint((want-i-1)*8)) //nolint:mnd // extract byte i, MSB first
		}
		n += want
	}
	return
}

// Writer writes an arbitrary number of bits to an underlying io.Writer.
// Bits are emitted MSB-first and buffered until a byte boundary is reached;
// call FlushBits after the last write to emit the trailing partial byte.
type Writer struct {
	W    io.Writer
	n    int    // number of valid bits currently buffered in `bits`
	bits uint64 // left-to-right-packed bit buffer
}

// WriteBits64 appends the low n bits of `bits` to the stream.
// The 64-bit buffer is flushed to W whenever a subsequent write would overflow it.
func (w *Writer) WriteBits64(bits uint64, n int) (err error) {
	const bufBits = 64
	if w.n+n > bufBits {
		// Split the write: upper `move` bits fill the current buffer and get flushed;
		// the remaining `n - move` bits are packed into the freshly-empty buffer.
		move := uint(bufBits - w.n)
		mask := bits >> move
		w.bits = (w.bits << move) | mask
		w.n = bufBits
		if err = w.FlushBits(); err != nil {
			return
		}
		n -= int(move)
		bits ^= (mask << move)
	}
	w.bits = (w.bits << uint(n)) | bits
	w.n += n
	return
}

// WriteBits appends the low n bits of `bits` to the stream.
func (w *Writer) WriteBits(bits uint, n int) (err error) {
	return w.WriteBits64(uint64(bits), n)
}

// Write emits p byte-by-byte. Satisfies io.Writer so the bit writer can be
// chained with byte-oriented encoders after the non-aligned header is written.
func (w *Writer) Write(p []byte) (n int, err error) {
	for n < len(p) {
		if err = w.WriteBits64(uint64(p[n]), 8); err != nil { //nolint:mnd // 8 bits per byte
			return
		}
		n++
	}
	return
}

// FlushBits writes any buffered bits to W, zero-padding the final byte on the right.
// Safe to call when the buffer is empty.
func (w *Writer) FlushBits() (err error) {
	if w.n > 0 {
		var b [8]byte
		bits := w.bits
		if w.n%8 != 0 {
			bits <<= uint(8 - (w.n % 8)) //nolint:mnd // left-align the trailing partial byte
		}
		want := (w.n + 7) / 8 //nolint:mnd // round up to whole bytes
		for i := range want {
			b[i] = byte(bits >> uint((want-i-1)*8)) //nolint:mnd // extract byte i, MSB first
		}
		if _, err = w.W.Write(b[:want]); err != nil {
			return
		}
		w.n = 0
	}
	return
}
