package buffer

import (
	"fmt"
	"sync/atomic"
	"time"

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

// WithUsageLog enables periodic logging of ring buffer byte utilization.
// The logger used is the one set via WithLogger (or the default no-op logger).
func WithUsageLog(interval time.Duration) RingAllocOption {
	return func(r *RingAlloc) {
		r.usageLogInterval = interval
	}
}

// WithStaleRingAllocLog enables a background watchdog goroutine that periodically
// scans live slots and logs any slot that has been held longer than timeout.
// This is a diagnostic tool for detecting leaked SlotHandles — do not enable in
// performance-critical production paths.
func WithStaleRingAllocLog(timeout time.Duration) RingAllocOption {
	return func(r *RingAlloc) {
		r.staleTimeout = timeout
	}
}

// ringSlotCount is the maximum number of in-flight allocations tracked at once.
// Must be a power of two. 4096 comfortably covers 30 s @ 30 fps + audio.
const ringSlotCount = 4096

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

	staleTimeout time.Duration
	allocTimes   [ringSlotCount]atomic.Int64 // unix-nano timestamp set on Alloc; 0 when released
	stopStale    chan struct{}

	usageLogInterval time.Duration
	stopUsageLog     chan struct{}
}

func (r *RingAlloc) String() string {
	if r.logName != "" {
		return r.logName
	}
	return "RingAlloc"
}

// staleWatchdog periodically scans live slots and invokes staleFn for any
// slot held longer than staleTimeout.
func (r *RingAlloc) staleWatchdog() {
	interval := r.staleTimeout / 2 //nolint:mnd // scan twice per timeout period
	if interval < time.Millisecond {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopStale:
			return
		case <-ticker.C:
			r.scanStaleSlots()
		}
	}
}

// scanStaleSlots checks all live slots for stale handles.
func (r *RingAlloc) scanStaleSlots() {
	now := time.Now().UnixNano()
	head := r.head.Load()
	tail := r.tail.Load()

	for i := head; i < tail; i++ {
		si := i % ringSlotCount
		if r.slots[si].refs.Load() <= 0 {
			continue
		}
		allocNano := r.allocTimes[si].Load()
		if allocNano == 0 {
			continue
		}
		age := time.Duration(now - allocNano)
		if age >= r.staleTimeout {
			r.log.Errorf(r, "slot %d held for %v, possible leak (refs=%d)", i, age, r.slots[si].refs.Load())
		}
	}
}

// StopStaleWatchdog stops the background stale-detection goroutine.
// Safe to call if no watchdog is running (no-op).
func (r *RingAlloc) StopStaleWatchdog() {
	if r.stopStale != nil {
		close(r.stopStale)
		r.stopStale = nil
	}
}

// Usage returns the byte utilization of the ring as a value in [0, 1].
// 0 means empty, 1 means fully occupied.
func (r *RingAlloc) Usage() float64 {
	head := r.head.Load()
	tail := r.tail.Load()
	if head == tail {
		return 0
	}
	read := int(r.slots[head%ringSlotCount].start)
	write := r.write
	var used int
	if write >= read {
		used = write - read
	} else {
		used = (r.bcap - read) + write
	}
	return float64(used) / float64(r.bcap)
}

// usageLogger periodically logs ring buffer utilization.
func (r *RingAlloc) usageLogger() {
	ticker := time.NewTicker(r.usageLogInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopUsageLog:
			return
		case <-ticker.C:
			r.log.Infof(r, "usage %.1f%%, slots %d/%d",
				r.Usage()*100, //nolint:mnd // percentage conversion
				r.tail.Load()-r.head.Load(), ringSlotCount)
		}
	}
}

// StopUsageLog stops the background usage-logging goroutine.
// Safe to call if no logger is running (no-op).
func (r *RingAlloc) StopUsageLog() {
	if r.stopUsageLog != nil {
		close(r.stopUsageLog)
		r.stopUsageLog = nil
	}
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
func NewRingAlloc(size int, opts ...RingAllocOption) *RingAlloc {
	r := &RingAlloc{
		buf:  make([]byte, size),
		bcap: size,
		log:  logger.Default,
	}
	for _, opt := range opts {
		opt(r)
	}
	if r.staleTimeout > 0 {
		r.stopStale = make(chan struct{})
		go r.staleWatchdog()
	}
	if r.usageLogInterval > 0 {
		r.stopUsageLog = make(chan struct{})
		go r.usageLogger()
	}
	return r
}

// GrowingRingAlloc wraps RingAlloc with automatic growth. The ring starts at
// the requested initSize; when it cannot satisfy an allocation it creates a new,
// larger ring. The old ring becomes GC-eligible once all outstanding SlotHandles
// have been released by consumers.
//
// Must be driven by a single producer goroutine (same constraint as RingAlloc).
type GrowingRingAlloc struct {
	current *RingAlloc
	opts    []RingAllocOption
}

// NewGrowingRingAlloc creates a growing ring allocator with the given initial
// byte capacity.
func NewGrowingRingAlloc(initSize int, opts ...RingAllocOption) *GrowingRingAlloc {
	return &GrowingRingAlloc{
		current: NewRingAlloc(initSize, opts...),
		opts:    opts,
	}
}

// calcGrowth computes the target capacity given the current cap and the
// requested allocation size.
func (g *GrowingRingAlloc) calcGrowth(currentCap, allocSize int) int {
	grov := 20 //nolint:mnd // growth factor ×2.0
	if currentCap > 1024*1024*10 {
		grov = 11 //nolint:mnd // growth factor ×1.1 for >10 MB
	} else if currentCap > 1024*1024*5 {
		grov = 12 //nolint:mnd // growth factor ×1.2 for >5 MB
	} else if currentCap > 1024*1024 {
		grov = 14 //nolint:mnd // growth factor ×1.4 for >1 MB
	}
	return max(currentCap*grov/10, allocSize*2) //nolint:mnd // factor is tenths
}

// grow creates a new, larger ring. The old ring stays alive until all its
// SlotHandles are released.
//
// Must be called from the producer goroutine only.
func (g *GrowingRingAlloc) grow(allocSize int) {
	r := g.current
	newCap := g.calcGrowth(r.bcap, allocSize)
	r.log.Infof(r, "Growing ring alloc to %dkb", newCap/1024) //nolint:mnd
	r.StopStaleWatchdog()                                     // old ring — stop its watchdog; slots drain naturally
	r.StopUsageLog()
	g.current = NewRingAlloc(newCap, g.opts...)
}

// Alloc carves n contiguous bytes from the current ring. When the current ring
// cannot satisfy the allocation a new, larger ring is created.
//
// Must be called from the producer goroutine only.
func (g *GrowingRingAlloc) Alloc(n int) ([]byte, *SlotHandle) {
	if buf, h := g.current.Alloc(n); buf != nil {
		return buf, h
	}

	if g.current.slotsExhausted() {
		panic(fmt.Sprintf("%s: slot table exhausted (%d slots in-flight), consumers are not releasing handles",
			g.current, ringSlotCount))
	}

	g.grow(n)
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

// slotsExhausted reports whether every entry in the slot table is in use.
func (r *RingAlloc) slotsExhausted() bool {
	return r.tail.Load()-r.head.Load() >= ringSlotCount
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
	if r.staleTimeout > 0 {
		r.allocTimes[idx%ringSlotCount].Store(time.Now().UnixNano())
	}
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

	slotIdx := h.idx % ringSlotCount
	refs := r.slots[slotIdx].refs.Add(-1)
	if refs < 0 {
		panic("refs < 0")
	}
	if refs == 0 {
		r.allocTimes[slotIdx].Store(0)
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
