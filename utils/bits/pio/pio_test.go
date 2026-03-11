package pio

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// Readers — big-endian
// ---------------------------------------------------------------------------

func TestU8(t *testing.T) {
	assert.Equal(t, uint8(0xAB), U8([]byte{0xAB}))
}

func TestU16BE(t *testing.T) {
	assert.Equal(t, uint16(0xDEAD), U16BE([]byte{0xDE, 0xAD}))
}

func TestI16BE(t *testing.T) {
	tests := []struct {
		name string
		b    []byte
		want int16
	}{
		{"positive", []byte{0x00, 0x7F}, 127},
		{"negative", []byte{0xFF, 0x80}, -128},
		{"zero", []byte{0x00, 0x00}, 0},
		{"minus_one", []byte{0xFF, 0xFF}, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, I16BE(tt.b))
		})
	}
}

func TestI24BE(t *testing.T) {
	tests := []struct {
		name string
		b    []byte
		want int32
	}{
		{"positive", []byte{0x00, 0x01, 0x00}, 256},
		{"negative", []byte{0xFF, 0xFF, 0x00}, -256},
		{"zero", []byte{0x00, 0x00, 0x00}, 0},
		{"max", []byte{0x7F, 0xFF, 0xFF}, 0x7FFFFF},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, I24BE(tt.b))
		})
	}
}

func TestU24BE(t *testing.T) {
	assert.Equal(t, uint32(0xABCDEF), U24BE([]byte{0xAB, 0xCD, 0xEF}))
}

func TestI32BE(t *testing.T) {
	tests := []struct {
		name string
		b    []byte
		want int32
	}{
		{"positive", []byte{0x00, 0x00, 0x01, 0x00}, 256},
		{"negative", []byte{0xFF, 0xFF, 0xFF, 0x00}, -256},
		{"max", []byte{0x7F, 0xFF, 0xFF, 0xFF}, 0x7FFFFFFF},
		{"min", []byte{0x80, 0x00, 0x00, 0x00}, -2147483648},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, I32BE(tt.b))
		})
	}
}

func TestU32BE(t *testing.T) {
	assert.Equal(t, uint32(0xDEADBEEF), U32BE([]byte{0xDE, 0xAD, 0xBE, 0xEF}))
}

func TestU32LE(t *testing.T) {
	// Little-endian: bytes reversed.
	assert.Equal(t, uint32(0xDEADBEEF), U32LE([]byte{0xEF, 0xBE, 0xAD, 0xDE}))
}

func TestU40BE(t *testing.T) {
	assert.Equal(t, uint64(0xABCDEF0123), U40BE([]byte{0xAB, 0xCD, 0xEF, 0x01, 0x23}))
}

func TestU64BE(t *testing.T) {
	assert.Equal(t, uint64(0xDEADBEEFCAFEBABE), U64BE([]byte{0xDE, 0xAD, 0xBE, 0xEF, 0xCA, 0xFE, 0xBA, 0xBE}))
}

func TestI64BE(t *testing.T) {
	tests := []struct {
		name string
		b    []byte
		want int64
	}{
		{"positive", []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00}, 256},
		{"negative", []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x00}, -256},
		{"minus_one", []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, I64BE(tt.b))
		})
	}
}

// ---------------------------------------------------------------------------
// Writers — big-endian
// ---------------------------------------------------------------------------

func TestPutU8(t *testing.T) {
	b := make([]byte, 1)
	PutU8(b, 0xAB)
	assert.Equal(t, []byte{0xAB}, b)
}

func TestPutU16BE(t *testing.T) {
	b := make([]byte, 2)
	PutU16BE(b, 0xDEAD)
	assert.Equal(t, []byte{0xDE, 0xAD}, b)
}

func TestPutI16BE(t *testing.T) {
	b := make([]byte, 2)
	PutI16BE(b, -1)
	assert.Equal(t, []byte{0xFF, 0xFF}, b)
}

func TestPutU24BE(t *testing.T) {
	b := make([]byte, 3)
	PutU24BE(b, 0xABCDEF)
	assert.Equal(t, []byte{0xAB, 0xCD, 0xEF}, b)
}

func TestPutI24BE(t *testing.T) {
	b := make([]byte, 3)
	PutI24BE(b, -256)
	assert.Equal(t, []byte{0xFF, 0xFF, 0x00}, b)
}

func TestPutU32BE(t *testing.T) {
	b := make([]byte, 4)
	PutU32BE(b, 0xDEADBEEF)
	assert.Equal(t, []byte{0xDE, 0xAD, 0xBE, 0xEF}, b)
}

func TestPutI32BE(t *testing.T) {
	b := make([]byte, 4)
	PutI32BE(b, -1)
	assert.Equal(t, []byte{0xFF, 0xFF, 0xFF, 0xFF}, b)
}

func TestPutU32LE(t *testing.T) {
	b := make([]byte, 4)
	PutU32LE(b, 0xDEADBEEF)
	assert.Equal(t, []byte{0xEF, 0xBE, 0xAD, 0xDE}, b)
}

func TestPutU40BE(t *testing.T) {
	b := make([]byte, 5)
	PutU40BE(b, 0xABCDEF0123)
	assert.Equal(t, []byte{0xAB, 0xCD, 0xEF, 0x01, 0x23}, b)
}

func TestPutU48BE(t *testing.T) {
	b := make([]byte, 6)
	PutU48BE(b, 0xABCDEF012345)
	assert.Equal(t, []byte{0xAB, 0xCD, 0xEF, 0x01, 0x23, 0x45}, b)
}

func TestPutU64BE(t *testing.T) {
	b := make([]byte, 8)
	PutU64BE(b, 0xDEADBEEFCAFEBABE)
	assert.Equal(t, []byte{0xDE, 0xAD, 0xBE, 0xEF, 0xCA, 0xFE, 0xBA, 0xBE}, b)
}

func TestPutI64BE(t *testing.T) {
	b := make([]byte, 8)
	PutI64BE(b, -1)
	assert.Equal(t, []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, b)
}

// ---------------------------------------------------------------------------
// Round-trip: Put → Read
// ---------------------------------------------------------------------------

func TestRoundTrip_U16BE(t *testing.T) {
	b := make([]byte, 2)
	PutU16BE(b, 0x1234)
	assert.Equal(t, uint16(0x1234), U16BE(b))
}

func TestRoundTrip_I32BE(t *testing.T) {
	b := make([]byte, 4)
	PutI32BE(b, -123456)
	assert.Equal(t, int32(-123456), I32BE(b))
}

func TestRoundTrip_U64BE(t *testing.T) {
	b := make([]byte, 8)
	PutU64BE(b, 0x0123456789ABCDEF)
	assert.Equal(t, uint64(0x0123456789ABCDEF), U64BE(b))
}

func TestRoundTrip_U32LE(t *testing.T) {
	b := make([]byte, 4)
	PutU32LE(b, 0x12345678)
	assert.Equal(t, uint32(0x12345678), U32LE(b))
}

// ---------------------------------------------------------------------------
// Vec functions
// ---------------------------------------------------------------------------

func TestVecLen(t *testing.T) {
	vec := [][]byte{{1, 2, 3}, {4, 5}, {6}}
	assert.Equal(t, 6, VecLen(vec))
}

func TestVecLen_Empty(t *testing.T) {
	assert.Equal(t, 0, VecLen(nil))
	assert.Equal(t, 0, VecLen([][]byte{}))
	assert.Equal(t, 0, VecLen([][]byte{{}}))
}

func TestVecSlice_Full(t *testing.T) {
	vec := [][]byte{{1, 2, 3}, {4, 5, 6}}
	result := VecSlice(vec, 0, -1) // -1 means "to the end"

	assert.Equal(t, 2, len(result))
	assert.Equal(t, []byte{1, 2, 3}, result[0])
	assert.Equal(t, []byte{4, 5, 6}, result[1])
}

func TestVecSlice_MiddleCut(t *testing.T) {
	vec := [][]byte{{1, 2, 3, 4}, {5, 6, 7, 8}}

	// Slice [2, 6): should give {3, 4} from first, {5, 6} from second.
	result := VecSlice(vec, 2, 6)
	combined := make([]byte, 0)
	for _, r := range result {
		combined = append(combined, r...)
	}
	assert.Equal(t, []byte{3, 4, 5, 6}, combined)
}

func TestVecSlice_SingleElement(t *testing.T) {
	vec := [][]byte{{10, 20, 30}}
	result := VecSlice(vec, 1, 2)

	assert.Equal(t, 1, len(result))
	assert.Equal(t, []byte{20}, result[0])
}

func TestVecSlice_StartEqualsEnd(t *testing.T) {
	vec := [][]byte{{1, 2, 3}}
	result := VecSlice(vec, 2, 2)
	assert.Equal(t, 0, len(result))
}

func TestVecSlice_PanicsOnInvertedRange(t *testing.T) {
	vec := [][]byte{{1, 2, 3}}
	assert.Panics(t, func() {
		VecSlice(vec, 2, 1)
	})
}

func TestVecSlice_PanicsOnStartOutOfRange(t *testing.T) {
	vec := [][]byte{{1, 2}}
	assert.Panics(t, func() {
		VecSlice(vec, 10, -1)
	})
}

func TestVecSlice_PanicsOnEndOutOfRange(t *testing.T) {
	vec := [][]byte{{1, 2}}
	assert.Panics(t, func() {
		VecSlice(vec, 0, 10)
	})
}

func TestVecSliceTo_NegativeStartClamped(t *testing.T) {
	vec := [][]byte{{1, 2, 3}}
	out := make([][]byte, 3)
	n := VecSliceTo(vec, out, -5, 2)

	combined := make([]byte, 0)
	for _, r := range out[:n] {
		combined = append(combined, r...)
	}
	assert.Equal(t, []byte{1, 2}, combined)
}
