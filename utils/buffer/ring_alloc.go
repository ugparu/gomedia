package buffer

import (
	"sync/atomic"

	"github.com/ugparu/gomedia/utils/logger"
)

// ringSlotCount is the maximum number of in-flight allocations tracked at once.
// Must be a power of two. 4096 comfortably covers 30 s @ 30 fps + audio.
const ringSlotCount = 4096

const danderRingSize = 1024 * 1024 * 10 // 10MB

// RingAlloc is a fixed-size byte slab with FIFO slot tracking and atomic reference
// counting. It is designed for a single producer goroutine (demuxer) and N consumer
// goroutines (writers).
//
// Each Alloc call carves a contiguous region [start, end) from the slab and records
// it as a slot with an initial reference count of 1. Calling Retain on the returned
// SlotHandle bumps the count (for Clone(false) fan-out). Calling Release on each
// owner decrements the count; when it reaches zero the slot is considered done.
//
// The ring head cursor advances past all consecutively-done slots, recycling their
// bytes for future allocations.
//
// Wrap-around: when a new allocation would overflow the slab end, a zero-refs waste
// slot is inserted to mark the dead tail bytes, and the write cursor resets to 0.
// This keeps every allocation contiguous — no allocation ever spans the wrap point.
type RingAlloc struct {
	buf   []byte
	bcap  int
	write int // next byte to write; producer-only, never accessed by consumers

	slots [ringSlotCount]ringSlot
	head  atomic.Uint64 // index of oldest live slot; advanced by consumers via CAS
	tail  atomic.Uint64 // next slot index; written by producer, read by consumers
}

type ringSlot struct {
	start int32
	end   int32
	refs  atomic.Int32 // 0 == done/waste, >0 == live
}

// SlotHandle is the release token returned with every allocation.
// A packet that wraps ring memory must hold exactly one SlotHandle and call
// Release() when it is no longer needed. Clone(false) calls Retain() to share
// ownership; every resulting owner must call Release() exactly once.
type SlotHandle struct {
	ring *RingAlloc
	idx  uint64
}

// NewRingAlloc creates a ring allocator backed by a slab of the given byte size.
func NewRingAlloc(size int) *RingAlloc {
	return &RingAlloc{
		buf:  make([]byte, size),
		bcap: size,
	}
}

// GrowingRingAlloc wraps RingAlloc and creates a fresh, larger ring when the
// current one is full. The old ring (and its backing slab) becomes eligible for
// GC once every SlotHandle referencing it has been released by consumers.
//
// Must be driven by a single producer goroutine (same constraint as RingAlloc).
type GrowingRingAlloc struct {
	current *RingAlloc
}

// NewGrowingRingAlloc creates a growing ring allocator with the given initial
// byte capacity.
func NewGrowingRingAlloc(initSize int) *GrowingRingAlloc {
	return &GrowingRingAlloc{current: NewRingAlloc(initSize)}
}

// Alloc carves n contiguous bytes from the current ring. When the ring is full
// a new ring is created (at least 2×n) and the old ring
// becomes GC-eligible once all its outstanding SlotHandles are released.
//
// Must be called from the producer goroutine only.
func (g *GrowingRingAlloc) Alloc(n int) ([]byte, *SlotHandle) {
	if buf, h := g.current.Alloc(n); buf != nil {
		return buf, h
	}
	newSize := max(g.current.bcap*14/10, n*2)
	if newSize > danderRingSize {
		logger.Warningf(g, "Growing ring alloc to %dkb is too large", newSize/1024)
	} else {
		logger.Infof(g, "Growing ring alloc to %dkb", newSize/1024)
	}
	g.current = NewRingAlloc(newSize)
	return g.current.Alloc(n)
}

// Extend grows the most recent allocation in-place. Only succeeds when the
// handle belongs to the current ring and there is enough contiguous free space.
//
// Must be called from the producer goroutine only.
func (g *GrowingRingAlloc) Extend(h *SlotHandle, n int) ([]byte, bool) {
	if h == nil || h.ring != g.current {
		return nil, false
	}
	return g.current.Extend(h, n)
}

// readCursor returns the start position of the oldest live slot.
// When the ring is empty (head == tail) it returns the current write position,
// which means all bytes are free.
func (r *RingAlloc) readCursor() int {
	head := r.head.Load()
	if head == r.tail.Load() {
		return r.write
	}
	return int(r.slots[head%ringSlotCount].start)
}

// regionFree reports whether [lo, hi) lies entirely in the free zone.
//
// Two topologies:
//   - write >= read  →  live is [read, write),  free is [0, read) ∪ [write, cap)
//   - write <  read  →  live is [0, write) ∪ [read, cap),  free is [write, read)
func (r *RingAlloc) regionFree(lo, hi, read int) bool {
	write := r.write
	if write >= read {
		return hi <= read || lo >= write
	}
	return lo >= write && hi <= read
}

// Alloc carves n contiguous bytes from the ring and returns the slice together
// with a SlotHandle for release. Returns nil, nil when the ring is full or the
// slot table is exhausted.
//
// Must be called from the producer goroutine only.
func (r *RingAlloc) Alloc(n int) ([]byte, *SlotHandle) {
	if n <= 0 || n > r.bcap {
		return nil, nil
	}

	head := r.head.Load()
	tail := r.tail.Load()
	read := r.readCursor()
	start := r.write

	// If n doesn't fit before the slab end, waste the tail and wrap.
	if start+n > r.bcap {
		// Need one extra slot for the waste entry.
		if tail-head >= ringSlotCount-1 {
			return nil, nil
		}
		waste := &r.slots[tail%ringSlotCount]
		waste.start = int32(start)
		waste.end = int32(r.bcap)
		// Store refs before incrementing tail so consumers see a consistent slot.
		waste.refs.Store(0) // immediately done — no handle issued
		r.tail.Add(1)
		tail++

		start = 0
		r.write = 0
		// Re-read after wrap; head may have advanced past waste slot already.
		head = r.head.Load()
		read = r.readCursor()
	}

	// When write == read and live slots exist, the ring is full, not empty.
	// regionFree cannot distinguish the two cases, so guard here.
	if start == read && head < tail {
		return nil, nil
	}
	if !r.regionFree(start, start+n, read) {
		return nil, nil
	}
	if tail-head >= ringSlotCount {
		return nil, nil
	}

	idx := tail
	slot := &r.slots[idx%ringSlotCount]
	slot.start = int32(start)
	slot.end = int32(start + n)
	// Store refs before incrementing tail — consumers cannot see this slot until
	// tail advances, and the atomic increment provides the release barrier.
	slot.refs.Store(1)
	r.write = start + n
	r.tail.Add(1)

	return r.buf[start : start+n], &SlotHandle{ring: r, idx: idx}
}

// Extend grows the most recent allocation in-place by n additional bytes.
// It only succeeds when the write cursor sits exactly at the end of the given
// slot (no other allocation happened in between) and there is enough free space.
// Returns the new, larger slice on success or nil, false otherwise.
//
// Must be called from the producer goroutine only.
func (r *RingAlloc) Extend(h *SlotHandle, n int) ([]byte, bool) {
	if h == nil || n <= 0 {
		return nil, false
	}
	slot := &r.slots[h.idx%ringSlotCount]
	curEnd := int(slot.end)
	if r.write != curEnd || curEnd+n > r.bcap {
		return nil, false
	}
	read := r.readCursor()
	// Same full-ring guard as Alloc: write == read with live slots means full.
	if curEnd == read && r.head.Load() < r.tail.Load() {
		return nil, false
	}
	if !r.regionFree(curEnd, curEnd+n, read) {
		return nil, false
	}
	slot.end = int32(curEnd + n)
	r.write = curEnd + n
	return r.buf[int(slot.start):r.write], true
}

// Retain increments the reference count, registering one additional owner.
// Call this before handing the handle to a second consumer (e.g., in Clone(false)).
// Safe to call on a nil handle.
func (h *SlotHandle) Retain() {
	if h == nil {
		return
	}
	h.ring.slots[h.idx%ringSlotCount].refs.Add(1)
}

// Release decrements the reference count. When it reaches zero the slot is
// marked done and the ring head is advanced past all consecutively-complete
// slots, recycling their bytes for future allocations.
// Safe to call on a nil handle (no-op).
func (h *SlotHandle) Release() {
	if h == nil {
		return
	}
	r := h.ring

	refs := r.slots[h.idx%ringSlotCount].refs.Add(-1)
	if refs < 0 {
		panic("refs < 0")
	}

	// Advance head past consecutive done slots using CAS so multiple concurrent
	// Release calls remain safe.
	for {
		head := r.head.Load()
		if head >= r.tail.Load() {
			break
		}
		if r.slots[head%ringSlotCount].refs.Load() != 0 {
			break
		}
		if r.head.CompareAndSwap(head, head+1) {
			continue
		}
		// CAS lost — another goroutine advanced head; retry from new position.
	}
}
