package buffer

import (
	"sync/atomic"
	"syscall"
)

// MmapRegion represents a shared memory-mapped region with simple reference counting.
// It is created once per file and then shared between multiple mmapViewBuffer instances.
type MmapRegion struct {
	data []byte
	refs int32
}

// NewMmapRegion maps a file descriptor with the given size into memory.
// The mapping is read-only and shared. Initial reference count is 1.
func NewMmapRegion(fd uintptr, size int) (*MmapRegion, error) {
	data, err := syscall.Mmap(
		int(fd),
		0,
		size,
		syscall.PROT_READ,
		syscall.MAP_SHARED,
	)
	if err != nil {
		return nil, err
	}

	return &MmapRegion{
		data: data,
		refs: 1,
	}, nil
}

// View returns a PooledBuffer that references a subrange of the mapped region.
// Each view increments the region's reference count and will decrement it on Release.
func (r *MmapRegion) View(offset, length int) PooledBuffer {
	if offset < 0 || length < 0 || offset+length > len(r.data) {
		panic("MmapRegion.View: out of bounds")
	}

	atomic.AddInt32(&r.refs, 1)

	return &mmapViewBuffer{
		region: r,
		buf:    r.data[offset : offset+length],
	}
}

// Release decrements the region reference count and unmaps when it reaches zero.
// It should be called once by the owner that created the region (e.g., activeFile).
func (r *MmapRegion) Release() {
	if atomic.AddInt32(&r.refs, -1) == 0 {
		if r.data != nil {
			_ = syscall.Munmap(r.data)
			r.data = nil
		}
	}
}

// mmapViewBuffer is a view into an MmapRegion that implements PooledBuffer.
// Releasing the view decrements the region reference count.
type mmapViewBuffer struct {
	region *MmapRegion
	buf    []byte
}

func (b *mmapViewBuffer) Data() []byte {
	return b.buf
}

func (b *mmapViewBuffer) Len() int {
	return len(b.buf)
}

func (b *mmapViewBuffer) Cap() int {
	return cap(b.buf)
}

func (b *mmapViewBuffer) Resize(_ int) {
	panic("mmapViewBuffer: Resize is not supported for memory-mapped buffers")
}

func (b *mmapViewBuffer) Release() {
	if b.region != nil {
		b.region.Release()
		b.region = nil
		b.buf = nil
	}
}

