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
			return &Buffer{
				Buffer: make([]byte, 0, defaultBufSize),
			}
		},
	}
)

func GetBuffer(size int) *Buffer {
	buf, _ := refBufPool.Get().(*Buffer)
	if cap(buf.Buffer) < size {
		buf.Buffer = make([]byte, size)
	}
	buf.Buffer = buf.Buffer[:size]
	return buf
}

func PutBuffer(b *Buffer) {
	refBufPool.Put(b)
}

type Buffer struct {
	Buffer []byte
}

func (b *Buffer) Grow(size int) []byte {
	if cap(b.Buffer) < size {
		b.Buffer = make([]byte, size)
	}
	b.Buffer = b.Buffer[:size]
	return b.Buffer
}
