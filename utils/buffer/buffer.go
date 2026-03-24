package buffer

import (
	"sync"
)

const (
	defaultBufSize = 4 * 1024  // Начальный размер 4KB
	bigBufSize     = 64 * 1024 // 64KB
)

// Пул объектов memBuffer
var bufPool = sync.Pool{
	New: func() any {
		return &memBuffer{
			buf: make([]byte, 0, defaultBufSize),
		}
	},
}

var bigBufPool = sync.Pool{
	New: func() any {
		return &memBuffer{
			buf: make([]byte, 0, bigBufSize),
		}
	},
}

// Get получает буфер из пула с заданной длиной (len)
func Get(size int) Buffer {
	var b *memBuffer
	if size >= bigBufSize {
		b = bigBufPool.Get().(*memBuffer)
	} else {
		b = bufPool.Get().(*memBuffer)
	}

	// Убеждаемся, что емкости хватает
	if cap(b.buf) < size {
		b.buf = make([]byte, size)
	}

	// Устанавливаем рабочую длину слайса
	b.buf = b.buf[:size]
	return b
}

type memBuffer struct {
	buf []byte // Прямой слайс байт
}

// Data возвращает сам слайс
func (b *memBuffer) Data() []byte {
	return b.buf
}

func (b *memBuffer) Len() int {
	return len(b.buf)
}

func (b *memBuffer) Cap() int {
	return cap(b.buf)
}

// Resize меняет длину слайса (len), не меняя емкость (cap), если влезает
func (b *memBuffer) Resize(size int) {
	if size > cap(b.buf) {
		newCap := cap(b.buf) * 14 / 10
		if newCap < size {
			newCap = size
		}
		newBuf := make([]byte, newCap)
		copy(newBuf, b.buf)
		b.buf = newBuf
	} else {
		b.buf = b.buf[:size]
	}
}
