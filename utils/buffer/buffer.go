package buffer

import (
	"sync"
	"sync/atomic"
)

const (
	defaultBufSize = 4 * 1024        // Начальный размер 4KB
	bigBufSize     = 64 * 1024       // 1MB
	maxBufSize     = 1 * 1024 * 1024 // 1MB предел для возврата в пул
)

var bigTotal, total int

// Пул объектов memBuffer
var bufPool = sync.Pool{
	New: func() any {
		total++
		println("total", total)
		return &memBuffer{
			buf: make([]byte, 0, defaultBufSize),
			ref: 1, // Новый буфер рождается с 1 владельцем
		}
	},
}

var bigBufPool = sync.Pool{
	New: func() any {
		bigTotal++
		println("bigTotal", bigTotal)
		return &memBuffer{
			buf: make([]byte, 0, bigBufSize),
			ref: 1, // Новый буфер рождается с 1 владельцем
		}
	},
}

// Get получает буфер из пула с заданной длиной (len)
func Get(size int) PooledBuffer {
	println(size)
	var b *memBuffer
	if size >= bigBufSize {
		b = bigBufPool.Get().(*memBuffer)
	} else {
		b = bufPool.Get().(*memBuffer)
	}
	b.ref = 1 // Сбрасываем счетчик (на случай, если пул вернул грязный объект)

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
	ref int32  // Атомарный счетчик
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

// Retain атомарно увеличивает счетчик
func (b *memBuffer) Retain() {
	atomic.AddInt32(&b.ref, 1)
}

// Release атомарно уменьшает счетчик и возвращает в пул при 0
func (b *memBuffer) Release() {
	count := atomic.AddInt32(&b.ref, -1)
	if count == 0 {
		b.recycle()
	} else if count < 0 {
		panic("buffer reference count is negative")
	}
}

func (b *memBuffer) recycle() {
	// Защита от утечки памяти: если буфер разросся слишком сильно,
	// лучше позволить GC собрать его, чем держать в пуле.
	if cap(b.buf) > maxBufSize {
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
