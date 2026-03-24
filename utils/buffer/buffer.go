package buffer

// Get получает буфер из пула с заданной длиной (len)
func Get(size int) Buffer {
	b := &memBuffer{buf: make([]byte, 0, size)}

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
		newBuf := make([]byte, size, newCap)
		copy(newBuf, b.buf)
		b.buf = newBuf
	} else {
		b.buf = b.buf[:size]
	}
}
