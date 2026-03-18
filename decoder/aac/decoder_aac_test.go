package aac_test

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/ugparu/gomedia"
	codecaac "github.com/ugparu/gomedia/codec/aac"
	decaac "github.com/ugparu/gomedia/decoder/aac"
	"github.com/ugparu/gomedia/utils/buffer"
)

const testDataDir = "../../tests/data/aac/"

// ---------------------------------------------------------------------------
// Test data helpers
// ---------------------------------------------------------------------------

type testParametersJSON struct {
	Codec       string `json:"codec"`
	StreamIndex uint8  `json:"stream_index"`
	Config      string `json:"config"` // base64-encoded MPEG4AudioConfig bytes
}

type testPacketJSON struct {
	Codec       string `json:"codec"`
	StreamIndex uint8  `json:"stream_index"`
	TimestampNs int64  `json:"timestamp_ns"`
	DurationNs  int64  `json:"duration_ns"`
	Size        int    `json:"size"`
	Data        string `json:"data"` // base64-encoded raw AAC payload
}

type testPacketsJSON struct {
	Packets []testPacketJSON `json:"packets"`
}

func loadCodecParameters(t *testing.T) *codecaac.CodecParameters {
	t.Helper()
	raw, err := os.ReadFile(testDataDir + "parameters.json")
	require.NoError(t, err)

	var params testParametersJSON
	require.NoError(t, json.Unmarshal(raw, &params))

	configBytes, err := base64.StdEncoding.DecodeString(params.Config)
	require.NoError(t, err)

	cp, err := codecaac.NewCodecDataFromMPEG4AudioConfigBytes(configBytes)
	require.NoError(t, err)
	cp.SetStreamIndex(params.StreamIndex)
	return &cp
}

func loadPacketFrames(t *testing.T) [][]byte {
	t.Helper()
	raw, err := os.ReadFile(testDataDir + "packets.json")
	require.NoError(t, err)

	var pkts testPacketsJSON
	require.NoError(t, json.Unmarshal(raw, &pkts))
	require.NotEmpty(t, pkts.Packets)

	frames := make([][]byte, len(pkts.Packets))
	for i, p := range pkts.Packets {
		data, decErr := base64.StdEncoding.DecodeString(p.Data)
		require.NoError(t, decErr, "packet %d base64 decode", i)
		require.Equal(t, p.Size, len(data), "packet %d declared size mismatch", i)
		frames[i] = data
	}
	return frames
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

func TestNewAacDecoder(t *testing.T) {
	t.Parallel()
	d := decaac.NewAacDecoder()
	require.NotNil(t, d)
}

// ---------------------------------------------------------------------------
// Init — error paths
// ---------------------------------------------------------------------------

func TestInit_NilParam(t *testing.T) {
	t.Parallel()
	d := decaac.NewAacDecoder()
	// A nil interface value fails the *aac.CodecParameters type assertion.
	var nilParam gomedia.AudioCodecParameters
	err := d.Init(nilParam)
	require.Error(t, err)
	require.Contains(t, err.Error(), "expected *aac.CodecParameters")
}

func TestInit_EmptyConfigBytes(t *testing.T) {
	t.Parallel()
	d := decaac.NewAacDecoder()
	// A zero-value CodecParameters has nil ConfigBytes — our new guard rejects it.
	cp := &codecaac.CodecParameters{}
	err := d.Init(cp)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty AudioSpecificConfig")
}

// ---------------------------------------------------------------------------
// Init — success paths
// ---------------------------------------------------------------------------

func TestInit_ValidConfig(t *testing.T) {
	t.Parallel()
	cp := loadCodecParameters(t)
	d := decaac.NewAacDecoder()
	require.NoError(t, d.Init(cp))
	d.Close()
}

func TestInit_Reinit(t *testing.T) {
	t.Parallel()
	// Calling Init twice must not leak or crash (fixed: aacdec_close before reinit).
	cp := loadCodecParameters(t)
	d := decaac.NewAacDecoder()
	require.NoError(t, d.Init(cp))
	require.NoError(t, d.Init(cp))
	d.Close()
}

func TestInit_ReinitWithDifferentConfig(t *testing.T) {
	t.Parallel()
	cp1 := loadCodecParameters(t)
	d := decaac.NewAacDecoder()
	require.NoError(t, d.Init(cp1))

	// Build a second valid config (44100 Hz stereo AAC-LC).
	config := codecaac.MPEG4AudioConfig{
		ObjectType:      codecaac.AotAacLc,
		SampleRateIndex: 4, // 44100 Hz
		ChannelConfig:   2, // stereo
	}
	config.Complete()
	cp2, err := codecaac.NewCodecDataFromMPEG4AudioConfig(config)
	require.NoError(t, err)

	require.NoError(t, d.Init(&cp2))
	d.Close()
}

// ---------------------------------------------------------------------------
// Close
// ---------------------------------------------------------------------------

func TestClose_WithoutInit(t *testing.T) {
	t.Parallel()
	// aacdec_close checks h->dec != NULL before calling aacDecoder_Close.
	d := decaac.NewAacDecoder()
	require.NotPanics(t, d.Close)
}

func TestClose_AfterInit(t *testing.T) {
	t.Parallel()
	cp := loadCodecParameters(t)
	d := decaac.NewAacDecoder()
	require.NoError(t, d.Init(cp))
	require.NotPanics(t, d.Close)
}

func TestClose_Double(t *testing.T) {
	t.Parallel()
	// The second Close must be a no-op since dec is NULL after the first.
	cp := loadCodecParameters(t)
	d := decaac.NewAacDecoder()
	require.NoError(t, d.Init(cp))
	d.Close()
	require.NotPanics(t, d.Close)
}

// ---------------------------------------------------------------------------
// Decode — empty / nil input (guards added in this session)
// ---------------------------------------------------------------------------

func TestDecode_NilInput(t *testing.T) {
	t.Parallel()
	// len(nil) == 0 triggers the early return added for Bug 2.
	cp := loadCodecParameters(t)
	d := decaac.NewAacDecoder()
	require.NoError(t, d.Init(cp))
	defer d.Close()

	pcm, _, err := d.Decode(nil, nil)
	require.NoError(t, err)
	require.Nil(t, pcm)
}

func TestDecode_EmptyInput(t *testing.T) {
	t.Parallel()
	cp := loadCodecParameters(t)
	d := decaac.NewAacDecoder()
	require.NoError(t, d.Init(cp))
	defer d.Close()

	pcm, _, err := d.Decode([]byte{}, nil)
	require.NoError(t, err)
	require.Nil(t, pcm)
}

// TestDecode_EmptyInputBeforeInit verifies the nil guard fires before any CGO
// call, so there is no NULL-decoder dereference.
func TestDecode_EmptyInputBeforeInit(t *testing.T) {
	t.Parallel()
	d := decaac.NewAacDecoder()
	// No Init called — decoder handle is NULL. The empty-input guard must fire
	// before aacDecoder_Fill is reached.
	pcm, _, err := d.Decode([]byte{}, nil)
	require.NoError(t, err)
	require.Nil(t, pcm)
}

// ---------------------------------------------------------------------------
// Decode — actual AAC frames from test data
// ---------------------------------------------------------------------------

// TestDecode_FirstFrame feeds the first real AAC AU and expects either valid
// PCM or a "not enough bits" silent skip (both are correct outcomes).
func TestDecode_FirstFrame(t *testing.T) {
	t.Parallel()
	cp := loadCodecParameters(t)
	frames := loadPacketFrames(t)

	d := decaac.NewAacDecoder()
	require.NoError(t, d.Init(cp))
	defer d.Close()

	pcm, _, err := d.Decode(frames[0], nil)
	require.NoError(t, err)
	// Either PCM bytes were returned or nil (NOT_ENOUGH_BITS on first call).
	if pcm != nil {
		require.Greater(t, len(pcm), 0)
	}
}

// TestDecode_ProducesPCM decodes multiple sequential frames and verifies that
// at least one produces non-empty PCM output.
func TestDecode_ProducesPCM(t *testing.T) {
	t.Parallel()
	cp := loadCodecParameters(t)
	frames := loadPacketFrames(t)

	d := decaac.NewAacDecoder()
	require.NoError(t, d.Init(cp))
	defer d.Close()

	limit := min(10, len(frames))
	var gotPCM bool
	for i := range limit {
		pcm, _, err := d.Decode(frames[i], nil)
		require.NoError(t, err, "frame %d", i)
		if len(pcm) > 0 {
			gotPCM = true
		}
	}
	require.True(t, gotPCM, "expected at least one frame to produce PCM output")
}

// TestDecode_PCMSize verifies that every non-empty PCM output has the correct
// byte count for the stream's parameters.
// Test data: AAC-LC, 16 kHz, mono → 1024 samples × 1 ch × 2 bytes = 2048 bytes.
func TestDecode_PCMSize(t *testing.T) {
	t.Parallel()
	cp := loadCodecParameters(t)
	frames := loadPacketFrames(t)

	d := decaac.NewAacDecoder()
	require.NoError(t, d.Init(cp))
	defer d.Close()

	const (
		samplesPerFrame = 1024 // AAC-LC
		bytesPerSample  = 2   // FDK-AAC always outputs 16-bit PCM
	)
	channels := int(cp.Channels()) // 1 (mono)
	expectedPCMBytes := samplesPerFrame * channels * bytesPerSample

	limit := min(20, len(frames))
	for i := range limit {
		pcm, _, err := d.Decode(frames[i], nil)
		require.NoError(t, err, "frame %d", i)
		if len(pcm) > 0 {
			require.Equal(t, expectedPCMBytes, len(pcm),
				"frame %d: PCM size mismatch (want %d bytes = %d samples × %d ch × %d bytes/sample)",
				i, expectedPCMBytes, samplesPerFrame, channels, bytesPerSample)
		}
	}
}

// TestDecode_AllFrames decodes the full packet set and checks that no frame
// returns an unexpected error.
func TestDecode_AllFrames(t *testing.T) {
	t.Parallel()
	cp := loadCodecParameters(t)
	frames := loadPacketFrames(t)

	d := decaac.NewAacDecoder()
	require.NoError(t, d.Init(cp))
	defer d.Close()

	for i, frame := range frames {
		_, _, err := d.Decode(frame, nil)
		require.NoError(t, err, "frame %d returned unexpected error", i)
	}
}

// TestDecode_TruncatedFrame feeds only the first 4 bytes of a real AU — far
// too short for a complete AAC frame. The decoder must return nil, nil (the
// NOT_ENOUGH_BITS path) rather than crash or return a pipeline-stopping error.
func TestDecode_TruncatedFrame(t *testing.T) {
	t.Parallel()
	cp := loadCodecParameters(t)
	frames := loadPacketFrames(t)
	require.Greater(t, len(frames[0]), 4)

	d := decaac.NewAacDecoder()
	require.NoError(t, d.Init(cp))
	defer d.Close()

	truncated := frames[0][:4]
	pcm, _, err := d.Decode(truncated, nil)
	// NOT_ENOUGH_BITS → nil, nil  OR  parse error → nil, error.
	// Either way the decoder must not crash.
	if err != nil {
		require.Contains(t, err.Error(), "decode aac frame failed")
	} else {
		require.Nil(t, pcm)
	}
}

// ---------------------------------------------------------------------------
// Decode — ring allocator path
// ---------------------------------------------------------------------------

// TestDecode_WithRingAlloc decodes several real AAC frames using a GrowingRingAlloc
// and verifies that when PCM is returned the ring slot is non-nil and data is
// non-empty. Every returned slot is released to avoid leaks.
func TestDecode_WithRingAlloc(t *testing.T) {
	t.Parallel()
	cp := loadCodecParameters(t)
	frames := loadPacketFrames(t)

	d := decaac.NewAacDecoder()
	require.NoError(t, d.Init(cp))
	defer d.Close()

	ring := buffer.NewGrowingRingAlloc(256 * 1024)

	limit := min(10, len(frames))
	var gotPCM bool
	for i := range limit {
		pcm, slot, err := d.Decode(frames[i], ring)
		require.NoError(t, err, "frame %d", i)
		if len(pcm) > 0 {
			gotPCM = true
			require.NotNil(t, slot, "frame %d: expected non-nil slot when PCM is returned via ring", i)
			slot.Release()
		}
	}
	require.True(t, gotPCM, "expected at least one frame to produce PCM output via ring")
}

// TestDecode_WithFullRing decodes frames with a very small initial ring (1 byte).
// GrowingRingAlloc will expand to accommodate the allocation, so the decoder
// still produces PCM. Any non-nil slots are released to avoid leaks.
func TestDecode_WithFullRing(t *testing.T) {
	t.Parallel()
	cp := loadCodecParameters(t)
	frames := loadPacketFrames(t)

	d := decaac.NewAacDecoder()
	require.NoError(t, d.Init(cp))
	defer d.Close()

	// A 1-byte initial ring — GrowingRingAlloc will grow on first allocation.
	// The decoder must still produce PCM regardless of ring behaviour.
	ring := buffer.NewGrowingRingAlloc(1)

	limit := min(10, len(frames))
	var gotPCM bool
	for i := range limit {
		pcm, slot, err := d.Decode(frames[i], ring)
		require.NoError(t, err, "frame %d", i)
		if slot != nil {
			slot.Release()
		}
		if len(pcm) > 0 {
			gotPCM = true
		}
	}
	require.True(t, gotPCM, "expected at least one frame to produce PCM output even with small initial ring")
}
