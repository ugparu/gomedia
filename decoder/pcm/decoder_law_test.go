package pcm_test

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/ugparu/gomedia"
	decPcm "github.com/ugparu/gomedia/decoder/pcm"
	"github.com/ugparu/gomedia/utils/buffer"
)

// ---------------------------------------------------------------------------
// Test data helpers
// ---------------------------------------------------------------------------

type lawPacketJSON struct {
	Codec       string `json:"codec"`
	StreamIndex uint8  `json:"stream_index"`
	TimestampNs int64  `json:"timestamp_ns"`
	DurationNs  int64  `json:"duration_ns"`
	Size        int    `json:"size"`
	Data        string `json:"data"` // base64-encoded raw law payload
}

type lawPacketsJSON struct {
	Packets []lawPacketJSON `json:"packets"`
}

// loadLawPackets reads a JSON packets file and base64-decodes each packet's
// data field, returning the raw encoded bytes for every packet.
func loadLawPackets(t *testing.T, path string) [][]byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "read %s", path)

	var pf lawPacketsJSON
	require.NoError(t, json.Unmarshal(raw, &pf))
	require.NotEmpty(t, pf.Packets)

	frames := make([][]byte, len(pf.Packets))
	for i, p := range pf.Packets {
		data, decErr := base64.StdEncoding.DecodeString(p.Data)
		require.NoError(t, decErr, "packet %d base64 decode", i)
		require.Equal(t, p.Size, len(data), "packet %d declared size mismatch", i)
		frames[i] = data
	}
	return frames
}

const (
	alawDataPath  = "../../tests/data/alaw/packets.json"
	mulawDataPath = "../../tests/data/mulaw/packets.json"
)

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

func TestNewALAWDecoder(t *testing.T) {
	t.Parallel()
	d := decPcm.NewALAWDecoder()
	require.NotNil(t, d)
}

func TestNewULAWDecoder(t *testing.T) {
	t.Parallel()
	d := decPcm.NewULAWDecoder()
	require.NotNil(t, d)
}

// ---------------------------------------------------------------------------
// Init
// ---------------------------------------------------------------------------

// TestInit_ALAW verifies that Init accepts a nil AudioCodecParameters value.
// The law decoder ignores its argument entirely.
func TestInit_ALAW(t *testing.T) {
	t.Parallel()
	d := decPcm.NewALAWDecoder()
	var nilParam gomedia.AudioCodecParameters
	require.NoError(t, d.Init(nilParam))
}

// TestInit_ULAW verifies that Init accepts a nil AudioCodecParameters value.
func TestInit_ULAW(t *testing.T) {
	t.Parallel()
	d := decPcm.NewULAWDecoder()
	var nilParam gomedia.AudioCodecParameters
	require.NoError(t, d.Init(nilParam))
}

// ---------------------------------------------------------------------------
// Close
// ---------------------------------------------------------------------------

func TestClose_ALAW(t *testing.T) {
	t.Parallel()
	d := decPcm.NewALAWDecoder()
	require.NotPanics(t, d.Close)
}

func TestClose_ULAW(t *testing.T) {
	t.Parallel()
	d := decPcm.NewULAWDecoder()
	require.NotPanics(t, d.Close)
}

// ---------------------------------------------------------------------------
// Decode — nil ring (heap output)
// ---------------------------------------------------------------------------

// TestDecode_ALAW_NilRing decodes every packet from the alaw test fixture with
// a nil ring and verifies that each decoded output is exactly 2× the input
// size (8-bit A-law → 16-bit PCM) and the slot is nil.
func TestDecode_ALAW_NilRing(t *testing.T) {
	t.Parallel()
	frames := loadLawPackets(t, alawDataPath)
	d := decPcm.NewALAWDecoder()
	var nilParam gomedia.AudioCodecParameters
	require.NoError(t, d.Init(nilParam))

	for i, frame := range frames {
		outData, slot, err := d.Decode(frame, nil)
		require.NoError(t, err, "frame %d", i)
		require.Equal(t, 2*len(frame), len(outData),
			"frame %d: expected 2× input bytes (8-bit alaw → 16-bit PCM)", i)
		require.Nil(t, slot, "frame %d: expected nil slot for nil ring", i)
	}
}

// TestDecode_ULAW_NilRing decodes every packet from the mulaw test fixture
// with a nil ring and verifies that each decoded output is exactly 2× the
// input size and the slot is nil.
func TestDecode_ULAW_NilRing(t *testing.T) {
	t.Parallel()
	frames := loadLawPackets(t, mulawDataPath)
	d := decPcm.NewULAWDecoder()
	var nilParam gomedia.AudioCodecParameters
	require.NoError(t, d.Init(nilParam))

	for i, frame := range frames {
		outData, slot, err := d.Decode(frame, nil)
		require.NoError(t, err, "frame %d", i)
		require.Equal(t, 2*len(frame), len(outData),
			"frame %d: expected 2× input bytes (8-bit ulaw → 16-bit PCM)", i)
		require.Nil(t, slot, "frame %d: expected nil slot for nil ring", i)
	}
}

// ---------------------------------------------------------------------------
// Decode — ring allocator path
// ---------------------------------------------------------------------------

// TestDecode_ALAW_WithRingAlloc decodes one A-law frame with a GrowingRingAlloc,
// verifies the slot is non-nil, the data has the correct size, and releases
// the slot.
func TestDecode_ALAW_WithRingAlloc(t *testing.T) {
	t.Parallel()
	frames := loadLawPackets(t, alawDataPath)
	d := decPcm.NewALAWDecoder()
	var nilParam gomedia.AudioCodecParameters
	require.NoError(t, d.Init(nilParam))

	ring := buffer.NewGrowingRingAlloc(64 * 1024)
	frame := frames[0]

	outData, slot, err := d.Decode(frame, ring)
	require.NoError(t, err)
	require.Equal(t, 2*len(frame), len(outData),
		"expected 2× input bytes (8-bit alaw → 16-bit PCM)")
	require.NotNil(t, slot, "expected non-nil slot when ring allocation succeeds")
	slot.Release()
}

// TestDecode_ULAW_WithRingAlloc decodes one mu-law frame with a GrowingRingAlloc,
// verifies the slot is non-nil, the data has the correct size, and releases
// the slot.
func TestDecode_ULAW_WithRingAlloc(t *testing.T) {
	t.Parallel()
	frames := loadLawPackets(t, mulawDataPath)
	d := decPcm.NewULAWDecoder()
	var nilParam gomedia.AudioCodecParameters
	require.NoError(t, d.Init(nilParam))

	ring := buffer.NewGrowingRingAlloc(64 * 1024)
	frame := frames[0]

	outData, slot, err := d.Decode(frame, ring)
	require.NoError(t, err)
	require.Equal(t, 2*len(frame), len(outData),
		"expected 2× input bytes (8-bit ulaw → 16-bit PCM)")
	require.NotNil(t, slot, "expected non-nil slot when ring allocation succeeds")
	slot.Release()
}

// ---------------------------------------------------------------------------
// Decode — full / tiny ring (graceful fallback)
// ---------------------------------------------------------------------------

// TestDecode_ALAW_WithFullRing passes a very small initial ring (1 byte).
// The law decoder decodes via the g711 library first and then copies into the
// ring. Because the decoded output is 2048 bytes and the growing ring will
// expand to accommodate it, PCM is still produced. Any returned slot is
// released to avoid leaks.
func TestDecode_ALAW_WithFullRing(t *testing.T) {
	t.Parallel()
	frames := loadLawPackets(t, alawDataPath)
	d := decPcm.NewALAWDecoder()
	var nilParam gomedia.AudioCodecParameters
	require.NoError(t, d.Init(nilParam))

	// A 1-byte initial ring — GrowingRingAlloc will grow on first alloc.
	// The decoder must still produce correct output.
	ring := buffer.NewGrowingRingAlloc(1)
	frame := frames[0]

	outData, slot, err := d.Decode(frame, ring)
	require.NoError(t, err)
	require.Equal(t, 2*len(frame), len(outData),
		"expected 2× input bytes even with small initial ring")
	if slot != nil {
		slot.Release()
	}
}

// TestDecode_ULAW_WithFullRing passes a very small initial ring (1 byte) for
// the mu-law decoder and verifies PCM is still produced correctly.
func TestDecode_ULAW_WithFullRing(t *testing.T) {
	t.Parallel()
	frames := loadLawPackets(t, mulawDataPath)
	d := decPcm.NewULAWDecoder()
	var nilParam gomedia.AudioCodecParameters
	require.NoError(t, d.Init(nilParam))

	// A 1-byte initial ring — GrowingRingAlloc will grow on first alloc.
	ring := buffer.NewGrowingRingAlloc(1)
	frame := frames[0]

	outData, slot, err := d.Decode(frame, ring)
	require.NoError(t, err)
	require.Equal(t, 2*len(frame), len(outData),
		"expected 2× input bytes even with small initial ring")
	if slot != nil {
		slot.Release()
	}
}
