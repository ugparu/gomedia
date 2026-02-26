package buffer

import (
	"os"
	"sync"
)

const (
	defaultBufSize = 4 * 1024        // Начальный размер 4KB
	bigBufSize     = 64 * 1024       // 64KB
	maxBufSize     = 1 * 1024 * 1024 // 1MB предел для возврата в пул
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
func Get(size int) PooledBuffer {
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
		// Если просят больше чем есть памяти, придется выделить новую
		newBuf := make([]byte, size)
		copy(newBuf, b.buf)
		b.buf = newBuf
	} else {
		b.buf = b.buf[:size]
	}
}

// Release возвращает буфер в пул
func (b *memBuffer) Release() {
	// Защита от утечки памяти: если буфер разросся слишком сильно,
	// лучше позволить GC собрать его, чем держать в пуле.
	if cap(b.buf) > maxBufSize || os.Getenv("REUSE_BUFFERS") != "1" {
		return
	}

	// "Стирать" данные нулями не обязательно, просто сбрасываем длину
	b.buf = b.buf[:0]
	if cap(b.buf) >= bigBufSize {
		bigBufPool.Put(b)
	} else {
		bufPool.Put(b)
	}
}
