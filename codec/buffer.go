package codec

import (
	"os"
	"syscall"

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

// fileBuffer reads data from file using memory mapping
type fileBuffer struct {
	file      *os.File
	offset    int64
	size      int
	data      []byte // slice pointing to actual data within mapped region
	mappedMem []byte // full mapped memory region (for unmapping)
}

// GetFileBuffer creates a buffer that reads data from a file at the specified offset
func GetFileBuffer(f *os.File, offset int64, size int) RefBuffer {
	// mmap requires page-aligned offset
	pageSize := int64(syscall.Getpagesize())
	alignedOffset := offset &^ (pageSize - 1)   // round down to page boundary
	offsetInPage := int(offset - alignedOffset) // offset within the page
	mappedSize := offsetInPage + size           // total size to map

	mappedMem, err := syscall.Mmap(int(f.Fd()), alignedOffset, mappedSize, syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		logger.Errorf(f, "failed to mmap file: %v", err)
		return nil
	}
	return &fileBuffer{
		file:      f,
		offset:    offset,
		size:      size,
		data:      mappedMem[offsetInPage : offsetInPage+size],
		mappedMem: mappedMem,
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
	if b.mappedMem != nil {
		_ = syscall.Munmap(b.mappedMem)
		b.mappedMem = nil
	}
	b.data = nil
}
