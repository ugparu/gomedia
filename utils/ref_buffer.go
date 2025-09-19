package utils

import (
	"sync"
)

const (
	defaultBufSize = 0
)

var (
	refBufPool = sync.Pool{
		New: func() any {
			return &RefBuffer{
				Buffer: make([]byte, 0, defaultBufSize),
				ref:    1,
			}
		},
	}
)

func GetRefBuffer(size int) *RefBuffer {
	buf, _ := refBufPool.Get().(*RefBuffer)
	buf.ref = 1
	if cap(buf.Buffer) < size {
		buf.Buffer = make([]byte, size)
	}
	buf.Buffer = buf.Buffer[:size]
	return buf
}

func PutRefBuffer(b *RefBuffer) {
	b.ref--
	if b.ref > 0 {
		return
	}
	refBufPool.Put(b)
}

type RefBuffer struct {
	Buffer []byte
	ref    int
}

func (b *RefBuffer) AddRef() {
	b.ref++
}

func (b *RefBuffer) Release() {
	b.ref--
}

func (b *RefBuffer) Grow(size int) []byte {
	if cap(b.Buffer) < size {
		b.Buffer = make([]byte, size)
	}
	b.Buffer = b.Buffer[:size]
	return b.Buffer
}
