//nolint:mnd // This file contains audio-specific magic numbers
package pcm

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

func TestNewAlawEncoder(t *testing.T) {
	t.Parallel()
	enc := NewAlawEncoder()
	require.NotNil(t, enc)
	enc.Close()
}

func TestInit_MonoSixteenKHz(t *testing.T) {
	t.Parallel()
	enc := NewAlawEncoder()
	defer enc.Close()

	par := newPCMCodecParams()
	err := enc.Init(par)
	require.NoError(t, err)

	inner := enc.(*alawEncoder)

	// inpFrameSize for mono = AlawFrameSize * 1 = 1600
	assert.Equal(t, AlawFrameSize, inner.inpFrameSize)

	// frameDuration = 800 samples / 8000 Hz = 100ms
	assert.Equal(t, 100*time.Millisecond, inner.frameDuration)

	// Codec parameters should be set to A-law mono 8kHz
	require.NotNil(t, inner.codecPar)
	assert.Equal(t, uint64(ALAWSampleRate), inner.codecPar.SampleRate())
	assert.Equal(t, uint8(1), inner.codecPar.Channels())
	assert.Equal(t, gomedia.PCMAlaw, inner.codecPar.Type())
}

func TestInit_Stereo(t *testing.T) {
	t.Parallel()
	enc := NewAlawEncoder()
	defer enc.Close()

	par := pcm.NewCodecParameters(0, gomedia.PCM, 2, 44100)
	err := enc.Init(par)
	require.NoError(t, err)

	inner := enc.(*alawEncoder)

	// inpFrameSize for stereo = AlawFrameSize * 2 = 3200
	assert.Equal(t, AlawFrameSize*2, inner.inpFrameSize)
	assert.Equal(t, uint8(2), inner.inpChannels)

	// Output is still mono A-law
	assert.Equal(t, uint8(1), inner.codecPar.Channels())
}

func TestInit_SampleRates(t *testing.T) {
	t.Parallel()

	rates := []uint64{8000, 16000, 32000, 44100, 48000}
	for _, sr := range rates {
		t.Run("rate", func(t *testing.T) {
			t.Parallel()
			enc := NewAlawEncoder()
			defer enc.Close()

			par := pcm.NewCodecParameters(0, gomedia.PCM, 1, sr)
			err := enc.Init(par)
			require.NoError(t, err)

			inner := enc.(*alawEncoder)
			// Output is always 8kHz A-law regardless of input rate
			assert.Equal(t, uint64(ALAWSampleRate), inner.codecPar.SampleRate())
		})
	}
}

func TestInit_StreamIndexPreserved(t *testing.T) {
	t.Parallel()
	enc := NewAlawEncoder()
	defer enc.Close()

	streamIndex := uint8(5)
	par := pcm.NewCodecParameters(streamIndex, gomedia.PCM, 1, 16000)
	require.NoError(t, enc.Init(par))

	inner := enc.(*alawEncoder)
	assert.Equal(t, streamIndex, inner.codecPar.StreamIndex())
}

func TestInit_RingAllocator(t *testing.T) {
	t.Parallel()
	enc := NewAlawEncoder()
	defer enc.Close()

	par := newPCMCodecParams()
	require.NoError(t, enc.Init(par))

	inner := enc.(*alawEncoder)
	require.NotNil(t, inner.ring, "ring allocator should be set after Init")
}

func TestCloseWithoutInit(t *testing.T) {
	t.Parallel()
	enc := NewAlawEncoder()
	// Should not panic
	enc.Close()
}

func TestCloseAfterInit(t *testing.T) {
	t.Parallel()
	enc := NewAlawEncoder()
	par := newPCMCodecParams()
	require.NoError(t, enc.Init(par))
	// Should not panic
	enc.Close()
}

func TestClose_ReleasesResources(t *testing.T) {
	t.Parallel()
	enc := NewAlawEncoder()
	par := newPCMCodecParams()
	require.NoError(t, enc.Init(par))

	inner := enc.(*alawEncoder)
	require.NotNil(t, inner.ring)
	require.NotNil(t, inner.r)

	enc.Close()
	assert.Nil(t, inner.buf)
	assert.Nil(t, inner.ring)
	assert.Nil(t, inner.r)
}

func TestEncode_SingleFrame(t *testing.T) {
	t.Parallel()
	enc := NewAlawEncoder()
	defer enc.Close()

	// Use 8kHz so no resampling is needed — frame maps 1:1
	par := pcm.NewCodecParameters(0, gomedia.PCM, 1, ALAWSampleRate)
	require.NoError(t, enc.Init(par))

	inner := enc.(*alawEncoder)

	// AlawFrameSize = 1600 bytes of 16-bit PCM
	data := make([]byte, AlawFrameSize)
	pkt := pcm.NewPacket(data, 0, "test", time.Now(), par, 100*time.Millisecond)

	result, err := inner.Encode(pkt)
	require.NoError(t, err)

	// Exactly one frame of input should produce exactly one encoded packet
	require.Len(t, result, 1)
	assert.Greater(t, len(result[0].Data()), 0)
}

func TestEncode_MultipleFrames(t *testing.T) {
	t.Parallel()
	enc := NewAlawEncoder()
	defer enc.Close()

	par := pcm.NewCodecParameters(0, gomedia.PCM, 1, ALAWSampleRate)
	require.NoError(t, enc.Init(par))

	inner := enc.(*alawEncoder)

	totalPackets := 0
	for i := range 10 {
		data := make([]byte, AlawFrameSize)
		ts := time.Duration(i) * 100 * time.Millisecond
		pkt := pcm.NewPacket(data, ts, "test", time.Now(), par, 100*time.Millisecond)

		result, err := inner.Encode(pkt)
		require.NoError(t, err)
		totalPackets += len(result)
	}

	assert.Equal(t, 10, totalPackets)
}

func TestEncode_BufferingPartialFrames(t *testing.T) {
	t.Parallel()
	enc := NewAlawEncoder()
	defer enc.Close()

	par := pcm.NewCodecParameters(0, gomedia.PCM, 1, ALAWSampleRate)
	require.NoError(t, enc.Init(par))

	inner := enc.(*alawEncoder)
	halfBytes := AlawFrameSize / 2

	// Send half a frame — should produce no output
	halfData := make([]byte, halfBytes)
	pkt := pcm.NewPacket(halfData, 0, "test", time.Now(), par, 0)

	result, err := inner.Encode(pkt)
	require.NoError(t, err)
	assert.Empty(t, result, "half frame should not produce output")

	// Internal buffer should hold the partial data
	assert.Equal(t, halfBytes, len(inner.buf), "buffer should hold half frame bytes")

	// Send the other half — should now produce one packet
	pkt2 := pcm.NewPacket(halfData, 0, "test", time.Now(), par, 0)
	result, err = inner.Encode(pkt2)
	require.NoError(t, err)
	assert.Len(t, result, 1, "completing the frame should produce one packet")
}

func TestEncode_LargerThanOneFrame(t *testing.T) {
	t.Parallel()
	enc := NewAlawEncoder()
	defer enc.Close()

	par := pcm.NewCodecParameters(0, gomedia.PCM, 1, ALAWSampleRate)
	require.NoError(t, enc.Init(par))

	inner := enc.(*alawEncoder)

	// Send 2.5 frames worth of data
	data := make([]byte, AlawFrameSize*5/2)
	pkt := pcm.NewPacket(data, 0, "test", time.Now(), par, 0)

	result, err := inner.Encode(pkt)
	require.NoError(t, err)

	// Should have encoded 2 complete frames
	assert.Equal(t, 2, len(result))

	// Internal buffer should hold the remaining half frame
	assert.Equal(t, AlawFrameSize/2, len(inner.buf))
}

func TestEncode_OutputPacketMetadata(t *testing.T) {
	t.Parallel()
	enc := NewAlawEncoder()
	defer enc.Close()

	par := pcm.NewCodecParameters(0, gomedia.PCM, 1, ALAWSampleRate)
	require.NoError(t, enc.Init(par))

	inner := enc.(*alawEncoder)

	sourceID := "rtsp://example.com/stream"
	startTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	data := make([]byte, AlawFrameSize)
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
	enc := NewAlawEncoder()
	defer enc.Close()

	par := pcm.NewCodecParameters(0, gomedia.PCM, 1, ALAWSampleRate)
	require.NoError(t, enc.Init(par))

	inner := enc.(*alawEncoder)
	require.NotNil(t, inner.ring)

	data := make([]byte, AlawFrameSize)
	pkt := pcm.NewPacket(data, 0, "test", time.Now(), par, inner.frameDuration)

	result, err := inner.Encode(pkt)
	require.NoError(t, err)
	require.Len(t, result, 1)

	// Verify data is present (ring allocation is internal)
	assert.Greater(t, len(result[0].Data()), 0)
}

func TestEncode_EmptyInput(t *testing.T) {
	t.Parallel()
	enc := NewAlawEncoder()
	defer enc.Close()

	par := pcm.NewCodecParameters(0, gomedia.PCM, 1, ALAWSampleRate)
	require.NoError(t, enc.Init(par))

	inner := enc.(*alawEncoder)

	// Empty data should be handled gracefully
	pkt := pcm.NewPacket(nil, 0, "test", time.Now(), par, 0)
	result, err := inner.Encode(pkt)
	require.NoError(t, err)
	assert.Empty(t, result)

	// Single byte should also be skipped
	pkt2 := pcm.NewPacket([]byte{0x00}, 0, "test", time.Now(), par, 0)
	result, err = inner.Encode(pkt2)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestEncode_WithRealPCMData(t *testing.T) {
	t.Parallel()

	pcmPackets := loadFixturePackets(t, pcmPacketsPath)

	enc := NewAlawEncoder()
	defer enc.Close()

	par := newPCMCodecParams()
	require.NoError(t, enc.Init(par))

	inner := enc.(*alawEncoder)
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

	// Input is 16kHz mono PCM, resampled to 8kHz before A-law encoding
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

	enc := NewAlawEncoder()
	defer enc.Close()

	par := newPCMCodecParams()
	require.NoError(t, enc.Init(par))

	inner := enc.(*alawEncoder)
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
		// A-law encodes 1:1 from 16-bit → 8-bit, then resampled, so output should be
		// roughly AlawFrameSize/2 bytes (800 samples of 8-bit A-law = 800 bytes max)
		assert.Less(t, size, AlawFrameSize, "packet %d unexpectedly large: %d bytes", i, size)
		assert.Greater(t, size, 0, "packet %d is empty", i)
	}
}

func TestEncode_StereoDownmix(t *testing.T) {
	t.Parallel()

	enc := NewAlawEncoder()
	defer enc.Close()

	// Stereo 8kHz — no resampling, only downmix
	par := pcm.NewCodecParameters(0, gomedia.PCM, 2, ALAWSampleRate)
	require.NoError(t, enc.Init(par))

	inner := enc.(*alawEncoder)

	// inpFrameSize for stereo = AlawFrameSize * 2 = 3200
	require.Equal(t, AlawFrameSize*2, inner.inpFrameSize)

	// Create stereo PCM: duplicate each mono sample into L and R
	stereoData := make([]byte, inner.inpFrameSize)
	for i := 0; i < len(stereoData); i += 4 {
		// L channel sample
		stereoData[i] = 0x10
		stereoData[i+1] = 0x20
		// R channel sample
		stereoData[i+2] = 0x30
		stereoData[i+3] = 0x40
	}

	pkt := pcm.NewPacket(stereoData, 0, "test", time.Now(), par, 100*time.Millisecond)
	result, err := inner.Encode(pkt)
	require.NoError(t, err)
	require.Len(t, result, 1, "one full stereo frame should produce one output packet")
	assert.Greater(t, len(result[0].Data()), 0)
}

func TestEncode_StereoWithRealData(t *testing.T) {
	t.Parallel()

	pcmPackets := loadFixturePackets(t, pcmPacketsPath)

	enc := NewAlawEncoder()
	defer enc.Close()

	// Stereo at input sample rate — duplicate mono data to fake stereo
	par := pcm.NewCodecParameters(0, gomedia.PCM, 2, 16000)
	require.NoError(t, enc.Init(par))

	inner := enc.(*alawEncoder)
	startTime := time.Now()

	var allOutput []gomedia.AudioPacket
	for _, fp := range pcmPackets {
		// Duplicate mono samples into stereo (interleave L=R)
		stereoData := make([]byte, len(fp.Data)*2)
		for i := 0; i < len(fp.Data); i += 2 {
			stereoData[i*2] = fp.Data[i]
			stereoData[i*2+1] = fp.Data[i+1]
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

func TestEncode_CompactionPreventsBufGrowth(t *testing.T) {
	t.Parallel()
	enc := NewAlawEncoder()
	defer enc.Close()

	par := pcm.NewCodecParameters(0, gomedia.PCM, 1, ALAWSampleRate)
	require.NoError(t, enc.Init(par))

	inner := enc.(*alawEncoder)

	// Feed many frames and check the buffer doesn't grow unboundedly
	for i := range 100 {
		data := make([]byte, AlawFrameSize)
		ts := time.Duration(i) * 100 * time.Millisecond
		pkt := pcm.NewPacket(data, ts, "test", time.Now(), par, 100*time.Millisecond)

		_, err := inner.Encode(pkt)
		require.NoError(t, err)
	}

	// After encoding exact frames, buffer should be empty
	assert.Equal(t, 0, len(inner.buf), "buffer should be empty after encoding exact frames")

	// Feed partial frames and verify compaction
	for i := range 100 {
		// Feed 1.5 frames each time
		data := make([]byte, AlawFrameSize*3/2)
		ts := time.Duration(i) * 150 * time.Millisecond
		pkt := pcm.NewPacket(data, ts, "test", time.Now(), par, 0)

		_, err := inner.Encode(pkt)
		require.NoError(t, err)
	}

	// Buffer should only contain leftover partial data, not grow to N * 1.5 * frameSize
	assert.Less(t, len(inner.buf), AlawFrameSize, "buffer should be compacted")
}

func TestEncode_CodecParametersValidity(t *testing.T) {
	t.Parallel()
	enc := NewAlawEncoder()
	defer enc.Close()

	par := newPCMCodecParams()
	require.NoError(t, enc.Init(par))

	inner := enc.(*alawEncoder)
	require.NotNil(t, inner.codecPar)

	assert.Equal(t, gomedia.PCMAlaw, inner.codecPar.Type())
	assert.Equal(t, uint64(ALAWSampleRate), inner.codecPar.SampleRate())
	assert.Equal(t, uint8(1), inner.codecPar.Channels())
}

func TestFrameDuration(t *testing.T) {
	t.Parallel()

	// Frame duration should always be 100ms regardless of input sample rate
	rates := []uint64{8000, 16000, 44100, 48000}
	for _, sr := range rates {
		t.Run("rate", func(t *testing.T) {
			t.Parallel()
			enc := NewAlawEncoder()
			defer enc.Close()

			par := pcm.NewCodecParameters(0, gomedia.PCM, 1, sr)
			require.NoError(t, enc.Init(par))

			inner := enc.(*alawEncoder)
			assert.Equal(t, 100*time.Millisecond, inner.frameDuration)
		})
	}
}
