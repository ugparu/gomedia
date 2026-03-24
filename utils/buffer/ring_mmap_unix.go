//go:build unix

package buffer

import (
	"runtime"

	"golang.org/x/sys/unix"
)

// mmapBytes allocates size bytes of virtual address space via mmap(MAP_ANONYMOUS).
// Physical pages are committed by the kernel only on first write (demand paging).
// The allocation is invisible to Go's heap profiler and GC.
func mmapBytes(size int) ([]byte, error) {
	return unix.Mmap(-1, 0, size,
		unix.PROT_READ|unix.PROT_WRITE,
		unix.MAP_PRIVATE|unix.MAP_ANON)
}

// setMmapFinalizer arranges for the ring's backing buffer to be munmap'd when
// the RingAlloc is garbage collected. This is safe because all SlotHandles hold
// a *RingAlloc reference, keeping it alive until every consumer has released.
func setMmapFinalizer(r *RingAlloc) {
	runtime.SetFinalizer(r, func(r *RingAlloc) {
		_ = unix.Munmap(r.buf)
	})
}
