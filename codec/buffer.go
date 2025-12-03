package codec

import (
	"io"
	"os"

	"github.com/ugparu/gomedia/utils/logger"
)

type RefBuffer interface {
	Data() []byte
	SetData([]byte)
	Len() int
}

const bufSize = 1024 // 1KB

func GetMemBuffer() RefBuffer {
	return &memBuffer{
		data: make([]byte, bufSize),
	}
}

type memBuffer struct {
	data []byte
}

func (b *memBuffer) Data() []byte {
	return b.data
}

func (b *memBuffer) SetData(data []byte) {
	if cap(b.data) < len(data) {
		logger.Debugf(b, "buffer realloc to: %d", len(data))
		b.data = make([]byte, len(data))
	}
	b.data = b.data[:len(data)]
	copy(b.data, data)
}

func (b *memBuffer) Len() int {
	return len(b.data)
}

func (b *memBuffer) Close() {
	b.data = nil
}

// fileBuffer reads data from file using ReadAt instead of memory mapping
type fileBuffer struct {
	file   *os.File
	offset int64
	size   int64
	data   []byte
}

// GetFileBuffer creates a buffer that reads data from a file at the specified offset
func GetFileBuffer(f *os.File, offset int64, size int64) RefBuffer {
	data := make([]byte, size)
	n, err := f.ReadAt(data, offset)
	if err != nil && err != io.EOF {
		logger.Errorf(f, "failed to read file: %v", err)
		return nil
	}
	return &fileBuffer{
		file:   f,
		offset: offset,
		size:   int64(n),
		data:   data[:n],
	}
}

func (b *fileBuffer) Data() []byte {
	return b.data
}

func (b *fileBuffer) SetData(data []byte) {
	if cap(b.data) < len(data) {
		b.data = make([]byte, len(data))
	}
	b.data = b.data[:len(data)]
	copy(b.data, data)
}

func (b *fileBuffer) Len() int {
	return len(b.data)
}

func (b *fileBuffer) Close() {
	b.data = nil
}
