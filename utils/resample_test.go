package utils

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResampler(t *testing.T) {
	const srcSampleRate = 44100
	const ch = 1
	const s = 10

	var dstSampleRates = []int{8000, 16000, 44100, 48000, 96000, 192000}

	for _, dstSampleRate := range dstSampleRates {
		v, err := NewPcmS16leResampler(1, srcSampleRate, dstSampleRate)
		require.NoError(t, err)

		pcm := make([]byte, srcSampleRate*ch*s*2)
		for i := 0; i < len(pcm); i += 4 {
			pcm[i] = byte(i)
			pcm[i+1] = byte(i + 1)
			pcm[i+2] = byte(i + 2)
			pcm[i+3] = byte(i + 3)
		}

		npcm, err := v.Resample(pcm)
		require.NoError(t, err)
		require.Equal(t, len(npcm), dstSampleRate*ch*s*2)
	}
}
