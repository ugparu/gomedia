package buffer

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// RingAlloc — basic allocation
// ---------------------------------------------------------------------------

func TestRingAlloc_BasicAlloc(t *testing.T) {
	r := NewRingAlloc(1024)
	buf, h := r.Alloc(100)

	require.NotNil(t, buf)
	require.NotNil(t, h)
	assert.Equal(t, 100, len(buf))
	h.Release()
}

func TestRingAlloc_AllocWritesAreIsolated(t *testing.T) {
	r := NewRingAlloc(1024)

	buf1, h1 := r.Alloc(4)
	buf2, h2 := r.Alloc(4)
	defer h1.Release()
	defer h2.Release()

	// Write to buf1, verify buf2 is unaffected.
	buf1[0], buf1[1], buf1[2], buf1[3] = 0xAA, 0xBB, 0xCC, 0xDD
	for _, b := range buf2 {
		assert.Equal(t, byte(0), b)
	}
}

func TestRingAlloc_AllocExactCapacity(t *testing.T) {
	r := NewRingAlloc(256)
	buf, h := r.Alloc(256)

	require.NotNil(t, buf)
	assert.Equal(t, 256, len(buf))
	h.Release()
}

func TestRingAlloc_AllocZero(t *testing.T) {
	r := NewRingAlloc(1024)
	buf, h := r.Alloc(0)

	assert.Nil(t, buf)
	assert.Nil(t, h)
}

func TestRingAlloc_AllocNegative(t *testing.T) {
	r := NewRingAlloc(1024)
	buf, h := r.Alloc(-1)

	assert.Nil(t, buf)
	assert.Nil(t, h)
}

func TestRingAlloc_AllocOverCapacity(t *testing.T) {
	r := NewRingAlloc(256)
	buf, h := r.Alloc(257)

	assert.Nil(t, buf)
	assert.Nil(t, h)
}

// ---------------------------------------------------------------------------
// RingAlloc — sequential alloc / release
// ---------------------------------------------------------------------------

func TestRingAlloc_SequentialFillAndDrain(t *testing.T) {
	r := NewRingAlloc(100)

	// Fill: 10-byte allocations, 10 of them fills the ring.
	handles := make([]*SlotHandle, 0, 10)
	for range 10 {
		_, h := r.Alloc(10)
		require.NotNil(t, h, "should be able to allocate")
		handles = append(handles, h)
	}

	// Ring should be full now.
	buf, h := r.Alloc(1)
	assert.Nil(t, buf, "ring should be full")
	assert.Nil(t, h)

	// Release all, ring should be usable again.
	for _, hh := range handles {
		hh.Release()
	}

	buf, h = r.Alloc(10)
	require.NotNil(t, buf, "should allocate after full drain")
	h.Release()
}

func TestRingAlloc_ReleaseFreesSpaceForNewAlloc(t *testing.T) {
	r := NewRingAlloc(64)

	_, h1 := r.Alloc(32)
	_, h2 := r.Alloc(32)
	require.NotNil(t, h2)

	// Full.
	buf, h := r.Alloc(1)
	assert.Nil(t, buf)
	assert.Nil(t, h)

	// Release first slot — head advances, freeing [0, 32).
	h1.Release()

	buf, h3 := r.Alloc(32)
	require.NotNil(t, buf, "should reclaim space after Release")
	h2.Release()
	h3.Release()
}

// ---------------------------------------------------------------------------
// RingAlloc — wrap-around
// ---------------------------------------------------------------------------

func TestRingAlloc_WrapAround(t *testing.T) {
	r := NewRingAlloc(100)

	// Allocate 60 bytes, release them.
	_, h1 := r.Alloc(60)
	require.NotNil(t, h1)
	h1.Release()

	// Now write cursor is at 60. Allocating 50 won't fit [60, 110), must wrap to [0, 50).
	buf, h2 := r.Alloc(50)
	require.NotNil(t, buf, "should wrap around")
	assert.Equal(t, 50, len(buf))
	h2.Release()
}

func TestRingAlloc_WrapAroundBlockedByLiveSlot(t *testing.T) {
	r := NewRingAlloc(100)

	// Allocate 60 bytes, keep alive.
	_, h1 := r.Alloc(60)
	require.NotNil(t, h1)

	// Write cursor at 60. 50 bytes won't fit at end → wrap to 0.
	// But [0, 60) is still live, so [0, 50) overlaps.
	buf, h2 := r.Alloc(50)
	assert.Nil(t, buf, "should fail: live data at [0,60) blocks wrap-around alloc")
	assert.Nil(t, h2)

	h1.Release()
}

// ---------------------------------------------------------------------------
// RingAlloc — slot table exhaustion
// ---------------------------------------------------------------------------

func TestRingAlloc_SlotTableExhaustion(t *testing.T) {
	// Use a huge slab so byte space isn't the bottleneck.
	r := NewRingAlloc(ringSlotCount * 16)

	handles := make([]*SlotHandle, 0, ringSlotCount)
	for range ringSlotCount {
		_, h := r.Alloc(1)
		if h == nil {
			break
		}
		handles = append(handles, h)
	}

	// Slot table should be exhausted now (or nearly so, accounting for waste slots).
	assert.GreaterOrEqual(t, len(handles), ringSlotCount-2,
		"should have filled most of the slot table")

	// Clean up.
	for _, h := range handles {
		h.Release()
	}
}

// ---------------------------------------------------------------------------
// SlotHandle — Retain / Release ref counting
// ---------------------------------------------------------------------------

func TestSlotHandle_NilRetainRelease(t *testing.T) {
	var h *SlotHandle
	require.NotPanics(t, func() { h.Retain() })
	require.NotPanics(t, func() { h.Release() })
}

func TestSlotHandle_RetainAddsOwner(t *testing.T) {
	r := NewRingAlloc(256)
	_, h := r.Alloc(64)
	require.NotNil(t, h)

	// Two additional owners.
	h.Retain()
	h.Retain()

	// Must release 3 times total (1 original + 2 retains).
	h.Release()
	h.Release()

	// Ring should still be occupied (one ref left).
	buf, h2 := r.Alloc(200)
	assert.Nil(t, buf, "slot should still be live with 1 ref remaining")
	assert.Nil(t, h2)

	// Final release frees the slot.
	h.Release()
	buf, h2 = r.Alloc(200)
	require.NotNil(t, buf, "slot should be free after all refs released")
	h2.Release()
}

func TestSlotHandle_DoubleReleasePanics(t *testing.T) {
	r := NewRingAlloc(256)
	_, h := r.Alloc(64)
	require.NotNil(t, h)

	h.Release()
	assert.Panics(t, func() { h.Release() }, "double release should panic (refs < 0)")
}

// ---------------------------------------------------------------------------
// RingAlloc — head advancement past consecutive done slots
// ---------------------------------------------------------------------------

func TestRingAlloc_HeadAdvancesPastConsecutiveDone(t *testing.T) {
	r := NewRingAlloc(256)

	_, h1 := r.Alloc(32)
	_, h2 := r.Alloc(32)
	_, h3 := r.Alloc(32)
	require.NotNil(t, h3)

	// Release out of order: slot 0 and 1, but not 2.
	h1.Release()
	h2.Release()

	// Head should have advanced past slots 0 and 1.
	head := r.head.Load()
	assert.Equal(t, uint64(2), head, "head should advance past 2 consecutive done slots")

	h3.Release()
	head = r.head.Load()
	assert.Equal(t, uint64(3), head)
}

func TestRingAlloc_HeadBlockedByLiveMiddleSlot(t *testing.T) {
	r := NewRingAlloc(256)

	_, h1 := r.Alloc(32)
	_, h2 := r.Alloc(32)
	_, h3 := r.Alloc(32)
	require.NotNil(t, h3)

	// Release slot 0 and 2, but keep slot 1 alive.
	h1.Release()
	h3.Release()

	// Head should stop at slot 1 (still live).
	head := r.head.Load()
	assert.Equal(t, uint64(1), head)

	// Now release slot 1 — head should jump to 3.
	h2.Release()
	head = r.head.Load()
	assert.Equal(t, uint64(3), head)
}

// ---------------------------------------------------------------------------
// RingAlloc — Extend
// ---------------------------------------------------------------------------

func TestRingAlloc_ExtendBasic(t *testing.T) {
	r := NewRingAlloc(256)

	buf1, h := r.Alloc(32)
	require.NotNil(t, buf1)

	// Write marker.
	buf1[0] = 0xDE

	extended, ok := r.Extend(h, 16)
	require.True(t, ok)
	assert.Equal(t, 48, len(extended), "extended slice should be 32+16")
	assert.Equal(t, byte(0xDE), extended[0], "original data preserved")

	h.Release()
}

func TestRingAlloc_ExtendFailsWhenNotMostRecent(t *testing.T) {
	r := NewRingAlloc(256)

	_, h1 := r.Alloc(32)
	_, h2 := r.Alloc(32)
	require.NotNil(t, h2)

	// Extending h1 should fail — h2 was allocated after.
	_, ok := r.Extend(h1, 16)
	assert.False(t, ok)

	h1.Release()
	h2.Release()
}

func TestRingAlloc_ExtendFailsOverCapacity(t *testing.T) {
	r := NewRingAlloc(64)

	_, h := r.Alloc(60)
	require.NotNil(t, h)

	// Only 4 bytes left, asking for 8 more.
	_, ok := r.Extend(h, 8)
	assert.False(t, ok)

	h.Release()
}

func TestRingAlloc_ExtendNilHandle(t *testing.T) {
	r := NewRingAlloc(256)

	_, ok := r.Extend(nil, 16)
	assert.False(t, ok)
}

func TestRingAlloc_ExtendZeroSize(t *testing.T) {
	r := NewRingAlloc(256)
	_, h := r.Alloc(32)
	require.NotNil(t, h)

	_, ok := r.Extend(h, 0)
	assert.False(t, ok)

	h.Release()
}

func TestRingAlloc_ExtendMultipleTimes(t *testing.T) {
	r := NewRingAlloc(256)

	buf, h := r.Alloc(10)
	require.NotNil(t, buf)

	for range 5 {
		extended, ok := r.Extend(h, 10)
		require.True(t, ok)
		buf = extended
	}
	assert.Equal(t, 60, len(buf), "10 + 5*10 = 60")

	h.Release()
}

// ---------------------------------------------------------------------------
// RingAlloc — regionFree
// ---------------------------------------------------------------------------

func TestRingAlloc_RegionFreeWriteAheadOfRead(t *testing.T) {
	r := NewRingAlloc(100)
	// Simulate: write=50, read=20 → live is [20,50), free is [0,20) ∪ [50,100).
	r.write = 50

	assert.True(t, r.regionFree(0, 10, 20), "[0,10) in free zone [0,20)")
	assert.True(t, r.regionFree(60, 80, 20), "[60,80) in free zone [50,100)")
	assert.False(t, r.regionFree(10, 30, 20), "[10,30) overlaps live [20,50)")
	assert.False(t, r.regionFree(40, 60, 20), "[40,60) overlaps live [20,50)")
}

func TestRingAlloc_RegionFreeWriteBehindRead(t *testing.T) {
	r := NewRingAlloc(100)
	// Simulate: write=20, read=80 → live is [0,20) ∪ [80,100), free is [20,80).
	r.write = 20

	assert.True(t, r.regionFree(30, 50, 80), "[30,50) in free zone [20,80)")
	assert.False(t, r.regionFree(10, 30, 80), "[10,30) overlaps live [0,20)")
	assert.False(t, r.regionFree(70, 90, 80), "[70,90) overlaps live [80,100)")
}

// ---------------------------------------------------------------------------
// GrowingRingAlloc — basic
// ---------------------------------------------------------------------------

func TestGrowingRingAlloc_BasicAlloc(t *testing.T) {
	g := NewGrowingRingAlloc(256)
	buf, h := g.Alloc(100)

	require.NotNil(t, buf)
	assert.Equal(t, 100, len(buf))
	h.Release()
}

func TestGrowingRingAlloc_GrowsWhenFull(t *testing.T) {
	g := NewGrowingRingAlloc(64)

	// Fill the initial ring.
	_, h1 := g.Alloc(64)
	require.NotNil(t, h1)

	// Next alloc should trigger growth to a new ring.
	buf2, h2 := g.Alloc(32)
	require.NotNil(t, buf2, "should succeed after growth")
	assert.Equal(t, 32, len(buf2))

	h1.Release()
	h2.Release()
}

func TestGrowingRingAlloc_GrowsLargeEnough(t *testing.T) {
	g := NewGrowingRingAlloc(64)

	// Fill the initial ring.
	_, h1 := g.Alloc(64)
	require.NotNil(t, h1)

	// Request something larger than the original capacity.
	buf, h2 := g.Alloc(128)
	require.NotNil(t, buf, "growth should accommodate larger-than-original alloc")
	assert.Equal(t, 128, len(buf))

	h1.Release()
	h2.Release()
}

func TestGrowingRingAlloc_OldRingBecomesGCEligible(t *testing.T) {
	g := NewGrowingRingAlloc(64)

	// Allocate on old ring, keep handle.
	buf1, h1 := g.Alloc(64)
	require.NotNil(t, buf1)

	// Force growth.
	_, h2 := g.Alloc(32)
	require.NotNil(t, h2)

	// Old handle is still valid — data should be accessible.
	buf1[0] = 0xFF
	assert.Equal(t, byte(0xFF), buf1[0])

	h1.Release()
	h2.Release()
}

// ---------------------------------------------------------------------------
// GrowingRingAlloc — Extend
// ---------------------------------------------------------------------------

func TestGrowingRingAlloc_ExtendOnCurrentRing(t *testing.T) {
	g := NewGrowingRingAlloc(256)

	buf, h := g.Alloc(32)
	require.NotNil(t, buf)
	buf[0] = 0xAB

	extended, ok := g.Extend(h, 32)
	require.True(t, ok)
	assert.Equal(t, 64, len(extended))
	assert.Equal(t, byte(0xAB), extended[0])

	h.Release()
}

func TestGrowingRingAlloc_ExtendFailsOnOldRing(t *testing.T) {
	g := NewGrowingRingAlloc(64)

	_, h1 := g.Alloc(64) // fills ring
	require.NotNil(t, h1)

	_, h2 := g.Alloc(32) // triggers growth
	require.NotNil(t, h2)

	// h1 belongs to the old ring, Extend should fail.
	_, ok := g.Extend(h1, 16)
	assert.False(t, ok, "should not extend handle from old ring")

	h1.Release()
	h2.Release()
}

func TestGrowingRingAlloc_ExtendNilHandle(t *testing.T) {
	g := NewGrowingRingAlloc(256)
	_, ok := g.Extend(nil, 16)
	assert.False(t, ok)
}

// ---------------------------------------------------------------------------
// GrowingRingAlloc — multiple growth cycles
// ---------------------------------------------------------------------------

func TestGrowingRingAlloc_MultipleGrowths(t *testing.T) {
	g := NewGrowingRingAlloc(32)

	var handles []*SlotHandle
	for range 20 {
		_, h := g.Alloc(32)
		require.NotNil(t, h, "allocation should always succeed via growth")
		handles = append(handles, h)
	}

	for _, h := range handles {
		h.Release()
	}
}

// ---------------------------------------------------------------------------
// Concurrency — concurrent Release from multiple consumers
// ---------------------------------------------------------------------------

func TestRingAlloc_ConcurrentRelease(t *testing.T) {
	r := NewRingAlloc(1024 * 1024) // 1 MB
	const N = 1000

	handles := make([]*SlotHandle, N)
	for i := range N {
		_, h := r.Alloc(64)
		require.NotNil(t, h, "alloc %d", i)
		handles[i] = h
	}

	// Release from N goroutines concurrently.
	var wg sync.WaitGroup
	wg.Add(N)
	for i := range N {
		go func(idx int) {
			defer wg.Done()
			handles[idx].Release()
		}(i)
	}
	wg.Wait()

	// All slots freed — head should equal tail.
	assert.Equal(t, r.head.Load(), r.tail.Load(), "all slots should be freed")

	// Ring should be fully usable again.
	buf, h := r.Alloc(512)
	require.NotNil(t, buf)
	h.Release()
}

func TestRingAlloc_ProducerConsumerConcurrency(t *testing.T) {
	r := NewRingAlloc(64 * 1024) // 64 KB
	const packets = 5000

	var produced atomic.Int64
	var consumed atomic.Int64

	ch := make(chan *SlotHandle, 64)

	// Producer goroutine.
	go func() {
		defer close(ch)
		for range packets {
			for {
				_, h := r.Alloc(32)
				if h != nil {
					produced.Add(1)
					ch <- h
					break
				}
				// Ring full — yield and retry.
				runtime.Gosched()
			}
		}
	}()

	// Consumer goroutines.
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for h := range ch {
				consumed.Add(1)
				h.Release()
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(packets), produced.Load())
	assert.Equal(t, int64(packets), consumed.Load())
	assert.Equal(t, r.head.Load(), r.tail.Load(), "ring should be empty after drain")
}

func TestRingAlloc_ConcurrentRetainRelease(t *testing.T) {
	r := NewRingAlloc(4096)
	const N = 100
	const consumers = 4

	for range N {
		_, h := r.Alloc(8)
		require.NotNil(t, h)

		// Simulate Clone(false) fan-out: Retain for each extra consumer.
		for range consumers - 1 {
			h.Retain()
		}

		var wg sync.WaitGroup
		wg.Add(consumers)
		for range consumers {
			go func() {
				defer wg.Done()
				h.Release()
			}()
		}
		wg.Wait()
	}

	assert.Equal(t, r.head.Load(), r.tail.Load(), "all slots should be freed")
}

// ---------------------------------------------------------------------------
// RingAlloc — stress test: many alloc/release cycles
// ---------------------------------------------------------------------------

func TestRingAlloc_StressCycles(t *testing.T) {
	r := NewRingAlloc(4096)

	for range 10000 {
		buf, h := r.Alloc(16)
		require.NotNil(t, buf)
		// Write and verify.
		buf[0] = 0xFE
		assert.Equal(t, byte(0xFE), buf[0])
		h.Release()
	}
}

// ---------------------------------------------------------------------------
// Benchmarks
// ---------------------------------------------------------------------------

func BenchmarkPooledBuffer_GetRelease_Small(b *testing.B) {
	for b.Loop() {
		buf := Get(128)
		buf.Release()
	}
}

func BenchmarkPooledBuffer_GetRelease_Big(b *testing.B) {
	for b.Loop() {
		buf := Get(bigBufSize)
		buf.Release()
	}
}

func BenchmarkRingAlloc_AllocRelease(b *testing.B) {
	r := NewRingAlloc(1024 * 1024)
	for b.Loop() {
		_, h := r.Alloc(64)
		h.Release()
	}
}

func BenchmarkRingAlloc_RetainRelease(b *testing.B) {
	r := NewRingAlloc(1024 * 1024)
	_, h := r.Alloc(64)
	// Keep one ref alive, benchmark Retain+Release overhead.
	for b.Loop() {
		h.Retain()
		h.Release()
	}
	h.Release()
}

func BenchmarkGrowingRingAlloc_AllocRelease(b *testing.B) {
	g := NewGrowingRingAlloc(1024 * 1024)
	for b.Loop() {
		_, h := g.Alloc(64)
		h.Release()
	}
}
