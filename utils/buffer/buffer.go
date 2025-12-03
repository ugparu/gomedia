package buffer

import (
	"bytes"
	"sync"
)

const (
	defaultBufSize = 0
	maxBufSize     = 1024 * 1024 // 1MB
)

var (
	bufPool = sync.Pool{
		New: func() any {
			return &memBuffer{
				Buffer: bytes.NewBuffer(make([]byte, 0, defaultBufSize)),
				ref:    0,
				size:   defaultBufSize,
			}
		},
	}
)

func Get(size int) RefBuffer {
	buf, _ := bufPool.Get().(*memBuffer)
	buf.Reset()
	if buf.Cap() < size {
		buf.Grow(size)
	}
	buf.size = size
	return buf
}

func Put(b RefBuffer) {
	buf, ok := b.(*memBuffer)
	if !ok {
		return
	}
	buf.ref--
	if buf.ref > 0 || buf.Len() > maxBufSize {
		return
	}
	buf.Reset()
	bufPool.Put(b)
}

func (b *memBuffer) Resize(size int) {
	b.size = size
	if b.size > b.Cap() {
		b.Buffer.Reset()
		b.Buffer.Grow(b.size)
	}
}

func (b *memBuffer) Data() []byte {
	return b.Buffer.Bytes()[:b.size]
}

type memBuffer struct {
	*bytes.Buffer
	ref  int
	size int
}

func (b *memBuffer) AddRef() {
	b.ref++
}

// // fileBuffer reads data from file using memory mapping
// type fileBuffer struct {
// 	file      *os.File
// 	offset    int64
// 	size      int
// 	data      []byte // slice pointing to actual data within mapped region
// 	mappedMem []byte // full mapped memory region (for unmapping)
// }

// // GetFileBuffer creates a buffer that reads data from a file at the specified offset
// func GetFileBuffer(f *os.File, offset int64, size int) RefBuffer {
// 	// mmap requires page-aligned offset
// 	pageSize := int64(syscall.Getpagesize())
// 	alignedOffset := offset &^ (pageSize - 1)   // round down to page boundary
// 	offsetInPage := int(offset - alignedOffset) // offset within the page
// 	mappedSize := offsetInPage + size           // total size to map

// 	mappedMem, err := syscall.Mmap(int(f.Fd()), alignedOffset, mappedSize, syscall.PROT_READ, syscall.MAP_SHARED)
// 	if err != nil {
// 		logger.Errorf(f, "failed to mmap file: %v", err)
// 		return nil
// 	}
// 	return &fileBuffer{
// 		file:      f,
// 		offset:    offset,
// 		size:      size,
// 		data:      mappedMem[offsetInPage : offsetInPage+size],
// 		mappedMem: mappedMem,
// 	}
// }

// func (b *fileBuffer) Data() []byte {
// 	return b.data
// }

// func (b *fileBuffer) SetData(data []byte) {
// 	if cap(b.data) < len(data) {
// 		b.data = make([]byte, len(data))
// 	}
// 	b.data = b.data[:len(data)]
// 	copy(b.data, data)
// }

// func (b *fileBuffer) Len() int {
// 	return len(b.data)
// }

// func (b *fileBuffer) Close() {
// 	if b.mappedMem != nil {
// 		_ = syscall.Munmap(b.mappedMem)
// 		b.mappedMem = nil
// 	}
// 	b.data = nil
// }
