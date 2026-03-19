//nolint:mnd // Test file uses many literal values for expected results
package pcm_test

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/ugparu/gomedia"
	decPcm "github.com/ugparu/gomedia/decoder/pcm"
	"github.com/ugparu/gomedia/utils/buffer"
	"github.com/zaf/g711"
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
	Data        string `json:"data"`
}

type lawPacketsJSON struct {
	Packets []lawPacketJSON `json:"packets"`
}

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

func TestInit_ALAW(t *testing.T) {
	t.Parallel()
	d := decPcm.NewALAWDecoder()
	var nilParam gomedia.AudioCodecParameters
	require.NoError(t, d.Init(nilParam))
}

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
// Decode — output size: 8-bit law → 16-bit PCM (2× input size)
// ---------------------------------------------------------------------------

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
// Decode — data correctness: output must match g711 reference decode
// ---------------------------------------------------------------------------

func TestDecode_ALAW_DataCorrectness(t *testing.T) {
	t.Parallel()
	frames := loadLawPackets(t, alawDataPath)
	d := decPcm.NewALAWDecoder()
	var nilParam gomedia.AudioCodecParameters
	require.NoError(t, d.Init(nilParam))

	for i, frame := range frames {
		outData, _, err := d.Decode(frame, nil)
		require.NoError(t, err, "frame %d", i)

		// Verify sample-by-sample against g711 reference
		reference := g711.DecodeAlaw(frame)
		require.Equal(t, reference, outData,
			"frame %d: decoded PCM must match g711.DecodeAlaw reference", i)
	}
}

func TestDecode_ULAW_DataCorrectness(t *testing.T) {
	t.Parallel()
	frames := loadLawPackets(t, mulawDataPath)
	d := decPcm.NewULAWDecoder()
	var nilParam gomedia.AudioCodecParameters
	require.NoError(t, d.Init(nilParam))

	for i, frame := range frames {
		outData, _, err := d.Decode(frame, nil)
		require.NoError(t, err, "frame %d", i)

		reference := g711.DecodeUlaw(frame)
		require.Equal(t, reference, outData,
			"frame %d: decoded PCM must match g711.DecodeUlaw reference", i)
	}
}

// ---------------------------------------------------------------------------
// Decode — known values: verify a-law silence (0xD5 → 0x0008) decodes correctly
// ---------------------------------------------------------------------------

func TestDecode_ALAW_KnownSilence(t *testing.T) {
	t.Parallel()
	d := decPcm.NewALAWDecoder()
	var nilParam gomedia.AudioCodecParameters
	require.NoError(t, d.Init(nilParam))

	// A-law silence byte is 0xD5, which decodes to PCM sample 8
	input := []byte{0xD5}
	outData, _, err := d.Decode(input, nil)
	require.NoError(t, err)
	require.Equal(t, 2, len(outData), "one 8-bit sample → one 16-bit sample")

	sample := int16(binary.LittleEndian.Uint16(outData))
	expected := g711.DecodeAlawFrame(0xD5)
	require.Equal(t, expected, sample,
		"A-law 0xD5 must decode to PCM %d", expected)
}

func TestDecode_ULAW_KnownSilence(t *testing.T) {
	t.Parallel()
	d := decPcm.NewULAWDecoder()
	var nilParam gomedia.AudioCodecParameters
	require.NoError(t, d.Init(nilParam))

	// Mu-law silence byte is 0xFF, which decodes to PCM sample 0
	input := []byte{0xFF}
	outData, _, err := d.Decode(input, nil)
	require.NoError(t, err)
	require.Equal(t, 2, len(outData))

	sample := int16(binary.LittleEndian.Uint16(outData))
	expected := g711.DecodeUlawFrame(0xFF)
	require.Equal(t, expected, sample,
		"Mu-law 0xFF must decode to PCM %d", expected)
}

// ---------------------------------------------------------------------------
// Decode — ring allocator path
// ---------------------------------------------------------------------------

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
	require.Equal(t, 2*len(frame), len(outData))
	require.NotNil(t, slot, "expected non-nil slot when ring allocation succeeds")

	// Data via ring must match data without ring
	refData, _, _ := d.Decode(frame, nil)
	require.Equal(t, refData, outData,
		"ring-allocated output must be identical to heap-allocated output")

	slot.Release()
}

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
	require.Equal(t, 2*len(frame), len(outData))
	require.NotNil(t, slot, "expected non-nil slot when ring allocation succeeds")

	refData, _, _ := d.Decode(frame, nil)
	require.Equal(t, refData, outData,
		"ring-allocated output must be identical to heap-allocated output")

	slot.Release()
}

// ---------------------------------------------------------------------------
// Decode — GrowingRingAlloc with tiny initial size
// ---------------------------------------------------------------------------

func TestDecode_ALAW_WithSmallRing(t *testing.T) {
	t.Parallel()
	frames := loadLawPackets(t, alawDataPath)
	d := decPcm.NewALAWDecoder()
	var nilParam gomedia.AudioCodecParameters
	require.NoError(t, d.Init(nilParam))

	// 1-byte initial ring → GrowingRingAlloc will grow to accommodate
	ring := buffer.NewGrowingRingAlloc(1)
	frame := frames[0]

	outData, slot, err := d.Decode(frame, ring)
	require.NoError(t, err)
	require.Equal(t, 2*len(frame), len(outData))
	// GrowingRingAlloc always succeeds (it grows), so slot must be non-nil
	require.NotNil(t, slot, "GrowingRingAlloc must always succeed (it grows)")
	slot.Release()
}

func TestDecode_ULAW_WithSmallRing(t *testing.T) {
	t.Parallel()
	frames := loadLawPackets(t, mulawDataPath)
	d := decPcm.NewULAWDecoder()
	var nilParam gomedia.AudioCodecParameters
	require.NoError(t, d.Init(nilParam))

	ring := buffer.NewGrowingRingAlloc(1)
	frame := frames[0]

	outData, slot, err := d.Decode(frame, ring)
	require.NoError(t, err)
	require.Equal(t, 2*len(frame), len(outData))
	require.NotNil(t, slot, "GrowingRingAlloc must always succeed (it grows)")
	slot.Release()
}

// ---------------------------------------------------------------------------
// Decode — empty input
// ---------------------------------------------------------------------------

func TestDecode_ALAW_EmptyInput(t *testing.T) {
	t.Parallel()
	d := decPcm.NewALAWDecoder()
	var nilParam gomedia.AudioCodecParameters
	require.NoError(t, d.Init(nilParam))

	outData, slot, err := d.Decode([]byte{}, nil)
	require.NoError(t, err)
	require.Empty(t, outData, "empty input must produce empty output")
	require.Nil(t, slot)
}

// ---------------------------------------------------------------------------
// Decode — stateless: multiple calls produce consistent results
// ---------------------------------------------------------------------------

func TestDecode_ALAW_Stateless(t *testing.T) {
	t.Parallel()
	frames := loadLawPackets(t, alawDataPath)
	require.True(t, len(frames) >= 2)
	d := decPcm.NewALAWDecoder()
	var nilParam gomedia.AudioCodecParameters
	require.NoError(t, d.Init(nilParam))

	// Decode frame 0 twice — output must be identical (decoder is stateless)
	out1, _, _ := d.Decode(frames[0], nil)
	out2, _, _ := d.Decode(frames[0], nil)
	require.Equal(t, out1, out2, "stateless decoder must produce identical output for same input")
}
