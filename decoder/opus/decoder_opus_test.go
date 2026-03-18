package opus_test

import (
	"testing"

	"github.com/hraban/opus"
	"github.com/stretchr/testify/require"
	"github.com/ugparu/gomedia"
	gomediaOpus "github.com/ugparu/gomedia/codec/opus"
	decOpus "github.com/ugparu/gomedia/decoder/opus"
	"github.com/ugparu/gomedia/utils/buffer"
)

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

// buildOpusPacket encodes 960 samples of silence at 48000 Hz into a single
// Opus packet and returns the raw bytes. channels must be 1 or 2.
func buildOpusPacket(t *testing.T, sampleRate, channels int) []byte {
	t.Helper()
	enc, err := opus.NewEncoder(sampleRate, channels, opus.AppVoIP)
	require.NoError(t, err)

	const frameSamples = 960
	pcm := make([]int16, frameSamples*channels)
	buf := make([]byte, 4096)
	n, err := enc.Encode(pcm, buf)
	require.NoError(t, err)
	require.Greater(t, n, 0)
	return buf[:n]
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

func TestNewOpusDecoder(t *testing.T) {
	t.Parallel()
	d := decOpus.NewOpusDecoder()
	require.NotNil(t, d)
}

// ---------------------------------------------------------------------------
// Init
// ---------------------------------------------------------------------------

func TestInit_ValidMono(t *testing.T) {
	t.Parallel()
	d := decOpus.NewOpusDecoder()
	cp := gomediaOpus.NewCodecParameters(0, gomedia.ChMono, 48000)
	require.NoError(t, d.Init(cp))
}

func TestInit_ValidStereo(t *testing.T) {
	t.Parallel()
	d := decOpus.NewOpusDecoder()
	cp := gomediaOpus.NewCodecParameters(0, gomedia.ChStereo, 48000)
	require.NoError(t, d.Init(cp))
}

func TestInit_InvalidSampleRate(t *testing.T) {
	t.Parallel()
	d := decOpus.NewOpusDecoder()
	// 7000 Hz is not a supported Opus sample rate (valid: 8000,12000,16000,24000,48000).
	cp := gomediaOpus.NewCodecParameters(0, gomedia.ChMono, 7000)
	err := d.Init(cp)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// Close
// ---------------------------------------------------------------------------

func TestClose_WithoutInit(t *testing.T) {
	t.Parallel()
	d := decOpus.NewOpusDecoder()
	require.NotPanics(t, d.Close)
}

func TestClose_AfterInit(t *testing.T) {
	t.Parallel()
	d := decOpus.NewOpusDecoder()
	cp := gomediaOpus.NewCodecParameters(0, gomedia.ChMono, 48000)
	require.NoError(t, d.Init(cp))
	require.NotPanics(t, d.Close)
}

// ---------------------------------------------------------------------------
// Decode
// ---------------------------------------------------------------------------

func TestDecode_NilRing(t *testing.T) {
	t.Parallel()
	pkt := buildOpusPacket(t, 48000, 1)

	d := decOpus.NewOpusDecoder()
	cp := gomediaOpus.NewCodecParameters(0, gomedia.ChMono, 48000)
	require.NoError(t, d.Init(cp))

	outData, slot, err := d.Decode(pkt, nil)
	require.NoError(t, err)
	require.NotEmpty(t, outData)
	require.Nil(t, slot)
}

func TestDecode_WithRingAlloc(t *testing.T) {
	t.Parallel()
	pkt := buildOpusPacket(t, 48000, 1)

	d := decOpus.NewOpusDecoder()
	cp := gomediaOpus.NewCodecParameters(0, gomedia.ChMono, 48000)
	require.NoError(t, d.Init(cp))

	ring := buffer.NewGrowingRingAlloc(256 * 1024)
	outData, slot, err := d.Decode(pkt, ring)
	require.NoError(t, err)
	require.NotEmpty(t, outData)
	require.NotNil(t, slot, "expected non-nil slot when ring allocation succeeds")
	slot.Release()
}

// TestDecode_WithFullRing passes a very small initial ring (1 byte). The
// GrowingRingAlloc will grow to accommodate the allocation, so PCM is still
// produced. Any returned slot is released to avoid leaks.
func TestDecode_WithFullRing(t *testing.T) {
	t.Parallel()
	pkt := buildOpusPacket(t, 48000, 1)

	d := decOpus.NewOpusDecoder()
	cp := gomediaOpus.NewCodecParameters(0, gomedia.ChMono, 48000)
	require.NoError(t, d.Init(cp))

	// A 1-byte initial ring forces GrowingRingAlloc to grow on first alloc.
	// The decoder must still produce non-empty PCM.
	ring := buffer.NewGrowingRingAlloc(1)
	outData, slot, err := d.Decode(pkt, ring)
	require.NoError(t, err)
	require.NotEmpty(t, outData)
	if slot != nil {
		slot.Release()
	}
}

func TestDecode_Stereo_NilRing(t *testing.T) {
	t.Parallel()
	pkt := buildOpusPacket(t, 48000, 2)

	d := decOpus.NewOpusDecoder()
	cp := gomediaOpus.NewCodecParameters(0, gomedia.ChStereo, 48000)
	require.NoError(t, d.Init(cp))

	outData, slot, err := d.Decode(pkt, nil)
	require.NoError(t, err)
	require.Greater(t, len(outData), 0)
	require.Nil(t, slot)
}
