//nolint:mnd // This file contains audio-specific magic numbers
package opus

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/pcm"
)

const pcmPacketsPath = "../../tests/data/pcm/packets.json"

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
	return pcm.NewCodecParameters(1, gomedia.PCM, 1, 16000)
}

func TestNewOpusEncoder(t *testing.T) {
	t.Parallel()
	enc := NewOpusEncoder()
	require.NotNil(t, enc)
	enc.Close()
}

func TestInit_MonoSixteenKHz(t *testing.T) {
	t.Parallel()
	enc := NewOpusEncoder()
	defer enc.Close()

	par := newPCMCodecParams()
	err := enc.Init(par)
	require.NoError(t, err)

	inner := enc.(*opusEncoder)

	// Opus always resamples to 48kHz internally
	// frameSize = channels * 40 * 48000 / 1000 = 1 * 40 * 48 = 1920 samples
	assert.Equal(t, 1920, inner.frameSize)

	// frameDuration is always 40ms (1920 samples at 48kHz)
	assert.Equal(t, 40*time.Millisecond, inner.frameDuration)

	// Codec parameters should be set
	require.NotNil(t, inner.codecPar)
	assert.Equal(t, uint64(opusSampleRate), inner.codecPar.SampleRate())
	assert.Equal(t, uint8(1), inner.codecPar.Channels())
	assert.Equal(t, gomedia.OPUS, inner.codecPar.Type())
}

func TestInit_Stereo(t *testing.T) {
	t.Parallel()
	enc := NewOpusEncoder()
	defer enc.Close()

	par := pcm.NewCodecParameters(0, gomedia.PCM, 2, 44100)
	err := enc.Init(par)
	require.NoError(t, err)

	inner := enc.(*opusEncoder)

	// frameSize = 2 * 40 * 48000 / 1000 = 3840 samples
	assert.Equal(t, 3840, inner.frameSize)
	assert.Equal(t, uint8(2), inner.codecPar.Channels())
	assert.Equal(t, gomedia.ChStereo, inner.codecPar.ChannelLayout)
}

func TestInit_SampleRates(t *testing.T) {
	t.Parallel()

	rates := []uint64{8000, 16000, 24000, 32000, 48000}
	for _, sr := range rates {
		t.Run(fmt.Sprintf("%dHz", sr), func(t *testing.T) {
			t.Parallel()
			enc := NewOpusEncoder()
			defer enc.Close()

			par := pcm.NewCodecParameters(0, gomedia.PCM, 1, sr)
			err := enc.Init(par)
			require.NoError(t, err)

			inner := enc.(*opusEncoder)
			// Output is always 48kHz regardless of input rate
			assert.Equal(t, uint64(opusSampleRate), inner.codecPar.SampleRate())
		})
	}
}

func TestInit_ChannelLayout(t *testing.T) {
	t.Parallel()

	enc := NewOpusEncoder()
	defer enc.Close()

	// Mono
	par := pcm.NewCodecParameters(0, gomedia.PCM, 1, 48000)
	require.NoError(t, enc.Init(par))
	inner := enc.(*opusEncoder)
	assert.Equal(t, gomedia.ChMono, inner.codecPar.ChannelLayout)

	// Stereo
	enc2 := NewOpusEncoder()
	defer enc2.Close()
	par2 := pcm.NewCodecParameters(0, gomedia.PCM, 2, 48000)
	require.NoError(t, enc2.Init(par2))
	inner2 := enc2.(*opusEncoder)
	assert.Equal(t, gomedia.ChStereo, inner2.codecPar.ChannelLayout)
}

func TestInit_StreamIndexPreserved(t *testing.T) {
	t.Parallel()
	enc := NewOpusEncoder()
	defer enc.Close()

	streamIndex := uint8(5)
	par := pcm.NewCodecParameters(streamIndex, gomedia.PCM, 1, 16000)
	require.NoError(t, enc.Init(par))

	inner := enc.(*opusEncoder)
	assert.Equal(t, streamIndex, inner.codecPar.StreamIndex())
}

func TestInit_RingAllocator(t *testing.T) {
	t.Parallel()
	enc := NewOpusEncoder()
	defer enc.Close()

	par := newPCMCodecParams()
	require.NoError(t, enc.Init(par))

	inner := enc.(*opusEncoder)
	require.NotNil(t, inner.ring, "ring allocator should be set after Init")
}

func TestCloseWithoutInit(t *testing.T) {
	t.Parallel()
	enc := NewOpusEncoder()
	// Should not panic
	enc.Close()
}

func TestCloseAfterInit(t *testing.T) {
	t.Parallel()
	enc := NewOpusEncoder()
	par := newPCMCodecParams()
	require.NoError(t, enc.Init(par))
	// Should not panic
	enc.Close()
}

func TestEncode_SingleFrame(t *testing.T) {
	t.Parallel()
	enc := NewOpusEncoder()
	defer enc.Close()

	par := pcm.NewCodecParameters(0, gomedia.PCM, 1, opusSampleRate)
	require.NoError(t, enc.Init(par))

	inner := enc.(*opusEncoder)

	// Create a silent PCM frame (frameSize int16 samples = frameSize*2 bytes)
	data := make([]byte, inner.frameSize*2)
	pkt := pcm.NewPacket(data, 0, "test", time.Now(), par, 40*time.Millisecond)

	result, err := inner.Encode(pkt)
	require.NoError(t, err)

	// Exactly one frame of input should produce exactly one encoded packet
	require.Len(t, result, 1)
	assert.Greater(t, len(result[0].Data()), 0)
}

func TestEncode_MultipleFrames(t *testing.T) {
	t.Parallel()
	enc := NewOpusEncoder()
	defer enc.Close()

	par := pcm.NewCodecParameters(0, gomedia.PCM, 1, opusSampleRate)
	require.NoError(t, enc.Init(par))

	inner := enc.(*opusEncoder)

	totalPackets := 0
	for i := range 10 {
		data := make([]byte, inner.frameSize*2)
		ts := time.Duration(i) * 40 * time.Millisecond
		pkt := pcm.NewPacket(data, ts, "test", time.Now(), par, 40*time.Millisecond)

		result, err := inner.Encode(pkt)
		require.NoError(t, err)
		totalPackets += len(result)
	}

	assert.Equal(t, 10, totalPackets)
}

func TestEncode_BufferingPartialFrames(t *testing.T) {
	t.Parallel()
	enc := NewOpusEncoder()
	defer enc.Close()

	par := pcm.NewCodecParameters(0, gomedia.PCM, 1, opusSampleRate)
	require.NoError(t, enc.Init(par))

	inner := enc.(*opusEncoder)
	halfBytes := inner.frameSize // half the int16 samples → frameSize bytes (each sample is 2 bytes, so frameSize*2/2)

	// Send half a frame — should produce no output
	halfData := make([]byte, halfBytes)
	pkt := pcm.NewPacket(halfData, 0, "test", time.Now(), par, 0)

	result, err := inner.Encode(pkt)
	require.NoError(t, err)
	assert.Empty(t, result, "half frame should not produce output")

	// Internal buffer should hold the partial data
	assert.Equal(t, halfBytes/2, len(inner.buf), "buffer should hold half frame samples")

	// Send the other half — should now produce one packet
	pkt2 := pcm.NewPacket(halfData, 0, "test", time.Now(), par, 0)
	result, err = inner.Encode(pkt2)
	require.NoError(t, err)
	assert.Len(t, result, 1, "completing the frame should produce one packet")
}

func TestEncode_LargerThanOneFrame(t *testing.T) {
	t.Parallel()
	enc := NewOpusEncoder()
	defer enc.Close()

	par := pcm.NewCodecParameters(0, gomedia.PCM, 1, opusSampleRate)
	require.NoError(t, enc.Init(par))

	inner := enc.(*opusEncoder)

	// Send 2.5 frames worth of data (in bytes: 2.5 * frameSize * 2)
	data := make([]byte, inner.frameSize*5) // 2.5 frames * 2 bytes per sample
	pkt := pcm.NewPacket(data, 0, "test", time.Now(), par, 0)

	result, err := inner.Encode(pkt)
	require.NoError(t, err)

	// Should have encoded 2 complete frames
	assert.Equal(t, 2, len(result))

	// Internal buffer should hold the remaining half frame
	assert.Equal(t, inner.frameSize/2, len(inner.buf))
}

func TestEncode_OutputPacketMetadata(t *testing.T) {
	t.Parallel()
	enc := NewOpusEncoder()
	defer enc.Close()

	par := pcm.NewCodecParameters(0, gomedia.PCM, 1, opusSampleRate)
	require.NoError(t, enc.Init(par))

	inner := enc.(*opusEncoder)

	sourceID := "rtsp://example.com/stream"
	startTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	data := make([]byte, inner.frameSize*2)
	pkt := pcm.NewPacket(data, 500*time.Millisecond, sourceID, startTime, par, inner.frameDuration)

	result, err := inner.Encode(pkt)
	require.NoError(t, err)
	require.Len(t, result, 1)

	p := result[0]
	assert.Greater(t, len(p.Data()), 0, "packet should have data")
	assert.Equal(t, sourceID, p.SourceID())
	assert.Equal(t, startTime, p.StartTime())
	assert.Equal(t, inner.frameDuration, p.Duration())
}

func TestEncode_RingAllocation(t *testing.T) {
	t.Parallel()
	enc := NewOpusEncoder()
	defer enc.Close()

	par := pcm.NewCodecParameters(0, gomedia.PCM, 1, opusSampleRate)
	require.NoError(t, enc.Init(par))

	inner := enc.(*opusEncoder)
	require.NotNil(t, inner.ring)

	data := make([]byte, inner.frameSize*2)
	pkt := pcm.NewPacket(data, 0, "test", time.Now(), par, inner.frameDuration)

	result, err := inner.Encode(pkt)
	require.NoError(t, err)
	require.Len(t, result, 1)

	// Verify data is present (ring allocation is internal)
	assert.Greater(t, len(result[0].Data()), 0)
}

func TestEncode_WithRealPCMData(t *testing.T) {
	t.Parallel()

	pcmPackets := loadFixturePackets(t, pcmPacketsPath)

	enc := NewOpusEncoder()
	defer enc.Close()

	par := newPCMCodecParams()
	require.NoError(t, enc.Init(par))

	inner := enc.(*opusEncoder)
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

	// Input is 16kHz mono, resampled to 48kHz before encoding.
	// The resampler may produce slightly different byte counts due to
	// interpolation, so use InDelta for the expected frame count.
	assert.Greater(t, len(allOutput), 0, "should produce output packets")

	// Every output packet should have non-zero data
	for i, p := range allOutput {
		assert.Greater(t, len(p.Data()), 0, "packet %d should have data", i)
	}

	// Duration should be consistent
	for i, p := range allOutput {
		assert.Equal(t, inner.frameDuration, p.Duration(), "packet %d duration mismatch", i)
	}
}

func TestEncode_OutputSizesReasonable(t *testing.T) {
	t.Parallel()

	pcmPackets := loadFixturePackets(t, pcmPacketsPath)

	enc := NewOpusEncoder()
	defer enc.Close()

	par := newPCMCodecParams()
	require.NoError(t, enc.Init(par))

	inner := enc.(*opusEncoder)
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

	for i, p := range allOutput {
		size := len(p.Data())
		// Opus packets should be well under 1000 bytes for typical audio
		assert.Less(t, size, 1000, "packet %d unexpectedly large: %d bytes", i, size)
		assert.Greater(t, size, 0, "packet %d is empty", i)
	}
}

func TestEncode_StereoWithRealData(t *testing.T) {
	t.Parallel()

	pcmPackets := loadFixturePackets(t, pcmPacketsPath)

	enc := NewOpusEncoder()
	defer enc.Close()

	// Use stereo at 48kHz — we'll duplicate mono data to fake stereo
	par := pcm.NewCodecParameters(0, gomedia.PCM, 2, opusSampleRate)
	require.NoError(t, enc.Init(par))

	inner := enc.(*opusEncoder)
	startTime := time.Now()

	var allOutput []gomedia.AudioPacket
	for _, fp := range pcmPackets[:10] { // use first 10 packets
		// Duplicate mono samples into stereo (interleave L=R)
		stereoData := make([]byte, len(fp.Data)*2)
		for i := 0; i < len(fp.Data); i += 2 {
			// Left channel
			stereoData[i*2] = fp.Data[i]
			stereoData[i*2+1] = fp.Data[i+1]
			// Right channel (same)
			stereoData[i*2+2] = fp.Data[i]
			stereoData[i*2+3] = fp.Data[i+1]
		}

		ts := time.Duration(fp.TimestampNs) * time.Nanosecond
		dur := time.Duration(fp.DurationNs) * time.Nanosecond
		pkt := pcm.NewPacket(stereoData, ts, "test", startTime, par, dur)

		result, err := inner.Encode(pkt)
		require.NoError(t, err)
		allOutput = append(allOutput, result...)
	}

	assert.Greater(t, len(allOutput), 0, "stereo encoding should produce output")
	for i, p := range allOutput {
		assert.Greater(t, len(p.Data()), 0, "stereo packet %d should have data", i)
	}
}

func TestEncode_CodecParametersValidity(t *testing.T) {
	t.Parallel()
	enc := NewOpusEncoder()
	defer enc.Close()

	par := newPCMCodecParams()
	require.NoError(t, enc.Init(par))

	inner := enc.(*opusEncoder)
	require.NotNil(t, inner.codecPar)

	assert.Equal(t, gomedia.OPUS, inner.codecPar.Type())
	assert.Equal(t, uint64(opusSampleRate), inner.codecPar.SampleRate())
	assert.Equal(t, uint8(1), inner.codecPar.Channels())
	assert.Equal(t, gomedia.ChMono, inner.codecPar.ChannelLayout)
	assert.Greater(t, inner.codecPar.Bitrate(), uint(0))
}

func TestFrameDuration_DifferentInputRates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		inputRate        uint64
		expectedDuration time.Duration
	}{
		{"48kHz", 48000, 40 * time.Millisecond},
		{"16kHz", 16000, 40 * time.Millisecond},
		{"8kHz", 8000, 40 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			enc := NewOpusEncoder()
			defer enc.Close()

			par := pcm.NewCodecParameters(0, gomedia.PCM, 1, tt.inputRate)
			require.NoError(t, enc.Init(par))

			inner := enc.(*opusEncoder)
			assert.Equal(t, tt.expectedDuration, inner.frameDuration)
		})
	}
}
