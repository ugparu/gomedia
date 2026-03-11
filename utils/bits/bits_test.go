package bits

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Reader — ReadBits64
// ---------------------------------------------------------------------------

func TestReader_ReadBits64_SingleByte(t *testing.T) {
	r := &Reader{R: bytes.NewReader([]byte{0b10110100})}

	// Read 3 bits → 101 = 5
	bits, err := r.ReadBits64(3)
	require.NoError(t, err)
	assert.Equal(t, uint64(0b101), bits)

	// Read 5 bits → 10100 = 20
	bits, err = r.ReadBits64(5)
	require.NoError(t, err)
	assert.Equal(t, uint64(0b10100), bits)
}

func TestReader_ReadBits64_MultiByte(t *testing.T) {
	// 0xDEAD = 1101 1110 1010 1101
	r := &Reader{R: bytes.NewReader([]byte{0xDE, 0xAD})}

	bits, err := r.ReadBits64(16)
	require.NoError(t, err)
	assert.Equal(t, uint64(0xDEAD), bits)
}

func TestReader_ReadBits64_CrossByteBoundary(t *testing.T) {
	// Two bytes: 0xFF 0x00 = 11111111 00000000
	r := &Reader{R: bytes.NewReader([]byte{0xFF, 0x00})}

	// Read 4 bits → 1111
	bits, err := r.ReadBits64(4)
	require.NoError(t, err)
	assert.Equal(t, uint64(0xF), bits)

	// Read 8 bits crossing boundary → 1111 0000
	bits, err = r.ReadBits64(8)
	require.NoError(t, err)
	assert.Equal(t, uint64(0xF0), bits)
}

func TestReader_ReadBits64_EOF(t *testing.T) {
	r := &Reader{R: bytes.NewReader([]byte{0xAB})}

	// Reading 16 bits from a 1-byte source should fail.
	_, err := r.ReadBits64(16)
	assert.ErrorIs(t, err, io.EOF)
}

func TestReader_ReadBits(t *testing.T) {
	r := &Reader{R: bytes.NewReader([]byte{0b11001010})}

	bits, err := r.ReadBits(4)
	require.NoError(t, err)
	assert.Equal(t, uint(0b1100), bits)
}

// ---------------------------------------------------------------------------
// Reader — Read (io.Reader)
// ---------------------------------------------------------------------------

func TestReader_Read_AlignedBytes(t *testing.T) {
	input := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	r := &Reader{R: bytes.NewReader(input)}

	buf := make([]byte, 4)
	n, err := r.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, 4, n)
	assert.Equal(t, input, buf)
}

func TestReader_Read_AfterBitReads(t *testing.T) {
	// 0xAB = 10101011, 0xCD = 11001101
	r := &Reader{R: bytes.NewReader([]byte{0xAB, 0xCD})}

	// Read 4 bits → 1010
	bits, err := r.ReadBits64(4)
	require.NoError(t, err)
	assert.Equal(t, uint64(0xA), bits)

	// Read remaining as bytes → should yield 0xBC, 0xD?
	// Remaining bits: 1011 11001101 — reading 1 byte = 10111100 = 0xBC
	buf := make([]byte, 1)
	n, err := r.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, byte(0xBC), buf[0])
}

// ---------------------------------------------------------------------------
// Writer — WriteBits64
// ---------------------------------------------------------------------------

func TestWriter_WriteBits64_SingleValue(t *testing.T) {
	var buf bytes.Buffer
	w := &Writer{W: &buf}

	// Write 8 bits: 0xAB
	err := w.WriteBits64(0xAB, 8)
	require.NoError(t, err)
	err = w.FlushBits()
	require.NoError(t, err)

	assert.Equal(t, []byte{0xAB}, buf.Bytes())
}

func TestWriter_WriteBits64_SubByte(t *testing.T) {
	var buf bytes.Buffer
	w := &Writer{W: &buf}

	// Write 3 bits: 101
	err := w.WriteBits64(0b101, 3)
	require.NoError(t, err)
	// Write 5 bits: 10100
	err = w.WriteBits64(0b10100, 5)
	require.NoError(t, err)
	err = w.FlushBits()
	require.NoError(t, err)

	// 101 10100 = 0b10110100 = 0xB4
	assert.Equal(t, []byte{0xB4}, buf.Bytes())
}

func TestWriter_WriteBits64_FlushPadsWithZeros(t *testing.T) {
	var buf bytes.Buffer
	w := &Writer{W: &buf}

	// Write 3 bits: 111
	err := w.WriteBits64(0b111, 3)
	require.NoError(t, err)
	err = w.FlushBits()
	require.NoError(t, err)

	// 111 + 5 zero-padding bits = 11100000 = 0xE0
	assert.Equal(t, []byte{0xE0}, buf.Bytes())
}

func TestWriter_WriteBits64_MultiByte(t *testing.T) {
	var buf bytes.Buffer
	w := &Writer{W: &buf}

	err := w.WriteBits64(0xDEAD, 16)
	require.NoError(t, err)
	err = w.FlushBits()
	require.NoError(t, err)

	assert.Equal(t, []byte{0xDE, 0xAD}, buf.Bytes())
}

func TestWriter_WriteBits64_OverflowTo64Bits(t *testing.T) {
	var buf bytes.Buffer
	w := &Writer{W: &buf}

	// Write 56 bits, then 16 more to cross the 64-bit boundary and trigger
	// the internal flush path.
	err := w.WriteBits64(0xFFFFFFFFFFFFFF, 56)
	require.NoError(t, err)
	err = w.WriteBits64(0xABCD, 16)
	require.NoError(t, err)
	err = w.FlushBits()
	require.NoError(t, err)

	// 56 bits of 0xFF + 16 bits of 0xABCD = 72 bits → 9 bytes
	// When overflow fires at n=56+16=72 > 64:
	//   move = 64 - 56 = 8 → top 8 bits of 0xABCD (0xAB) fill the 64-bit accumulator
	//   flush 8 bytes, then remaining 8 bits (0xCD) buffered and flushed.
	expected := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xAB, 0xCD}
	assert.Equal(t, expected, buf.Bytes())
}

func TestWriter_WriteBits(t *testing.T) {
	var buf bytes.Buffer
	w := &Writer{W: &buf}

	err := w.WriteBits(0xAB, 8)
	require.NoError(t, err)
	err = w.FlushBits()
	require.NoError(t, err)

	assert.Equal(t, []byte{0xAB}, buf.Bytes())
}

// ---------------------------------------------------------------------------
// Writer — Write (io.Writer)
// ---------------------------------------------------------------------------

func TestWriter_Write_Bytes(t *testing.T) {
	var buf bytes.Buffer
	w := &Writer{W: &buf}

	data := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	n, err := w.Write(data)
	require.NoError(t, err)
	assert.Equal(t, 4, n)

	err = w.FlushBits()
	require.NoError(t, err)
	assert.Equal(t, data, buf.Bytes())
}

// ---------------------------------------------------------------------------
// Writer — FlushBits
// ---------------------------------------------------------------------------

func TestWriter_FlushBits_NothingBuffered(t *testing.T) {
	var buf bytes.Buffer
	w := &Writer{W: &buf}

	err := w.FlushBits()
	require.NoError(t, err)
	assert.Empty(t, buf.Bytes())
}

// ---------------------------------------------------------------------------
// Round-trip: Writer → Reader
// ---------------------------------------------------------------------------

func TestBits_RoundTrip(t *testing.T) {
	var buf bytes.Buffer
	w := &Writer{W: &buf}

	// Write: 5-bit value, 11-bit value, 16-bit value
	require.NoError(t, w.WriteBits64(0b10110, 5))
	require.NoError(t, w.WriteBits64(0b10101010101, 11))
	require.NoError(t, w.WriteBits64(0xCAFE, 16))
	require.NoError(t, w.FlushBits())

	r := &Reader{R: bytes.NewReader(buf.Bytes())}

	v1, err := r.ReadBits64(5)
	require.NoError(t, err)
	assert.Equal(t, uint64(0b10110), v1)

	v2, err := r.ReadBits64(11)
	require.NoError(t, err)
	assert.Equal(t, uint64(0b10101010101), v2)

	v3, err := r.ReadBits64(16)
	require.NoError(t, err)
	assert.Equal(t, uint64(0xCAFE), v3)
}

// ---------------------------------------------------------------------------
// GolombBitReader
// ---------------------------------------------------------------------------

func TestGolombBitReader_ReadBit(t *testing.T) {
	// 0b10110000
	r := &GolombBitReader{R: bytes.NewReader([]byte{0xB0})}

	expected := []uint{1, 0, 1, 1, 0, 0, 0, 0}
	for i, want := range expected {
		bit, err := r.ReadBit()
		require.NoError(t, err, "bit %d", i)
		assert.Equal(t, want, bit, "bit %d", i)
	}
}

func TestGolombBitReader_ReadBit_EOF(t *testing.T) {
	r := &GolombBitReader{R: bytes.NewReader(nil)}
	_, err := r.ReadBit()
	assert.Error(t, err)
}

func TestGolombBitReader_ReadBits(t *testing.T) {
	// 0xAB = 10101011
	r := &GolombBitReader{R: bytes.NewReader([]byte{0xAB})}

	bits, err := r.ReadBits(4)
	require.NoError(t, err)
	assert.Equal(t, uint(0b1010), bits) // 0xA

	bits, err = r.ReadBits(4)
	require.NoError(t, err)
	assert.Equal(t, uint(0b1011), bits) // 0xB
}

func TestGolombBitReader_ReadBits32(t *testing.T) {
	r := &GolombBitReader{R: bytes.NewReader([]byte{0xDE, 0xAD})}

	v, err := r.ReadBits32(16)
	require.NoError(t, err)
	assert.Equal(t, uint32(0xDEAD), v)
}

func TestGolombBitReader_ReadBits64(t *testing.T) {
	r := &GolombBitReader{R: bytes.NewReader([]byte{0xDE, 0xAD, 0xBE, 0xEF})}

	v, err := r.ReadBits64(32)
	require.NoError(t, err)
	assert.Equal(t, uint64(0xDEADBEEF), v)
}

func TestGolombBitReader_ReadExponentialGolombCode(t *testing.T) {
	// Exp-Golomb coding (used in H.264/H.265 SPS/PPS):
	//   value → code
	//   0     → 1                (1 bit)
	//   1     → 010              (3 bits)
	//   2     → 011              (3 bits)
	//   3     → 00100            (5 bits)
	//   4     → 00101            (5 bits)
	//   7     → 001 000          (5+1? no: 0001000 = 7 bits)
	tests := []struct {
		name  string
		input []byte
		want  uint
	}{
		{"value_0", []byte{0b10000000}, 0},         // 1 + padding
		{"value_1", []byte{0b01000000}, 1},         // 010 + padding
		{"value_2", []byte{0b01100000}, 2},         // 011 + padding
		{"value_3", []byte{0b00100000}, 3},         // 00100 + padding
		{"value_4", []byte{0b00101000}, 4},         // 00101 + padding
		{"value_5", []byte{0b00110000}, 5},         // 00110 + padding
		{"value_6", []byte{0b00111000}, 6},         // 00111 + padding
		{"value_7", []byte{0b00010000}, 7},         // 0001000 + padding
		{"value_8", []byte{0b00010010}, 8},         // 0001001 + padding
		{"value_14", []byte{0b00011110}, 14},       // 0001111 + padding
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &GolombBitReader{R: bytes.NewReader(tt.input)}
			got, err := r.ReadExponentialGolombCode()
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGolombBitReader_ReadExponentialGolombCode_Sequential(t *testing.T) {
	// Pack two Exp-Golomb codes: value 0 (1) + value 3 (00100) = 1 00100 + 00 padding = 10010000
	r := &GolombBitReader{R: bytes.NewReader([]byte{0b10010000})}

	v1, err := r.ReadExponentialGolombCode()
	require.NoError(t, err)
	assert.Equal(t, uint(0), v1)

	v2, err := r.ReadExponentialGolombCode()
	require.NoError(t, err)
	assert.Equal(t, uint(3), v2)
}
