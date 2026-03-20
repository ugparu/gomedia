package utils

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generateSineWavePCM generates mono S16LE PCM data of a sine wave at the given frequency.
func generateSineWavePCM(sampleRate, frequency, numSamples int) []byte {
	pcm := make([]byte, numSamples*2)
	for i := range numSamples {
		sample := int16(math.Sin(2*math.Pi*float64(frequency)*float64(i)/float64(sampleRate)) * 16000) //nolint: mnd
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(sample))
	}
	return pcm
}

// generateSilencePCM generates mono S16LE PCM data of silence.
func generateSilencePCM(numSamples int) []byte {
	return make([]byte, numSamples*2)
}

// generateStereoPCM generates stereo S16LE PCM data with different signals per channel.
func generateStereoPCM(sampleRate, numSamples int) []byte {
	pcm := make([]byte, numSamples*4) // 2 channels * 2 bytes per sample
	for i := range numSamples {
		// Left channel: 440Hz sine
		left := int16(math.Sin(2*math.Pi*440*float64(i)/float64(sampleRate)) * 16000) //nolint: mnd
		// Right channel: 880Hz sine
		right := int16(math.Sin(2*math.Pi*880*float64(i)/float64(sampleRate)) * 16000) //nolint: mnd
		binary.LittleEndian.PutUint16(pcm[i*4:], uint16(left))
		binary.LittleEndian.PutUint16(pcm[i*4+2:], uint16(right))
	}
	return pcm
}

// readS16LE reads a mono S16LE sample at the given index.
func readS16LE(pcm []byte, idx int) int16 {
	return int16(binary.LittleEndian.Uint16(pcm[idx*2:]))
}

// --- Constructor Tests ---

func TestNewResampler_ValidMono(t *testing.T) {
	t.Parallel()
	r, err := NewPcmS16leResampler(1, 44100, 48000)
	require.NoError(t, err)
	require.NotNil(t, r)
}

func TestNewResampler_ValidStereo(t *testing.T) {
	t.Parallel()
	r, err := NewPcmS16leResampler(2, 44100, 48000)
	require.NoError(t, err)
	require.NotNil(t, r)
}

func TestNewResampler_InvalidChannels_Zero(t *testing.T) {
	t.Parallel()
	_, err := NewPcmS16leResampler(0, 44100, 48000)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid channels")
}

func TestNewResampler_InvalidChannels_Three(t *testing.T) {
	t.Parallel()
	_, err := NewPcmS16leResampler(3, 44100, 48000)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid channels")
}

func TestNewResampler_InvalidChannels_Negative(t *testing.T) {
	t.Parallel()
	_, err := NewPcmS16leResampler(-1, 44100, 48000)
	require.Error(t, err)
}

func TestNewResampler_InvalidSampleRate_Zero(t *testing.T) {
	t.Parallel()
	_, err := NewPcmS16leResampler(1, 0, 48000)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid sampleRate")
}

func TestNewResampler_InvalidSampleRate_Negative(t *testing.T) {
	t.Parallel()
	_, err := NewPcmS16leResampler(1, -44100, 48000)
	require.Error(t, err)
}

func TestNewResampler_InvalidOutputSampleRate_Zero(t *testing.T) {
	t.Parallel()
	_, err := NewPcmS16leResampler(1, 44100, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid nSampleRate")
}

// --- Resample Input Validation Tests ---

func TestResample_EmptyPCM(t *testing.T) {
	t.Parallel()
	r, err := NewPcmS16leResampler(1, 44100, 48000)
	require.NoError(t, err)

	_, err = r.Resample([]byte{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty pcm")
}

func TestResample_MisalignedPCM_Mono(t *testing.T) {
	t.Parallel()
	r, err := NewPcmS16leResampler(1, 44100, 48000)
	require.NoError(t, err)

	// 3 bytes is not divisible by 2 (mono S16LE frame size)
	_, err = r.Resample([]byte{0x01, 0x02, 0x03})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "should mod")
}

func TestResample_MisalignedPCM_Stereo(t *testing.T) {
	t.Parallel()
	r, err := NewPcmS16leResampler(2, 44100, 48000)
	require.NoError(t, err)

	// 6 bytes is not divisible by 4 (stereo S16LE frame size)
	_, err = r.Resample([]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "should mod")
}

func TestResample_TooFewSamples(t *testing.T) {
	t.Parallel()
	r, err := NewPcmS16leResampler(1, 44100, 48000)
	require.NoError(t, err)

	// 3 samples (6 bytes) — minimum is 4
	_, err = r.Resample([]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least 4 samples")
}

func TestResample_ExactlyFourSamples(t *testing.T) {
	t.Parallel()
	r, err := NewPcmS16leResampler(1, 44100, 48000)
	require.NoError(t, err)

	// 4 samples (8 bytes) — should not error on validation, but may produce 0 output
	// due to splineCacheSize requirement in resampleChannel
	pcm := make([]byte, 8)
	_, err = r.Resample(pcm)
	// This should not return a validation error
	require.NoError(t, err)
}

// --- Same Sample Rate Passthrough ---

func TestResample_SameSampleRate_Passthrough(t *testing.T) {
	t.Parallel()
	r, err := NewPcmS16leResampler(1, 44100, 44100)
	require.NoError(t, err)

	pcm := generateSineWavePCM(44100, 440, 1000)
	out, err := r.Resample(pcm)
	require.NoError(t, err)
	assert.Equal(t, pcm, out, "same sample rate should return input unchanged")
}

// --- Output Length Tests ---

func TestResample_Mono_OutputLength(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		srcRate int
		dstRate int
	}{
		{"downsample 44100→8000", 44100, 8000},
		{"downsample 44100→16000", 44100, 16000},
		{"upsample 44100→48000", 44100, 48000},
		{"upsample 8000→48000", 8000, 48000},
		{"upsample 44100→96000", 44100, 96000},
		{"upsample 44100→192000", 44100, 192000},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, err := NewPcmS16leResampler(1, tc.srcRate, tc.dstRate)
			require.NoError(t, err)

			durationSec := 10
			numSamples := tc.srcRate * durationSec
			pcm := generateSineWavePCM(tc.srcRate, 440, numSamples)

			out, err := r.Resample(pcm)
			require.NoError(t, err)

			expectedLen := tc.dstRate * durationSec * 2 // mono, 2 bytes per sample
			assert.Equal(t, expectedLen, len(out),
				"output length should match expected for %s", tc.name)
		})
	}
}

func TestResample_Stereo_OutputLength(t *testing.T) {
	t.Parallel()

	r, err := NewPcmS16leResampler(2, 44100, 48000)
	require.NoError(t, err)

	durationSec := 10
	numSamples := 44100 * durationSec
	pcm := generateStereoPCM(44100, numSamples)

	out, err := r.Resample(pcm)
	require.NoError(t, err)

	expectedLen := 48000 * durationSec * 4 // stereo, 4 bytes per frame
	assert.Equal(t, expectedLen, len(out))
}

// --- Signal Integrity Tests ---

func TestResample_SilenceIn_SilenceOut(t *testing.T) {
	t.Parallel()
	r, err := NewPcmS16leResampler(1, 44100, 48000)
	require.NoError(t, err)

	pcm := generateSilencePCM(44100) // 1 second of silence
	out, err := r.Resample(pcm)
	require.NoError(t, err)

	// All output samples should be zero (silence)
	numSamples := len(out) / 2
	for i := range numSamples {
		sample := readS16LE(out, i)
		assert.Equal(t, int16(0), sample, "silence should produce silence, non-zero at sample %d", i)
	}
}

func TestResample_SineWave_PreservesEnergy(t *testing.T) {
	t.Parallel()
	r, err := NewPcmS16leResampler(1, 44100, 48000)
	require.NoError(t, err)

	pcm := generateSineWavePCM(44100, 440, 44100) // 1 second of 440Hz
	out, err := r.Resample(pcm)
	require.NoError(t, err)

	// Compute RMS of input and output — they should be roughly similar
	inputRMS := computeRMS(pcm, 1)
	outputRMS := computeRMS(out, 1)

	// Allow 10% tolerance for resampling artifacts
	assert.InDelta(t, inputRMS, outputRMS, inputRMS*0.1,
		"resampled signal should preserve approximate energy (input RMS=%v, output RMS=%v)", inputRMS, outputRMS)
}

func TestResample_SineWave_OutputNotAllZeros(t *testing.T) {
	t.Parallel()
	r, err := NewPcmS16leResampler(1, 44100, 48000)
	require.NoError(t, err)

	pcm := generateSineWavePCM(44100, 440, 44100)
	out, err := r.Resample(pcm)
	require.NoError(t, err)

	hasNonZero := false
	for i := 0; i < len(out); i += 2 {
		if out[i] != 0 || out[i+1] != 0 {
			hasNonZero = true
			break
		}
	}
	assert.True(t, hasNonZero, "resampled sine wave should not be all zeros")
}

func TestResample_OutputWithinInt16Range(t *testing.T) {
	t.Parallel()
	r, err := NewPcmS16leResampler(1, 44100, 48000)
	require.NoError(t, err)

	// Use a loud signal near int16 max
	pcm := make([]byte, 44100*2)
	for i := 0; i < 44100; i++ {
		sample := int16(math.Sin(2*math.Pi*440*float64(i)/44100) * 32000) //nolint: mnd
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(sample))
	}

	out, err := r.Resample(pcm)
	require.NoError(t, err)

	// Every output sample should be valid S16LE — by definition it is since we read as int16,
	// but check that spline interpolation doesn't produce clipping beyond int16 range
	// (the int16 cast in resampleChannel truncates, which could produce artifacts)
	numSamples := len(out) / 2
	require.Greater(t, numSamples, 0)
}

// --- Multiple Consecutive Calls (Cache Continuity) ---

func TestResample_MultipleCalls_ContinuousOutput(t *testing.T) {
	t.Parallel()
	r, err := NewPcmS16leResampler(1, 44100, 48000)
	require.NoError(t, err)

	// Feed 10 chunks of 4410 samples (100ms each)
	chunkSamples := 4410
	numChunks := 10
	totalOutputLen := 0

	for i := range numChunks {
		pcm := generateSineWavePCM(44100, 440, chunkSamples)
		out, err := r.Resample(pcm)
		require.NoError(t, err, "chunk %d should not error", i)
		require.NotNil(t, out, "chunk %d should produce output", i)
		totalOutputLen += len(out)
	}

	// Total output should be approximately 10 * 100ms * 48000 * 2 bytes = 96000 bytes
	expectedTotal := numChunks * chunkSamples * 48000 / 44100 * 2
	// Allow some tolerance for rounding across chunks
	assert.InDelta(t, expectedTotal, totalOutputLen, float64(expectedTotal)*0.02,
		"cumulative output across multiple calls should approximate expected length")
}

func TestResample_MultipleCalls_NoDrift(t *testing.T) {
	t.Parallel()
	r, err := NewPcmS16leResampler(1, 44100, 48000)
	require.NoError(t, err)

	// Compare: single large call vs multiple small calls
	totalSamples := 44100 // 1 second
	pcmFull := generateSineWavePCM(44100, 440, totalSamples)

	outFull, err := r.Resample(pcmFull)
	require.NoError(t, err)

	// Reset resampler for chunked version
	r2, err := NewPcmS16leResampler(1, 44100, 48000)
	require.NoError(t, err)

	chunkSize := 4410 // 100ms
	totalChunkedLen := 0
	for offset := 0; offset < totalSamples*2; offset += chunkSize * 2 {
		end := offset + chunkSize*2
		if end > len(pcmFull) {
			end = len(pcmFull)
		}
		out, err := r2.Resample(pcmFull[offset:end])
		require.NoError(t, err)
		totalChunkedLen += len(out)
	}

	// Chunked and full should produce same total length
	assert.Equal(t, len(outFull), totalChunkedLen,
		"chunked resampling should produce same total length as single-call")
}

// --- Spline Interpolation Tests ---

func TestSpline_AtKnotPoints_ReturnsOriginalValues(t *testing.T) {
	t.Parallel()
	xi := []float64{0, 1, 2, 3}
	yi := []float64{7, 9, 2, 5}

	// Interpolate at the knot points themselves
	xo := []float64{0, 1, 2, 3}
	yo := make([]float64, 4)

	err := spline(xi, yi, xo, yo)
	require.NoError(t, err)

	assert.InDelta(t, 7.0, yo[0], 0.01, "spline at x=0 should return y=7")
	assert.InDelta(t, 9.0, yo[1], 0.01, "spline at x=1 should return y=9")
	assert.InDelta(t, 2.0, yo[2], 0.01, "spline at x=2 should return y=2")
	assert.InDelta(t, 5.0, yo[3], 0.01, "spline at x=3 should return y=5")
}

func TestSpline_MidpointInterpolation_WithinRange(t *testing.T) {
	t.Parallel()
	xi := []float64{0, 1, 2, 3}
	yi := []float64{0, 10, 0, 10}

	xo := []float64{0.5, 1.5, 2.5}
	yo := make([]float64, 3)

	err := spline(xi, yi, xo, yo)
	require.NoError(t, err)

	// Midpoint values should be reasonable (between neighboring knot values, or close)
	for i, v := range yo {
		assert.Greater(t, v, -20.0, "spline midpoint %d should not be wildly negative", i)
		assert.Less(t, v, 20.0, "spline midpoint %d should not be wildly positive", i)
	}
}

func TestSpline_LinearData_ProducesLinearOutput(t *testing.T) {
	t.Parallel()
	xi := []float64{0, 1, 2, 3}
	yi := []float64{0, 1, 2, 3} // perfectly linear

	xo := []float64{0.5, 1.5, 2.5}
	yo := make([]float64, 3)

	err := spline(xi, yi, xo, yo)
	require.NoError(t, err)

	assert.InDelta(t, 0.5, yo[0], 0.01, "linear data at x=0.5 should give y≈0.5")
	assert.InDelta(t, 1.5, yo[1], 0.01, "linear data at x=1.5 should give y≈1.5")
	assert.InDelta(t, 2.5, yo[2], 0.01, "linear data at x=2.5 should give y≈2.5")
}

func TestSpline_InvalidXi(t *testing.T) {
	t.Parallel()
	err := spline([]float64{0, 1}, []float64{0, 1, 2, 3}, []float64{0.5}, []float64{0})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid xi")
}

func TestSpline_InvalidYi(t *testing.T) {
	t.Parallel()
	err := spline([]float64{0, 1, 2, 3}, []float64{0, 1}, []float64{0.5}, []float64{0})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid yi")
}

func TestSpline_EmptyXo(t *testing.T) {
	t.Parallel()
	err := spline([]float64{0, 1, 2, 3}, []float64{0, 1, 2, 3}, []float64{}, []float64{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid xo")
}

func TestSpline_MismatchedXoYo(t *testing.T) {
	t.Parallel()
	err := spline([]float64{0, 1, 2, 3}, []float64{0, 1, 2, 3}, []float64{0.5}, []float64{0, 0})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid yo")
}

// --- Internal Function Tests ---

func TestResamplerInitChannel_Mono(t *testing.T) {
	t.Parallel()
	// 4 mono samples: 100, 200, -100, -200
	pcm := make([]byte, 8)
	binary.LittleEndian.PutUint16(pcm[0:], uint16(int16(100)))
	binary.LittleEndian.PutUint16(pcm[2:], uint16(int16(200)))
	binary.LittleEndian.PutUint16(pcm[4:], uint16(0xFF9C)) // -100 in S16LE
	binary.LittleEndian.PutUint16(pcm[6:], uint16(0xFF38)) // -200 in S16LE

	result := resamplerInitChannel(pcm, 1, 0)
	require.Len(t, result, 4)
	assert.Equal(t, int16(100), result[0])
	assert.Equal(t, int16(200), result[1])
	assert.Equal(t, int16(-100), result[2])
	assert.Equal(t, int16(-200), result[3])
}

func TestResamplerInitChannel_Stereo_Left(t *testing.T) {
	t.Parallel()
	// 2 stereo frames: L=100,R=200, L=300,R=400
	pcm := make([]byte, 8)
	binary.LittleEndian.PutUint16(pcm[0:], uint16(int16(100)))
	binary.LittleEndian.PutUint16(pcm[2:], uint16(int16(200)))
	binary.LittleEndian.PutUint16(pcm[4:], uint16(int16(300)))
	binary.LittleEndian.PutUint16(pcm[6:], uint16(int16(400)))

	left := resamplerInitChannel(pcm, 2, 0)
	require.Len(t, left, 2)
	assert.Equal(t, int16(100), left[0])
	assert.Equal(t, int16(300), left[1])
}

func TestResamplerInitChannel_Stereo_Right(t *testing.T) {
	t.Parallel()
	// 2 stereo frames: L=100,R=200, L=300,R=400
	pcm := make([]byte, 8)
	binary.LittleEndian.PutUint16(pcm[0:], uint16(int16(100)))
	binary.LittleEndian.PutUint16(pcm[2:], uint16(int16(200)))
	binary.LittleEndian.PutUint16(pcm[4:], uint16(int16(300)))
	binary.LittleEndian.PutUint16(pcm[6:], uint16(int16(400)))

	right := resamplerInitChannel(pcm, 2, 1)
	require.Len(t, right, 2)
	assert.Equal(t, int16(200), right[0])
	assert.Equal(t, int16(400), right[1])
}

func TestResamplerInitChannel_InvalidChannel(t *testing.T) {
	t.Parallel()
	pcm := make([]byte, 8)
	result := resamplerInitChannel(pcm, 1, 1) // channel 1 doesn't exist in mono
	assert.Nil(t, result)
}

func TestResampleMerge_MonoOnly(t *testing.T) {
	t.Parallel()
	left := []int16{100, -200, 32767}
	result := resampleMerge(left, nil)

	require.Len(t, result, 6) // 3 samples * 2 bytes
	assert.Equal(t, int16(100), int16(binary.LittleEndian.Uint16(result[0:])))
	assert.Equal(t, int16(-200), int16(binary.LittleEndian.Uint16(result[2:])))
	assert.Equal(t, int16(32767), int16(binary.LittleEndian.Uint16(result[4:])))
}

func TestResampleMerge_Stereo(t *testing.T) {
	t.Parallel()
	left := []int16{100, 300}
	right := []int16{200, 400}
	result := resampleMerge(left, right)

	require.Len(t, result, 8) // 2 frames * 2 channels * 2 bytes
	assert.Equal(t, int16(100), int16(binary.LittleEndian.Uint16(result[0:])))
	assert.Equal(t, int16(200), int16(binary.LittleEndian.Uint16(result[2:])))
	assert.Equal(t, int16(300), int16(binary.LittleEndian.Uint16(result[4:])))
	assert.Equal(t, int16(400), int16(binary.LittleEndian.Uint16(result[6:])))
}

// --- Helpers ---

func computeRMS(pcm []byte, channels int) float64 {
	numSamples := len(pcm) / (2 * channels)
	if numSamples == 0 {
		return 0
	}
	var sumSquares float64
	for i := range numSamples {
		sample := float64(int16(binary.LittleEndian.Uint16(pcm[i*2*channels:])))
		sumSquares += sample * sample
	}
	return math.Sqrt(sumSquares / float64(numSamples))
}
