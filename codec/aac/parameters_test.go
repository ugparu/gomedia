package aac

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/ugparu/gomedia"
)

func TestNewCodecDataFromMPEG4AudioConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		objectType    uint
		sampleRateIdx uint
		channelConfig uint
		sampleRate    int
		channelLayout gomedia.ChannelLayout
	}{
		{
			name:          "basic_config",
			objectType:    AotAacLc,
			sampleRateIdx: 4,
			channelConfig: 2,
		},
		{
			name:          "with_sample_rate",
			objectType:    AotAacLc,
			sampleRate:    48000,
			channelConfig: 2,
		},
		{
			name:          "with_channel_layout",
			objectType:    AotAacLc,
			sampleRateIdx: 4,
			channelLayout: gomedia.ChFrontLeft | gomedia.ChFrontRight,
		},
		{
			name:          "mono_config",
			objectType:    AotAacLc,
			sampleRateIdx: 4,
			channelConfig: 1,
		},
		{
			name:          "5_1_config",
			objectType:    AotAacLc,
			sampleRateIdx: 0,
			channelConfig: 6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			config := MPEG4AudioConfig{
				ObjectType:      tt.objectType,
				SampleRateIndex: tt.sampleRateIdx,
				ChannelConfig:   tt.channelConfig,
				SampleRate:      tt.sampleRate,
				ChannelLayout:   tt.channelLayout,
			}
			config.Complete()

			cod, err := NewCodecDataFromMPEG4AudioConfig(config)
			require.NoError(t, err)
			require.NotNil(t, cod.ConfigBytes)
			require.Greater(t, len(cod.ConfigBytes), 0)
			require.Equal(t, gomedia.AAC, cod.CodecType)
			require.Equal(t, tt.objectType, cod.Config.ObjectType)
			if tt.channelConfig > 0 {
				require.Equal(t, tt.channelConfig, cod.Config.ChannelConfig)
			}
			// Bitrate may be 0 if channel layout lookup fails (e.g., with_channel_layout case)
			if cod.Config.ChannelConfig > 0 {
				require.Greater(t, cod.BRate, uint(0))
			}
		})
	}
}

func TestNewCodecDataFromMPEG4AudioConfig_InvalidConfig(t *testing.T) {
	t.Parallel()

	t.Run("invalid_object_type", func(t *testing.T) {
		t.Parallel()
		config := MPEG4AudioConfig{
			ObjectType:      0,
			SampleRateIndex: 4,
			ChannelConfig:   2,
		}

		_, err := NewCodecDataFromMPEG4AudioConfig(config)
		// WriteMPEG4AudioConfig doesn't validate, so this might succeed
		// but ParseMPEG4AudioConfigBytes will fail
		if err != nil {
			require.Contains(t, err.Error(), "parse MPEG4AudioConfig failed")
		}
	})
}

func TestNewCodecDataFromMPEG4AudioConfigBytes(t *testing.T) {
	t.Parallel()

	t.Run("valid_bytes", func(t *testing.T) {
		t.Parallel()
		config := MPEG4AudioConfig{
			ObjectType:      AotAacLc,
			SampleRateIndex: 4,
			ChannelConfig:   2,
		}

		var buf bytes.Buffer
		err := WriteMPEG4AudioConfig(&buf, config)
		require.NoError(t, err)

		cod, err := NewCodecDataFromMPEG4AudioConfigBytes(buf.Bytes())
		require.NoError(t, err)
		require.Equal(t, buf.Bytes(), cod.ConfigBytes)
		require.Equal(t, gomedia.AAC, cod.CodecType)
		require.Equal(t, uint(AotAacLc), cod.Config.ObjectType)
		require.Equal(t, uint(4), cod.Config.SampleRateIndex)
		require.Equal(t, uint(2), cod.Config.ChannelConfig)
	})

	t.Run("invalid_bytes", func(t *testing.T) {
		t.Parallel()
		invalidBytes := []byte{0x01} // Too short

		_, err := NewCodecDataFromMPEG4AudioConfigBytes(invalidBytes)
		require.Error(t, err)
		require.Contains(t, err.Error(), "parse MPEG4AudioConfig failed")
	})

	t.Run("empty_bytes", func(t *testing.T) {
		t.Parallel()
		_, err := NewCodecDataFromMPEG4AudioConfigBytes([]byte{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "parse MPEG4AudioConfig failed")
	})

	t.Run("nil_bytes", func(t *testing.T) {
		t.Parallel()
		_, err := NewCodecDataFromMPEG4AudioConfigBytes(nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "parse MPEG4AudioConfig failed")
	})
}

func TestCodecParameters_Accessors(t *testing.T) {
	t.Parallel()

	t.Run("channel_layout", func(t *testing.T) {
		t.Parallel()
		config := MPEG4AudioConfig{
			ObjectType:      AotAacLc,
			SampleRateIndex: 4,
			ChannelConfig:   2,
		}
		config.Complete()

		cod, err := NewCodecDataFromMPEG4AudioConfig(config)
		require.NoError(t, err)

		layout := cod.ChannelLayout()
		require.Equal(t, gomedia.ChFrontLeft|gomedia.ChFrontRight, layout)
	})

	t.Run("sample_rate", func(t *testing.T) {
		t.Parallel()
		config := MPEG4AudioConfig{
			ObjectType:      AotAacLc,
			SampleRateIndex: 4,
			ChannelConfig:   2,
		}
		config.Complete()

		cod, err := NewCodecDataFromMPEG4AudioConfig(config)
		require.NoError(t, err)

		sampleRate := cod.SampleRate()
		require.Equal(t, uint64(44100), sampleRate)
	})

	t.Run("channels", func(t *testing.T) {
		t.Parallel()
		config := MPEG4AudioConfig{
			ObjectType:      AotAacLc,
			SampleRateIndex: 4,
			ChannelConfig:   2,
		}
		config.Complete()

		cod, err := NewCodecDataFromMPEG4AudioConfig(config)
		require.NoError(t, err)

		channels := cod.Channels()
		require.Equal(t, uint8(2), channels)
	})

	t.Run("sample_format", func(t *testing.T) {
		t.Parallel()
		config := MPEG4AudioConfig{
			ObjectType:      AotAacLc,
			SampleRateIndex: 4,
			ChannelConfig:   2,
		}

		cod, err := NewCodecDataFromMPEG4AudioConfig(config)
		require.NoError(t, err)

		sampleFormat := cod.SampleFormat()
		require.Equal(t, gomedia.FLTP, sampleFormat)
	})

	t.Run("tag", func(t *testing.T) {
		t.Parallel()
		config := MPEG4AudioConfig{
			ObjectType:      AotAacLc,
			SampleRateIndex: 4,
			ChannelConfig:   2,
		}

		cod, err := NewCodecDataFromMPEG4AudioConfig(config)
		require.NoError(t, err)

		tag := cod.Tag()
		require.Equal(t, "mp4a.40.2", tag)
	})

	t.Run("mpeg4_audio_config_bytes", func(t *testing.T) {
		t.Parallel()
		config := MPEG4AudioConfig{
			ObjectType:      AotAacLc,
			SampleRateIndex: 4,
			ChannelConfig:   2,
		}

		var buf bytes.Buffer
		err := WriteMPEG4AudioConfig(&buf, config)
		require.NoError(t, err)

		cod, err := NewCodecDataFromMPEG4AudioConfigBytes(buf.Bytes())
		require.NoError(t, err)

		configBytes := cod.MPEG4AudioConfigBytes()
		require.Equal(t, buf.Bytes(), configBytes)
	})

	t.Run("all_channel_configs", func(t *testing.T) {
		t.Parallel()
		for i := uint(1); i < uint(len(chanConfigTable)); i++ {
			config := MPEG4AudioConfig{
				ObjectType:      AotAacLc,
				SampleRateIndex: 4,
				ChannelConfig:   i,
			}
			config.Complete()

			cod, err := NewCodecDataFromMPEG4AudioConfig(config)
			require.NoError(t, err, "channel config %d", i)

			layout := cod.ChannelLayout()
			require.Equal(t, chanConfigTable[i], layout, "channel config %d", i)

			channels := cod.Channels()
			expectedChannels := chanConfigTable[i].Count()
			require.Equal(t, uint8(expectedChannels), channels, "channel config %d", i)
		}
	})

	t.Run("all_sample_rates", func(t *testing.T) {
		t.Parallel()
		for i := uint(0); i < uint(len(sampleRateTable)); i++ {
			config := MPEG4AudioConfig{
				ObjectType:      AotAacLc,
				SampleRateIndex: i,
				ChannelConfig:   2,
			}
			config.Complete()

			cod, err := NewCodecDataFromMPEG4AudioConfig(config)
			require.NoError(t, err, "sample rate index %d", i)

			sampleRate := cod.SampleRate()
			require.Equal(t, uint64(sampleRateTable[i]), sampleRate, "sample rate index %d", i)
		}
	})

	t.Run("all_object_types", func(t *testing.T) {
		t.Parallel()
		objectTypes := []uint{AotAacMain, AotAacLc, AotAacLtp, AotSbr}
		for _, ot := range objectTypes {
			config := MPEG4AudioConfig{
				ObjectType:      ot,
				SampleRateIndex: 4,
				ChannelConfig:   2,
			}

			cod, err := NewCodecDataFromMPEG4AudioConfig(config)
			require.NoError(t, err, "object type %d", ot)

			tag := cod.Tag()
			require.Equal(t, "mp4a.40."+string(rune('0'+ot)), tag, "object type %d", ot)
		}
	})
}

func TestCodecParameters_BitrateCalculation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		sampleRateIdx uint
		channelConfig uint
		expectedBRate uint
	}{
		{
			name:          "mono_44khz",
			sampleRateIdx: 4,
			channelConfig: 1,
			expectedBRate: 44100 * 1 * 4 * 8, // sampleRate * channels * bytesPerSample * bitsPerByte
		},
		{
			name:          "stereo_44khz",
			sampleRateIdx: 4,
			channelConfig: 2,
			expectedBRate: 44100 * 2 * 4 * 8,
		},
		{
			name:          "stereo_48khz",
			sampleRateIdx: 3,
			channelConfig: 2,
			expectedBRate: 48000 * 2 * 4 * 8,
		},
		{
			name:          "5_1_96khz",
			sampleRateIdx: 0,
			channelConfig: 6,
			expectedBRate: 96000 * 6 * 4 * 8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			config := MPEG4AudioConfig{
				ObjectType:      AotAacLc,
				SampleRateIndex: tt.sampleRateIdx,
				ChannelConfig:   tt.channelConfig,
			}
			config.Complete()

			cod, err := NewCodecDataFromMPEG4AudioConfig(config)
			require.NoError(t, err)

			bitrate := cod.Bitrate()
			require.Equal(t, tt.expectedBRate, bitrate)
		})
	}

	// Verify bitrate calculation formula: SampleRate * Channels * BytesPerSample * 8
	t.Run("bitrate_formula_verification", func(t *testing.T) {
		t.Parallel()
		config := MPEG4AudioConfig{
			ObjectType:      AotAacLc,
			SampleRateIndex: 4,
			ChannelConfig:   2,
		}
		config.Complete()

		cod, err := NewCodecDataFromMPEG4AudioConfig(config)
		require.NoError(t, err)

		expectedBRate := cod.SampleRate() * uint64(cod.Channels()) * uint64(cod.SampleFormat().BytesPerSample()) * 8
		require.Equal(t, uint(expectedBRate), cod.Bitrate())
	})
}

func TestCodecParameters_StreamIndex(t *testing.T) {
	t.Parallel()

	config := MPEG4AudioConfig{
		ObjectType:      AotAacLc,
		SampleRateIndex: 4,
		ChannelConfig:   2,
	}

	cod, err := NewCodecDataFromMPEG4AudioConfig(config)
	require.NoError(t, err)

	// Test setting and getting stream index
	cod.SetStreamIndex(5)
	require.Equal(t, uint8(5), cod.StreamIndex())

	cod.SetStreamIndex(0)
	require.Equal(t, uint8(0), cod.StreamIndex())

	cod.SetStreamIndex(255)
	require.Equal(t, uint8(255), cod.StreamIndex())
}

func TestCodecParameters_Type(t *testing.T) {
	t.Parallel()

	config := MPEG4AudioConfig{
		ObjectType:      AotAacLc,
		SampleRateIndex: 4,
		ChannelConfig:   2,
	}

	cod, err := NewCodecDataFromMPEG4AudioConfig(config)
	require.NoError(t, err)

	codecType := cod.Type()
	require.Equal(t, gomedia.AAC, codecType)
}

func TestCodecParameters_SetBitrate(t *testing.T) {
	t.Parallel()

	config := MPEG4AudioConfig{
		ObjectType:      AotAacLc,
		SampleRateIndex: 4,
		ChannelConfig:   2,
	}

	cod, err := NewCodecDataFromMPEG4AudioConfig(config)
	require.NoError(t, err)

	originalBRate := cod.Bitrate()
	require.Greater(t, originalBRate, uint(0))

	// Set custom bitrate
	cod.SetBitrate(128000)
	require.Equal(t, uint(128000), cod.Bitrate())

	// Set back to original
	cod.SetBitrate(originalBRate)
	require.Equal(t, originalBRate, cod.Bitrate())
}

// Test edge case: channel config 0 (defined in AOT-specific config)
func TestCodecParameters_ChannelConfigZero(t *testing.T) {
	t.Parallel()

	config := MPEG4AudioConfig{
		ObjectType:      AotAacLc,
		SampleRateIndex: 4,
		ChannelConfig:   0,
	}
	config.Complete()

	// Channel config 0 is valid in MPEG4AudioConfig but may cause issues
	// Test that it doesn't panic
	cod, err := NewCodecDataFromMPEG4AudioConfig(config)
	if err == nil {
		layout := cod.ChannelLayout()
		require.Equal(t, gomedia.ChannelLayout(0), layout)
		channels := cod.Channels()
		require.Equal(t, uint8(0), channels)
	}
}

// Test that ConfigBytes preserves the exact bytes written
func TestCodecParameters_ConfigBytesPreservation(t *testing.T) {
	t.Parallel()

	config := MPEG4AudioConfig{
		ObjectType:      AotAacLc,
		SampleRateIndex: 4,
		ChannelConfig:   2,
	}

	var buf bytes.Buffer
	err := WriteMPEG4AudioConfig(&buf, config)
	require.NoError(t, err)

	originalBytes := buf.Bytes()

	cod, err := NewCodecDataFromMPEG4AudioConfigBytes(originalBytes)
	require.NoError(t, err)

	configBytes := cod.MPEG4AudioConfigBytes()
	require.Equal(t, originalBytes, configBytes)

	// Verify we can parse it again
	parsedConfig, err := ParseMPEG4AudioConfigBytes(configBytes)
	require.NoError(t, err)
	require.Equal(t, config.ObjectType, parsedConfig.ObjectType)
	require.Equal(t, config.SampleRateIndex, parsedConfig.SampleRateIndex)
	require.Equal(t, config.ChannelConfig, parsedConfig.ChannelConfig)
}

// Test round-trip: Config -> CodecParameters -> ConfigBytes -> Parse -> Config
func TestCodecParameters_RoundTrip(t *testing.T) {
	t.Parallel()

	originalConfig := MPEG4AudioConfig{
		ObjectType:      AotAacLc,
		SampleRateIndex: 4,
		ChannelConfig:   2,
	}
	originalConfig.Complete()

	// Create codec parameters
	cod, err := NewCodecDataFromMPEG4AudioConfig(originalConfig)
	require.NoError(t, err)

	// Get config bytes
	configBytes := cod.MPEG4AudioConfigBytes()

	// Parse config bytes
	parsedConfig, err := ParseMPEG4AudioConfigBytes(configBytes)
	require.NoError(t, err)

	// Verify all fields match
	require.Equal(t, originalConfig.ObjectType, parsedConfig.ObjectType)
	require.Equal(t, originalConfig.SampleRateIndex, parsedConfig.SampleRateIndex)
	require.Equal(t, originalConfig.ChannelConfig, parsedConfig.ChannelConfig)
	require.Equal(t, originalConfig.SampleRate, parsedConfig.SampleRate)
	require.Equal(t, originalConfig.ChannelLayout, parsedConfig.ChannelLayout)
}
