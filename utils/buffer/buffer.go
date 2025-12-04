package buffer

import (
	"os"
	"sync"
	"syscall"
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

// memMapBuffer — буфер на основе memory-mapped файла
type memMapBuffer struct {
	mmapRegion []byte       // Полный mmap регион (для Munmap)
	buf        []byte       // Слайс данных, который видит пользователь
	closeFn    func() error // Функция закрытия (например, file.Close)
}

// GetMmap создает буфер на основе memory-mapped файла
// file - открытый файл для маппинга
// offset - смещение в файле (автоматически выравнивается по странице)
// length - размер маппируемого региона
// closeFn - функция, вызываемая после освобождения всех ссылок (может быть nil)
func GetMmap(file *os.File, offset int64, length int, closeFn func() error) (PooledBuffer, error) {
	pageSize := int64(syscall.Getpagesize())

	// Выравниваем offset вниз до границы страницы
	alignedOffset := offset &^ (pageSize - 1)
	offsetDelta := int(offset - alignedOffset)

	// Увеличиваем length чтобы покрыть нужный диапазон
	alignedLength := length + offsetDelta

	data, err := syscall.Mmap(
		int(file.Fd()),
		alignedOffset,
		alignedLength,
		syscall.PROT_READ,
		syscall.MAP_SHARED,
	)
	if err != nil {
		return nil, err
	}

	return &memMapBuffer{
		mmapRegion: data,
		buf:        data[offsetDelta : offsetDelta+length],
		closeFn:    closeFn,
	}, nil
}

// Data возвращает memory-mapped слайс
func (b *memMapBuffer) Data() []byte {
	return b.buf
}

// Len возвращает длину буфера
func (b *memMapBuffer) Len() int {
	return len(b.buf)
}

// Cap возвращает емкость буфера (для mmap равна длине)
func (b *memMapBuffer) Cap() int {
	return cap(b.buf)
}

// Resize паникует, так как изменение размера mmap региона не поддерживается
func (b *memMapBuffer) Resize(_ int) {
	panic("memMapBuffer: Resize is not supported for memory-mapped buffers")
}

// Release освобождает memory-mapped регион и связанные ресурсы
func (b *memMapBuffer) Release() {
	// Освобождаем memory-mapped регион
	if b.mmapRegion != nil {
		_ = syscall.Munmap(b.mmapRegion)
		b.mmapRegion = nil
		b.buf = nil
	}

	// Вызываем функцию закрытия (например, file.Close)
	if b.closeFn != nil {
		_ = b.closeFn()
		b.closeFn = nil
	}
}
