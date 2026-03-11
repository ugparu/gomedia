package buffer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Get — pool selection & sizing
// ---------------------------------------------------------------------------

func TestGet_SmallBuffer(t *testing.T) {
	buf := Get(100)
	defer buf.Release()

	assert.Equal(t, 100, buf.Len())
	assert.GreaterOrEqual(t, buf.Cap(), 100)
	assert.Equal(t, 100, len(buf.Data()))
}

func TestGet_ExactDefaultSize(t *testing.T) {
	buf := Get(defaultBufSize)
	defer buf.Release()

	assert.Equal(t, defaultBufSize, buf.Len())
}

func TestGet_BigBuffer(t *testing.T) {
	buf := Get(bigBufSize)
	defer buf.Release()

	assert.Equal(t, bigBufSize, buf.Len())
	assert.GreaterOrEqual(t, buf.Cap(), bigBufSize)
}

func TestGet_LargerThanBigTier(t *testing.T) {
	size := bigBufSize + 1024
	buf := Get(size)
	defer buf.Release()

	assert.Equal(t, size, buf.Len())
	assert.GreaterOrEqual(t, buf.Cap(), size)
}

func TestGet_ZeroSize(t *testing.T) {
	buf := Get(0)
	defer buf.Release()

	assert.Equal(t, 0, buf.Len())
}

func TestGet_OverMaxBufSize(t *testing.T) {
	size := maxBufSize + 1
	buf := Get(size)
	// Release of oversized buffer should not panic (it just skips the pool).
	buf.Release()

	assert.Equal(t, size, buf.Len())
}

// ---------------------------------------------------------------------------
// Data — writable slice
// ---------------------------------------------------------------------------

func TestData_IsWritable(t *testing.T) {
	buf := Get(16)
	defer buf.Release()

	d := buf.Data()
	for i := range d {
		d[i] = byte(i)
	}
	for i, b := range buf.Data() {
		assert.Equal(t, byte(i), b)
	}
}

// ---------------------------------------------------------------------------
// Resize
// ---------------------------------------------------------------------------

func TestResize_WithinCapacity(t *testing.T) {
	buf := Get(100)
	defer buf.Release()

	origCap := buf.Cap()
	buf.Resize(50)

	assert.Equal(t, 50, buf.Len())
	assert.Equal(t, origCap, buf.Cap(), "cap should not change when shrinking")
}

func TestResize_GrowWithinCapacity(t *testing.T) {
	buf := Get(10)
	defer buf.Release()

	// The pool gives at least defaultBufSize capacity, so growing to 100 stays in-place.
	buf.Resize(100)
	assert.Equal(t, 100, buf.Len())
}

func TestResize_BeyondCapacity(t *testing.T) {
	buf := Get(10)
	defer buf.Release()

	// Write a marker.
	buf.Data()[0] = 0xAB

	// Force reallocation by asking for more than the pool tier provides.
	newSize := buf.Cap() + 1024
	buf.Resize(newSize)

	assert.Equal(t, newSize, buf.Len())
	assert.Equal(t, byte(0xAB), buf.Data()[0], "existing data should be preserved")
}

// ---------------------------------------------------------------------------
// Release — pool recycling
// ---------------------------------------------------------------------------

func TestRelease_SmallBufferReturnsToPool(t *testing.T) {
	// Get and release, then get again — should reuse the pooled object.
	buf := Get(100)
	buf.Release()

	buf2 := Get(100)
	defer buf2.Release()

	assert.Equal(t, 100, buf2.Len())
}

func TestRelease_BigBufferReturnsToPool(t *testing.T) {
	buf := Get(bigBufSize)
	buf.Release()

	buf2 := Get(bigBufSize)
	defer buf2.Release()

	assert.Equal(t, bigBufSize, buf2.Len())
}

func TestRelease_OversizedBufferNotPooled(t *testing.T) {
	// Buffers > maxBufSize are dropped, not pooled. Should not panic.
	buf := Get(maxBufSize + 1)
	require.NotPanics(t, func() { buf.Release() })
}

// ---------------------------------------------------------------------------
// Multiple cycles — verify stability
// ---------------------------------------------------------------------------

func TestPooledBuffer_MultipleCycles(t *testing.T) {
	for i := 0; i < 1000; i++ {
		size := (i % 5) * defaultBufSize
		buf := Get(size)
		assert.Equal(t, size, buf.Len())
		buf.Release()
	}
}
