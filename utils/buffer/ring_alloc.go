package buffer

import (
	"fmt"
	"sync/atomic"

	"github.com/ugparu/gomedia/utils/logger"
)

type RingAllocOption func(*RingAlloc)

func WithLogName(name string) RingAllocOption {
	return func(r *RingAlloc) {
		r.logName = name
	}
}

// WithLogger sets the logger for the ring allocator.
func WithLogger(l logger.Logger) RingAllocOption {
	return func(r *RingAlloc) { r.log = l }
}

// ringSlotCount is the maximum number of in-flight allocations tracked at once.
// Must be a power of two. 4096 comfortably covers 30 s @ 30 fps + audio.
const ringSlotCount = 4096

const danderRingSize = 1024 * 1024 * 1024 // 1GB

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

	logName string
	log     logger.Logger
}

func (r *RingAlloc) String() string {
	if r.logName != "" {
		return fmt.Sprintf("RingAlloc(%s)", r.logName)
	}
	return "RingAlloc"
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

// newRingAllocSoftCap creates a RingAlloc with an initial usable capacity of
// softCap. When bufSize > softCap the backing buffer is allocated via
// mmap(MAP_ANONYMOUS) so that physical pages are committed only on first write
// and the allocation is invisible to Go's heap profiler. When bufSize == softCap
// a regular Go slice is used (no mmap overhead for fixed-size rings).
func newRingAllocSoftCap(bufSize, softCap int, opts ...RingAllocOption) *RingAlloc {
	softCap = min(softCap, bufSize)

	var buf []byte
	var useMmap bool
	if bufSize > softCap {
		var err error
		buf, err = mmapBytes(bufSize)
		if err != nil {
			buf = make([]byte, bufSize) // fallback
		} else {
			useMmap = true
		}
	} else {
		buf = make([]byte, bufSize)
	}

	r := &RingAlloc{
		buf:  buf,
		bcap: softCap,
		log:  logger.Default,
	}
	for _, opt := range opts {
		opt(r)
	}
	if useMmap {
		setMmapFinalizer(r)
	}
	return r
}

// NewRingAlloc creates a ring allocator backed by a slab of the given byte size.
func NewRingAlloc(size int, opts ...RingAllocOption) *RingAlloc {
	return newRingAllocSoftCap(size, size, opts...)
}

// GrowingRingAlloc wraps RingAlloc with in-place capacity growth. The backing
// buffer is pre-allocated at danderRingSize using virtual memory; physical pages
// are committed only as data is written. The usable capacity (bcap) starts at
// initSize and grows in place when needed, avoiding the memory overhead of
// multiple coexisting rings during growth transitions.
//
// Only when the ring is genuinely full of live data (all slots occupied) is a
// new ring created as a last resort.
//
// Must be driven by a single producer goroutine (same constraint as RingAlloc).
type GrowingRingAlloc struct {
	current *RingAlloc
	maxCap  int
	opts    []RingAllocOption
}

// NewGrowingRingAlloc creates a growing ring allocator with the given initial
// byte capacity. The backing buffer is pre-allocated at danderRingSize (or
// initSize if larger) using virtual memory.
func NewGrowingRingAlloc(initSize int, opts ...RingAllocOption) *GrowingRingAlloc {
	mc := max(danderRingSize, initSize)
	return &GrowingRingAlloc{
		current: newRingAllocSoftCap(mc, initSize, opts...),
		maxCap:  mc,
		opts:    opts,
	}
}

// calcGrowth computes the target capacity given the current cap and the
// requested allocation size.
func (g *GrowingRingAlloc) calcGrowth(currentCap, allocSize int) int {
	grov := 20
	if currentCap > 1024*1024*10 {
		grov = 11
	} else if currentCap > 1024*1024*5 {
		grov = 12
	} else if currentCap > 1024*1024 {
		grov = 14
	}
	return max(currentCap*grov/10, allocSize*2)
}

// growCap increases the usable capacity of the current ring in place.
// The backing buffer was pre-allocated at maxCap, so this only bumps the bcap
// field — no memory allocation occurs and no old ring is left behind.
//
// Must be called from the producer goroutine only.
func (g *GrowingRingAlloc) growCap(allocSize int) {
	r := g.current
	newCap := g.calcGrowth(r.bcap, allocSize)
	newCap = min(newCap, g.maxCap)
	if newCap <= r.bcap {
		return
	}
	if newCap > danderRingSize {
		r.log.Warningf(r, "Growing ring alloc to %dkb is too large", newCap/1024)
	} else {
		r.log.Infof(r, "Growing ring alloc to %dkb", newCap/1024)
	}
	r.bcap = newCap
}

// Alloc carves n contiguous bytes from the current ring. When the usable
// capacity is too small it is grown in place (the backing buffer was
// pre-allocated). Only when the ring is genuinely full of live data is a new
// ring created as a last resort.
//
// Must be called from the producer goroutine only.
func (g *GrowingRingAlloc) Alloc(n int) ([]byte, *SlotHandle) {
	r := g.current

	// Preemptively grow the usable capacity when the allocation would either
	// exceed the current cap (n > bcap) or force a wrap (write+n > bcap).
	// Growing in place reuses the pre-allocated backing buffer, avoiding the
	// memory overhead of two coexisting rings during growth transitions.
	if (n > r.bcap || r.write+n > r.bcap) && r.bcap < g.maxCap {
		g.growCap(n)
	}

	if buf, h := r.Alloc(n); buf != nil {
		return buf, h
	}

	// The ring is genuinely full of live data or the slot table is exhausted.
	// Create a new ring as a last resort; the old ring becomes GC-eligible once
	// all outstanding SlotHandles have been released by consumers.
	newSize := g.calcGrowth(r.bcap, n)
	mc := g.maxCap
	if newSize > mc {
		mc = newSize
		g.maxCap = mc
	}
	r.log.Warningf(r, "Ring genuinely full, creating new %dkb ring", mc/1024)
	g.current = newRingAllocSoftCap(mc, mc, g.opts...)
	return g.current.Alloc(n)
}

// Extend grows the most recent allocation in-place. Only succeeds when the
// handle belongs to the current ring and there is enough contiguous free space.
// Like Alloc, the usable capacity is grown in place when possible.
//
// Must be called from the producer goroutine only.
func (g *GrowingRingAlloc) Extend(h *SlotHandle, n int) ([]byte, bool) {
	if h == nil || h.ring != g.current {
		return nil, false
	}
	r := g.current
	if r.write+n > r.bcap && r.bcap < g.maxCap {
		g.growCap(n)
	}
	return r.Extend(h, n)
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
