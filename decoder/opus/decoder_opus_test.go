//nolint:mnd // Test file uses many literal values for expected results
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

const (
	frameSamples = 960 // 20ms at 48kHz
	bytesPerS16  = 2   // int16 = 2 bytes
)

// buildOpusPacket encodes frameSamples of silence at the given rate/channels
// into a single Opus packet.
func buildOpusPacket(t *testing.T, sampleRate, channels int) []byte {
	t.Helper()
	enc, err := opus.NewEncoder(sampleRate, channels, opus.AppVoIP)
	require.NoError(t, err)

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
// Decode — mono, nil ring
// ---------------------------------------------------------------------------

func TestDecode_Mono_NilRing(t *testing.T) {
	t.Parallel()
	pkt := buildOpusPacket(t, 48000, 1)

	d := decOpus.NewOpusDecoder()
	cp := gomediaOpus.NewCodecParameters(0, gomedia.ChMono, 48000)
	require.NoError(t, d.Init(cp))

	outData, slot, err := d.Decode(pkt, nil)
	require.NoError(t, err)
	require.Nil(t, slot, "nil ring must produce nil slot")

	// Opus decodes 960 samples × 1 channel × 2 bytes/sample = 1920 bytes
	expectedLen := frameSamples * 1 * bytesPerS16
	require.Equal(t, expectedLen, len(outData),
		"mono: expected %d bytes (960 samples × 1ch × 2 bytes)", expectedLen)
}

// ---------------------------------------------------------------------------
// Decode — stereo, nil ring
// ---------------------------------------------------------------------------

func TestDecode_Stereo_NilRing(t *testing.T) {
	t.Parallel()
	pkt := buildOpusPacket(t, 48000, 2)

	d := decOpus.NewOpusDecoder()
	cp := gomediaOpus.NewCodecParameters(0, gomedia.ChStereo, 48000)
	require.NoError(t, d.Init(cp))

	outData, slot, err := d.Decode(pkt, nil)
	require.NoError(t, err)
	require.Nil(t, slot)

	// Opus decodes 960 samples × 2 channels × 2 bytes/sample = 3840 bytes
	expectedLen := frameSamples * 2 * bytesPerS16
	require.Equal(t, expectedLen, len(outData),
		"stereo: expected %d bytes (960 samples × 2ch × 2 bytes)", expectedLen)
}

// ---------------------------------------------------------------------------
// Decode — with ring allocator
// ---------------------------------------------------------------------------

func TestDecode_WithRingAlloc(t *testing.T) {
	t.Parallel()
	pkt := buildOpusPacket(t, 48000, 1)

	d := decOpus.NewOpusDecoder()
	cp := gomediaOpus.NewCodecParameters(0, gomedia.ChMono, 48000)
	require.NoError(t, d.Init(cp))

	ring := buffer.NewGrowingRingAlloc(256 * 1024)
	outData, slot, err := d.Decode(pkt, ring)
	require.NoError(t, err)
	require.NotNil(t, slot, "ring must produce non-nil slot")

	expectedLen := frameSamples * 1 * bytesPerS16
	require.Equal(t, expectedLen, len(outData))

	slot.Release()
}

// ---------------------------------------------------------------------------
// Decode — ring vs heap must produce identical output
// ---------------------------------------------------------------------------

func TestDecode_RingMatchesHeap(t *testing.T) {
	t.Parallel()
	pkt := buildOpusPacket(t, 48000, 1)

	d := decOpus.NewOpusDecoder()
	cp := gomediaOpus.NewCodecParameters(0, gomedia.ChMono, 48000)
	require.NoError(t, d.Init(cp))

	heapData, _, err := d.Decode(pkt, nil)
	require.NoError(t, err)

	// Re-init decoder for ring path (Opus decoder state changes after decode)
	d2 := decOpus.NewOpusDecoder()
	require.NoError(t, d2.Init(cp))

	ring := buffer.NewGrowingRingAlloc(256 * 1024)
	ringData, slot, err := d2.Decode(pkt, ring)
	require.NoError(t, err)
	require.NotNil(t, slot)

	require.Equal(t, heapData, ringData,
		"ring-allocated output must be identical to heap-allocated output")

	slot.Release()
}

// ---------------------------------------------------------------------------
// Decode — tiny ring: GrowingRingAlloc grows, slot must be non-nil
// ---------------------------------------------------------------------------

func TestDecode_WithSmallRing(t *testing.T) {
	t.Parallel()
	pkt := buildOpusPacket(t, 48000, 1)

	d := decOpus.NewOpusDecoder()
	cp := gomediaOpus.NewCodecParameters(0, gomedia.ChMono, 48000)
	require.NoError(t, d.Init(cp))

	ring := buffer.NewGrowingRingAlloc(1)
	outData, slot, err := d.Decode(pkt, ring)
	require.NoError(t, err)
	require.NotEmpty(t, outData)
	// GrowingRingAlloc always succeeds by growing
	require.NotNil(t, slot, "GrowingRingAlloc must always succeed (it grows)")
	slot.Release()
}

// ---------------------------------------------------------------------------
// Decode — silence input produces all-zero PCM
// ---------------------------------------------------------------------------

func TestDecode_SilenceProducesZeroPCM(t *testing.T) {
	t.Parallel()
	// Encode 960 silence samples
	pkt := buildOpusPacket(t, 48000, 1)

	d := decOpus.NewOpusDecoder()
	cp := gomediaOpus.NewCodecParameters(0, gomedia.ChMono, 48000)
	require.NoError(t, d.Init(cp))

	outData, _, err := d.Decode(pkt, nil)
	require.NoError(t, err)

	// Opus may not produce exact zeros due to codec priming, but the output
	// should be very close to silence. Check that all samples are within a
	// small tolerance of zero.
	for i := 0; i+1 < len(outData); i += 2 {
		sample := int16(uint16(outData[i]) | uint16(outData[i+1])<<8)
		require.InDelta(t, 0, int(sample), 100,
			"silence input should decode to near-zero PCM (sample %d = %d)", i/2, sample)
	}
}
