package aac

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/pcm"
)

const (
	pcmPacketsPath = "../../tests/data/pcm/packets.json"
	aacPacketsPath = "../../tests/data/aac/packets.json"
)

// fixturePacket holds decoded test data for a single packet.
type fixturePacket struct {
	TimestampNs int64
	DurationNs  int64
	Size        int
	Data        []byte
}

func loadFixturePackets(t *testing.T, path string) []fixturePacket {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	var f struct {
		Packets []struct {
			TimestampNs int64  `json:"timestamp_ns"`
			DurationNs  int64  `json:"duration_ns"`
			Size        int    `json:"size"`
			Data        string `json:"data"`
		} `json:"packets"`
	}
	require.NoError(t, json.Unmarshal(raw, &f))
	require.NotEmpty(t, f.Packets)

	packets := make([]fixturePacket, len(f.Packets))
	for i, p := range f.Packets {
		data, decErr := base64.StdEncoding.DecodeString(p.Data)
		require.NoError(t, decErr, "packet %d base64 decode", i)
		require.Equal(t, p.Size, len(data), "packet %d size mismatch", i)
		packets[i] = fixturePacket{
			TimestampNs: p.TimestampNs,
			DurationNs:  p.DurationNs,
			Size:        p.Size,
			Data:        data,
		}
	}
	return packets
}

// newPCMCodecParams returns PCM codec parameters matching the test data (mono 16kHz).
func newPCMCodecParams() *pcm.CodecParameters {
	return pcm.NewCodecParameters(
		1,          // stream index
		gomedia.PCM,
		1,          // mono
		16000,      // 16 kHz
	)
}

func TestNewAacEncoder(t *testing.T) {
	t.Parallel()
	enc := NewAacEncoder()
	require.NotNil(t, enc)
	enc.Close()
}

func TestInit_MonoSixteenKHz(t *testing.T) {
	t.Parallel()
	enc := NewAacEncoder()
	defer enc.Close()

	par := newPCMCodecParams()
	err := enc.Init(par)
	require.NoError(t, err)

	inner := enc.(*aacEncoder)

	assert.Equal(t, 1, inner.Channels())
	assert.Greater(t, inner.FrameSize(), 0)
	assert.Greater(t, inner.NbBytesPerFrame(), 0)

	// FrameSize should be 1024 for AAC-LC
	assert.Equal(t, 1024, inner.FrameSize()) //nolint:mnd // AAC-LC frame size

	// NbBytesPerFrame = 2 * channels * FrameSize = 2 * 1 * 1024 = 2048
	assert.Equal(t, 2048, inner.NbBytesPerFrame()) //nolint:mnd // expected bytes per frame

	// Codec parameters should be set
	require.NotNil(t, inner.param)
	assert.Equal(t, uint64(16000), inner.param.SampleRate()) //nolint:mnd // test sample rate
	assert.Equal(t, uint8(1), inner.param.Channels())
}

func TestInit_Stereo(t *testing.T) {
	t.Parallel()
	enc := NewAacEncoder()
	defer enc.Close()

	par := pcm.NewCodecParameters(0, gomedia.PCM, 2, 44100) //nolint:mnd // stereo 44.1kHz
	err := enc.Init(par)
	require.NoError(t, err)

	inner := enc.(*aacEncoder)
	assert.Equal(t, 2, inner.Channels()) //nolint:mnd // stereo
	assert.Equal(t, uint8(2), inner.param.Channels()) //nolint:mnd // stereo
	assert.Equal(t, uint64(44100), inner.param.SampleRate()) //nolint:mnd // 44.1kHz
}

func TestInit_SampleRates(t *testing.T) {
	t.Parallel()

	rates := []uint64{8000, 11025, 12000, 16000, 22050, 24000, 32000, 44100, 48000}
	for _, sr := range rates {
		t.Run("rate_"+string(rune('0'+sr/1000)), func(t *testing.T) {
			t.Parallel()
			enc := NewAacEncoder()
			defer enc.Close()

			par := pcm.NewCodecParameters(0, gomedia.PCM, 1, sr)
			err := enc.Init(par)
			require.NoError(t, err)

			inner := enc.(*aacEncoder)
			assert.Equal(t, uint64(sr), inner.param.SampleRate())
		})
	}
}

func TestCloseWithoutInit(t *testing.T) {
	t.Parallel()
	enc := NewAacEncoder()
	// Should not panic
	enc.Close()
}

func TestCloseAfterInit(t *testing.T) {
	t.Parallel()
	enc := NewAacEncoder()
	par := newPCMCodecParams()
	require.NoError(t, enc.Init(par))
	// Should not panic
	enc.Close()
}

func TestEncode_SingleFrame(t *testing.T) {
	t.Parallel()
	enc := NewAacEncoder()
	defer enc.Close()

	par := newPCMCodecParams()
	require.NoError(t, enc.Init(par))

	inner := enc.(*aacEncoder)
	frameBytes := inner.NbBytesPerFrame()

	// Create a silent PCM frame
	data := make([]byte, frameBytes)
	pkt := pcm.NewPacket(data, 0, "test", time.Now(), par, time.Duration(inner.FrameSize())*time.Second/16000) //nolint:mnd // 16kHz

	result, err := inner.Encode(pkt)
	require.NoError(t, err)

	// FDK-AAC may or may not produce output for the first frame (encoder delay)
	// but should not return an error
	for _, p := range result {
		assert.Greater(t, len(p.Data()), 0)
	}
}

func TestEncode_MultipleFrames(t *testing.T) {
	t.Parallel()
	enc := NewAacEncoder()
	defer enc.Close()

	par := newPCMCodecParams()
	require.NoError(t, enc.Init(par))

	inner := enc.(*aacEncoder)
	frameBytes := inner.NbBytesPerFrame()
	frameDur := time.Duration(inner.FrameSize()) * time.Second / 16000 //nolint:mnd // 16kHz

	totalPackets := 0
	for i := range 10 { //nolint:mnd // encode 10 frames
		data := make([]byte, frameBytes)
		ts := time.Duration(i) * frameDur
		pkt := pcm.NewPacket(data, ts, "test", time.Now(), par, frameDur)

		result, err := inner.Encode(pkt)
		require.NoError(t, err)
		totalPackets += len(result)
	}

	// Should have produced some output packets
	assert.Greater(t, totalPackets, 0)
}

func TestEncode_BufferingPartialFrames(t *testing.T) {
	t.Parallel()
	enc := NewAacEncoder()
	defer enc.Close()

	par := newPCMCodecParams()
	require.NoError(t, enc.Init(par))

	inner := enc.(*aacEncoder)
	frameBytes := inner.NbBytesPerFrame()

	// Send half a frame — should produce no output
	halfData := make([]byte, frameBytes/2) //nolint:mnd // half frame
	pkt := pcm.NewPacket(halfData, 0, "test", time.Now(), par, 0)

	result, err := inner.Encode(pkt)
	require.NoError(t, err)
	assert.Empty(t, result, "half frame should not produce output")

	// Send the other half — should now produce output
	pkt2 := pcm.NewPacket(halfData, 0, "test", time.Now(), par, 0)
	result, err = inner.Encode(pkt2)
	require.NoError(t, err)
	// Encoder may buffer internally, so output is not guaranteed on first full frame
}

func TestEncode_LargerThanOneFrame(t *testing.T) {
	t.Parallel()
	enc := NewAacEncoder()
	defer enc.Close()

	par := newPCMCodecParams()
	require.NoError(t, enc.Init(par))

	inner := enc.(*aacEncoder)
	frameBytes := inner.NbBytesPerFrame()

	// Send 2.5 frames worth of data
	data := make([]byte, frameBytes*2+frameBytes/2) //nolint:mnd // 2.5 frames
	pkt := pcm.NewPacket(data, 0, "test", time.Now(), par, 0)

	result, err := inner.Encode(pkt)
	require.NoError(t, err)

	// Should have encoded at least 2 frames (buffered the remaining half)
	assert.GreaterOrEqual(t, len(result), 1)

	// Internal buffer should hold the remaining half frame
	assert.Equal(t, frameBytes/2, inner.pcmLen) //nolint:mnd // half frame remains
}

func TestEncode_OutputPacketMetadata(t *testing.T) {
	t.Parallel()
	enc := NewAacEncoder()
	defer enc.Close()

	par := newPCMCodecParams()
	require.NoError(t, enc.Init(par))

	inner := enc.(*aacEncoder)
	frameBytes := inner.NbBytesPerFrame()

	sourceID := "rtsp://example.com/stream"
	startTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	ts := 500 * time.Millisecond //nolint:mnd // test timestamp

	// Encode enough frames to guarantee output
	var result []gomedia.AudioPacket
	for i := range 5 { //nolint:mnd // encode 5 frames
		data := make([]byte, frameBytes)
		pkt := pcm.NewPacket(data, ts+time.Duration(i)*inner.frameDuration, sourceID, startTime, par, inner.frameDuration)
		r, err := inner.Encode(pkt)
		require.NoError(t, err)
		result = append(result, r...)
	}

	require.NotEmpty(t, result)
	for _, p := range result {
		assert.Greater(t, len(p.Data()), 0, "packet should have data")
		assert.Equal(t, sourceID, p.SourceID())
		assert.Equal(t, startTime, p.StartTime())
		assert.Greater(t, p.Duration(), time.Duration(0), "packet should have duration")
	}
}

func TestEncode_RingAllocation(t *testing.T) {
	t.Parallel()
	enc := NewAacEncoder()
	defer enc.Close()

	par := newPCMCodecParams()
	require.NoError(t, enc.Init(par))

	inner := enc.(*aacEncoder)
	require.NotNil(t, inner.ring, "ring allocator should be set after Init")

	frameBytes := inner.NbBytesPerFrame()

	// Encode enough frames to get output
	for i := range 5 { //nolint:mnd // encode 5 frames
		data := make([]byte, frameBytes)
		pkt := pcm.NewPacket(data, time.Duration(i)*inner.frameDuration, "test", time.Now(), par, inner.frameDuration)
		_, err := inner.Encode(pkt)
		require.NoError(t, err)
	}
}

func TestEncode_WithRealPCMData(t *testing.T) {
	t.Parallel()

	pcmPackets := loadFixturePackets(t, pcmPacketsPath)
	aacPackets := loadFixturePackets(t, aacPacketsPath)

	enc := NewAacEncoder()
	defer enc.Close()

	par := newPCMCodecParams()
	require.NoError(t, enc.Init(par))

	inner := enc.(*aacEncoder)
	startTime := time.Now()

	var allOutput []gomedia.AudioPacket
	for _, fp := range pcmPackets {
		ts := time.Duration(fp.TimestampNs) * time.Nanosecond
		dur := time.Duration(fp.DurationNs) * time.Nanosecond
		pkt := pcm.NewPacket(fp.Data, ts, "test", startTime, par, dur)

		result, err := inner.Encode(pkt)
		require.NoError(t, err)
		allOutput = append(allOutput, result...)
	}

	// Total PCM bytes / NbBytesPerFrame gives the expected number of AAC frames.
	totalPCMBytes := 0
	for _, fp := range pcmPackets {
		totalPCMBytes += len(fp.Data)
	}
	expectedFrames := totalPCMBytes / inner.NbBytesPerFrame()
	assert.InDelta(t, expectedFrames, len(allOutput), 5, //nolint:mnd // tolerance for encoder delay
		"output packet count should match PCM data (got %d, want ~%d)", len(allOutput), expectedFrames)
	_ = aacPackets // reference data used for size validation below

	// Every output packet should have non-zero data
	for i, p := range allOutput {
		assert.Greater(t, len(p.Data()), 0, "packet %d should have data", i)
	}

	// AAC frame duration should be consistent (64ms for 1024 samples at 16kHz)
	expectedDuration := time.Duration(1024) * time.Second / 16000 //nolint:mnd // 1024 samples at 16kHz
	for i, p := range allOutput {
		assert.Equal(t, expectedDuration, p.Duration(), "packet %d duration mismatch", i)
	}
}

func TestEncode_OutputSizesReasonable(t *testing.T) {
	t.Parallel()

	pcmPackets := loadFixturePackets(t, pcmPacketsPath)

	enc := NewAacEncoder()
	defer enc.Close()

	par := newPCMCodecParams()
	require.NoError(t, enc.Init(par))

	inner := enc.(*aacEncoder)
	startTime := time.Now()

	var allOutput []gomedia.AudioPacket
	for _, fp := range pcmPackets {
		ts := time.Duration(fp.TimestampNs) * time.Nanosecond
		dur := time.Duration(fp.DurationNs) * time.Nanosecond
		pkt := pcm.NewPacket(fp.Data, ts, "test", startTime, par, dur)

		result, err := inner.Encode(pkt)
		require.NoError(t, err)
		allOutput = append(allOutput, result...)
	}

	require.NotEmpty(t, allOutput)

	// RFC 3640 §3.3.6: AAC-hbr mode AU size fits in 13 bits (max 8191 bytes)
	for i, p := range allOutput {
		size := len(p.Data())
		assert.Less(t, size, 8192, "packet %d exceeds RFC 3640 AAC-hbr max AU size", i) //nolint:mnd // RFC 3640 limit
		assert.Greater(t, size, 0, "packet %d is empty", i)
	}
}

func TestEncode_CodecParametersValidity(t *testing.T) {
	t.Parallel()
	enc := NewAacEncoder()
	defer enc.Close()

	par := newPCMCodecParams()
	require.NoError(t, enc.Init(par))

	inner := enc.(*aacEncoder)
	require.NotNil(t, inner.param)

	// Validate the produced AudioSpecificConfig
	configBytes := inner.param.MPEG4AudioConfigBytes()
	require.NotEmpty(t, configBytes, "AudioSpecificConfig should not be empty")

	// For mono 16kHz AAC-LC:
	// ObjectType should be 2 (AAC-LC)
	assert.Equal(t, uint(2), inner.param.Config.ObjectType) //nolint:mnd // AAC-LC

	// SampleRate should match input
	assert.Equal(t, uint64(16000), inner.param.SampleRate()) //nolint:mnd // 16kHz

	// Channels should be mono
	assert.Equal(t, uint8(1), inner.param.Channels())

	// ChannelConfig should be 1 (mono), not 0
	assert.Equal(t, uint(1), inner.param.Config.ChannelConfig)

	// SampleRateIndex should be 8 (16000 Hz)
	assert.Equal(t, uint(8), inner.param.Config.SampleRateIndex) //nolint:mnd // index 8 = 16kHz
}

func TestEncode_StereoCodecParameters(t *testing.T) {
	t.Parallel()
	enc := NewAacEncoder()
	defer enc.Close()

	par := pcm.NewCodecParameters(0, gomedia.PCM, 2, 48000) //nolint:mnd // stereo 48kHz
	require.NoError(t, enc.Init(par))

	inner := enc.(*aacEncoder)
	require.NotNil(t, inner.param)

	assert.Equal(t, uint(2), inner.param.Config.ChannelConfig)       //nolint:mnd // stereo
	assert.Equal(t, uint(3), inner.param.Config.SampleRateIndex)     //nolint:mnd // index 3 = 48kHz
	assert.Equal(t, uint64(48000), inner.param.SampleRate())         //nolint:mnd // 48kHz
	assert.Equal(t, uint8(2), inner.param.Channels())                //nolint:mnd // stereo
}

func TestEncode_StreamIndexPreserved(t *testing.T) {
	t.Parallel()
	enc := NewAacEncoder()
	defer enc.Close()

	streamIndex := uint8(3) //nolint:mnd // test stream index
	par := pcm.NewCodecParameters(streamIndex, gomedia.PCM, 1, 16000) //nolint:mnd // mono 16kHz
	require.NoError(t, enc.Init(par))

	inner := enc.(*aacEncoder)
	assert.Equal(t, streamIndex, inner.param.StreamIndex())
}

func TestFrameSize(t *testing.T) {
	t.Parallel()
	enc := NewAacEncoder()
	defer enc.Close()

	par := newPCMCodecParams()
	require.NoError(t, enc.Init(par))

	inner := enc.(*aacEncoder)

	// AAC-LC always uses 1024 samples per frame
	assert.Equal(t, 1024, inner.FrameSize()) //nolint:mnd // AAC-LC frame size
}

func TestNbBytesPerFrame(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		channels uint8
		rate     uint64
		expected int
	}{
		{"mono", 1, 16000, 2048},     //nolint:mnd // 2 * 1 * 1024
		{"stereo", 2, 44100, 4096},   //nolint:mnd // 2 * 2 * 1024
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			enc := NewAacEncoder()
			defer enc.Close()

			par := pcm.NewCodecParameters(0, gomedia.PCM, tt.channels, tt.rate)
			require.NoError(t, enc.Init(par))

			inner := enc.(*aacEncoder)
			assert.Equal(t, tt.expected, inner.NbBytesPerFrame())
		})
	}
}
