package buffer

// Get returns a Buffer whose backing slice has len == size.
func Get(size int) Buffer {
	b := &memBuffer{buf: make([]byte, 0, size)}

	if cap(b.buf) < size {
		b.buf = make([]byte, size)
	}

	b.buf = b.buf[:size]
	return b
}

type memBuffer struct {
	buf []byte
}

func (b *memBuffer) Data() []byte {
	return b.buf
}

func (b *memBuffer) Len() int {
	return len(b.buf)
}

func (b *memBuffer) Cap() int {
	return cap(b.buf)
}

// Resize grows or shrinks the slice. If size fits within the existing capacity,
// only len changes; otherwise a new backing array is allocated and contents copied.
func (b *memBuffer) Resize(size int) {
	if size > cap(b.buf) {
		newBuf := make([]byte, size)
		copy(newBuf, b.buf)
		b.buf = newBuf
	} else {
		b.buf = b.buf[:size]
	}
}
