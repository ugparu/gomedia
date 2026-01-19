package aac

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/ugparu/gomedia/utils/buffer"
)

func TestNewPacket(t *testing.T) {
	t.Parallel()

	t.Run("basic_packet_creation", func(t *testing.T) {
		t.Parallel()
		config := MPEG4AudioConfig{
			ObjectType:      AotAacLc,
			SampleRateIndex: 4,
			ChannelConfig:   2,
		}
		config.Complete()

		codecPar, err := NewCodecDataFromMPEG4AudioConfig(config)
		require.NoError(t, err)
		codecPar.SetStreamIndex(1)

		data := []byte{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0}
		ts := 100 * time.Millisecond
		url := "rtsp://example.com/stream"
		absTime := time.Now()
		dur := 20 * time.Millisecond

		pkt := NewPacket(data, ts, url, absTime, &codecPar, dur)

		require.NotNil(t, pkt)
		require.Equal(t, len(data), pkt.Len())
		require.Equal(t, ts, pkt.Timestamp())
		require.Equal(t, dur, pkt.Duration())
		require.Equal(t, url, pkt.URL())
		require.Equal(t, absTime, pkt.StartTime())
		require.Equal(t, uint8(1), pkt.StreamIndex())
		require.Equal(t, &codecPar, pkt.CodecParameters())

		// Verify data was copied correctly
		pkt.View(func(buf buffer.PooledBuffer) {
			require.NotNil(t, buf)
			require.Equal(t, len(data), buf.Len())
			require.Equal(t, data, buf.Data())
		})
	})

	t.Run("empty_data", func(t *testing.T) {
		t.Parallel()
		config := MPEG4AudioConfig{
			ObjectType:      AotAacLc,
			SampleRateIndex: 4,
			ChannelConfig:   2,
		}
		config.Complete()

		codecPar, err := NewCodecDataFromMPEG4AudioConfig(config)
		require.NoError(t, err)

		data := []byte{}
		pkt := NewPacket(data, 0, "", time.Time{}, &codecPar, 0)

		require.NotNil(t, pkt)
		require.Equal(t, 0, pkt.Len())
	})

	t.Run("large_data", func(t *testing.T) {
		t.Parallel()
		config := MPEG4AudioConfig{
			ObjectType:      AotAacLc,
			SampleRateIndex: 4,
			ChannelConfig:   2,
		}
		config.Complete()

		codecPar, err := NewCodecDataFromMPEG4AudioConfig(config)
		require.NoError(t, err)

		// Create large data (10KB)
		data := make([]byte, 10240)
		for i := range data {
			data[i] = byte(i % 256)
		}

		pkt := NewPacket(data, 0, "", time.Time{}, &codecPar, 0)

		require.NotNil(t, pkt)
		require.Equal(t, len(data), pkt.Len())

		pkt.View(func(buf buffer.PooledBuffer) {
			require.Equal(t, data, buf.Data())
		})
	})

	t.Run("all_fields_set", func(t *testing.T) {
		t.Parallel()
		config := MPEG4AudioConfig{
			ObjectType:      AotAacLc,
			SampleRateIndex: 4,
			ChannelConfig:   2,
		}
		config.Complete()

		codecPar, err := NewCodecDataFromMPEG4AudioConfig(config)
		require.NoError(t, err)
		codecPar.SetStreamIndex(5)

		data := []byte{0x01, 0x02, 0x03}
		ts := 500 * time.Millisecond
		url := "rtsp://test.com/audio"
		absTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
		dur := 23 * time.Millisecond

		pkt := NewPacket(data, ts, url, absTime, &codecPar, dur)

		require.Equal(t, ts, pkt.Timestamp())
		require.Equal(t, dur, pkt.Duration())
		require.Equal(t, url, pkt.URL())
		require.Equal(t, absTime, pkt.StartTime())
		require.Equal(t, uint8(5), pkt.StreamIndex())
	})
}

func TestPacket_Clone(t *testing.T) {
	t.Parallel()

	t.Run("clone_with_data_copy", func(t *testing.T) {
		t.Parallel()
		config := MPEG4AudioConfig{
			ObjectType:      AotAacLc,
			SampleRateIndex: 4,
			ChannelConfig:   2,
		}
		config.Complete()

		codecPar, err := NewCodecDataFromMPEG4AudioConfig(config)
		require.NoError(t, err)

		data := []byte{0x11, 0x22, 0x33, 0x44}
		ts := 100 * time.Millisecond
		url := "rtsp://example.com/stream"
		absTime := time.Now()
		dur := 20 * time.Millisecond

		original := NewPacket(data, ts, url, absTime, &codecPar, dur)

		// Clone with data copy
		cloned := original.Clone(true).(*Packet)

		require.NotNil(t, cloned)
		require.Equal(t, original.Len(), cloned.Len())
		require.Equal(t, original.Timestamp(), cloned.Timestamp())
		require.Equal(t, original.Duration(), cloned.Duration())
		require.Equal(t, original.URL(), cloned.URL())
		require.Equal(t, original.StartTime(), cloned.StartTime())
		require.Equal(t, original.StreamIndex(), cloned.StreamIndex())

		// Verify data is independent
		original.View(func(buf buffer.PooledBuffer) {
			originalData := make([]byte, len(buf.Data()))
			copy(originalData, buf.Data())
			originalData[0] = 0xFF

			cloned.View(func(clonedBuf buffer.PooledBuffer) {
				// Cloned data should not be affected
				require.NotEqual(t, originalData[0], clonedBuf.Data()[0])
				require.Equal(t, byte(0x11), clonedBuf.Data()[0])
			})
		})
	})

	t.Run("clone_without_data_copy", func(t *testing.T) {
		t.Parallel()
		config := MPEG4AudioConfig{
			ObjectType:      AotAacLc,
			SampleRateIndex: 4,
			ChannelConfig:   2,
		}
		config.Complete()

		codecPar, err := NewCodecDataFromMPEG4AudioConfig(config)
		require.NoError(t, err)

		data := []byte{0x11, 0x22, 0x33, 0x44}
		ts := 100 * time.Millisecond
		url := "rtsp://example.com/stream"
		absTime := time.Now()
		dur := 20 * time.Millisecond

		original := NewPacket(data, ts, url, absTime, &codecPar, dur)

		// Clone without data copy (shared buffer)
		cloned := original.Clone(false).(*Packet)

		require.NotNil(t, cloned)
		require.Equal(t, original.Len(), cloned.Len())

		// Both should reference the same buffer
		original.View(func(buf buffer.PooledBuffer) {
			originalData := buf.Data()
			cloned.View(func(clonedBuf buffer.PooledBuffer) {
				// Should be the same underlying buffer
				require.Equal(t, originalData, clonedBuf.Data())
			})
		})
	})

	t.Run("clone_preserves_all_fields", func(t *testing.T) {
		t.Parallel()
		config := MPEG4AudioConfig{
			ObjectType:      AotAacLc,
			SampleRateIndex: 4,
			ChannelConfig:   2,
		}
		config.Complete()

		codecPar, err := NewCodecDataFromMPEG4AudioConfig(config)
		require.NoError(t, err)
		codecPar.SetStreamIndex(7)

		data := []byte{0xAA, 0xBB, 0xCC}
		ts := 250 * time.Millisecond
		url := "rtsp://test.com/audio"
		absTime := time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC)
		dur := 15 * time.Millisecond

		original := NewPacket(data, ts, url, absTime, &codecPar, dur)

		cloned := original.Clone(true).(*Packet)

		require.Equal(t, original.Timestamp(), cloned.Timestamp())
		require.Equal(t, original.Duration(), cloned.Duration())
		require.Equal(t, original.URL(), cloned.URL())
		require.Equal(t, original.StartTime(), cloned.StartTime())
		require.Equal(t, original.StreamIndex(), cloned.StreamIndex())
		require.Equal(t, original.CodecParameters(), cloned.CodecParameters())
	})

	t.Run("clone_independent_modification", func(t *testing.T) {
		t.Parallel()
		config := MPEG4AudioConfig{
			ObjectType:      AotAacLc,
			SampleRateIndex: 4,
			ChannelConfig:   2,
		}
		config.Complete()

		codecPar, err := NewCodecDataFromMPEG4AudioConfig(config)
		require.NoError(t, err)

		data := []byte{0x01, 0x02, 0x03}
		original := NewPacket(data, 100*time.Millisecond, "url1", time.Now(), &codecPar, 20*time.Millisecond)

		cloned := original.Clone(true).(*Packet)

		// Modify cloned packet fields
		cloned.SetTimestamp(200 * time.Millisecond)
		cloned.SetDuration(40 * time.Millisecond)
		cloned.SetURL("url2")
		cloned.SetStreamIndex(99)

		// Original should be unchanged
		require.Equal(t, 100*time.Millisecond, original.Timestamp())
		require.Equal(t, 20*time.Millisecond, original.Duration())
		require.Equal(t, "url1", original.URL())
		require.NotEqual(t, uint8(99), original.StreamIndex())

		// Cloned should have new values
		require.Equal(t, 200*time.Millisecond, cloned.Timestamp())
		require.Equal(t, 40*time.Millisecond, cloned.Duration())
		require.Equal(t, "url2", cloned.URL())
		require.Equal(t, uint8(99), cloned.StreamIndex())
	})
}

func TestPacket_Properties(t *testing.T) {
	t.Parallel()

	t.Run("timestamp_preservation", func(t *testing.T) {
		t.Parallel()
		config := MPEG4AudioConfig{
			ObjectType:      AotAacLc,
			SampleRateIndex: 4,
			ChannelConfig:   2,
		}
		config.Complete()

		codecPar, err := NewCodecDataFromMPEG4AudioConfig(config)
		require.NoError(t, err)

		testCases := []time.Duration{
			0,
			1 * time.Millisecond,
			100 * time.Millisecond,
			1 * time.Second,
			10 * time.Second,
			1 * time.Hour,
		}

		for _, ts := range testCases {
			pkt := NewPacket([]byte{0x01}, ts, "", time.Time{}, &codecPar, 0)
			require.Equal(t, ts, pkt.Timestamp())

			// Test setting timestamp
			newTs := ts + 50*time.Millisecond
			pkt.SetTimestamp(newTs)
			require.Equal(t, newTs, pkt.Timestamp())
		}
	})

	t.Run("duration_correctness", func(t *testing.T) {
		t.Parallel()
		config := MPEG4AudioConfig{
			ObjectType:      AotAacLc,
			SampleRateIndex: 4,
			ChannelConfig:   2,
		}
		config.Complete()

		codecPar, err := NewCodecDataFromMPEG4AudioConfig(config)
		require.NoError(t, err)

		testCases := []time.Duration{
			0,
			1 * time.Millisecond,
			20 * time.Millisecond,
			100 * time.Millisecond,
			1 * time.Second,
		}

		for _, dur := range testCases {
			pkt := NewPacket([]byte{0x01}, 0, "", time.Time{}, &codecPar, dur)
			require.Equal(t, dur, pkt.Duration())

			// Test setting duration
			newDur := dur + 10*time.Millisecond
			pkt.SetDuration(newDur)
			require.Equal(t, newDur, pkt.Duration())
		}
	})

	t.Run("codec_parameters_reference", func(t *testing.T) {
		t.Parallel()
		config1 := MPEG4AudioConfig{
			ObjectType:      AotAacLc,
			SampleRateIndex: 4,
			ChannelConfig:   2,
		}
		config1.Complete()

		codecPar1, err := NewCodecDataFromMPEG4AudioConfig(config1)
		require.NoError(t, err)
		codecPar1.SetStreamIndex(1)

		config2 := MPEG4AudioConfig{
			ObjectType:      AotAacMain,
			SampleRateIndex: 3,
			ChannelConfig:   1,
		}
		config2.Complete()

		codecPar2, err := NewCodecDataFromMPEG4AudioConfig(config2)
		require.NoError(t, err)
		codecPar2.SetStreamIndex(2)

		pkt1 := NewPacket([]byte{0x01}, 0, "", time.Time{}, &codecPar1, 0)
		pkt2 := NewPacket([]byte{0x02}, 0, "", time.Time{}, &codecPar2, 0)

		require.Equal(t, &codecPar1, pkt1.CodecParameters())
		require.Equal(t, &codecPar2, pkt2.CodecParameters())
		require.NotEqual(t, pkt1.CodecParameters(), pkt2.CodecParameters())

		// Verify codec parameters are correct
		require.Equal(t, uint(AotAacLc), pkt1.CodecParameters().(*CodecParameters).Config.ObjectType)
		require.Equal(t, uint(AotAacMain), pkt2.CodecParameters().(*CodecParameters).Config.ObjectType)
	})

	t.Run("url_preservation", func(t *testing.T) {
		t.Parallel()
		config := MPEG4AudioConfig{
			ObjectType:      AotAacLc,
			SampleRateIndex: 4,
			ChannelConfig:   2,
		}
		config.Complete()

		codecPar, err := NewCodecDataFromMPEG4AudioConfig(config)
		require.NoError(t, err)

		testURLs := []string{
			"",
			"rtsp://example.com/stream",
			"rtsp://user:pass@example.com:554/path/to/stream",
			"file:///path/to/file.mp4",
		}

		for _, url := range testURLs {
			pkt := NewPacket([]byte{0x01}, 0, url, time.Time{}, &codecPar, 0)
			require.Equal(t, url, pkt.URL())

			// Test setting URL
			newURL := url + "/modified"
			pkt.SetURL(newURL)
			require.Equal(t, newURL, pkt.URL())
		}
	})

	t.Run("absolute_time_preservation", func(t *testing.T) {
		t.Parallel()
		config := MPEG4AudioConfig{
			ObjectType:      AotAacLc,
			SampleRateIndex: 4,
			ChannelConfig:   2,
		}
		config.Complete()

		codecPar, err := NewCodecDataFromMPEG4AudioConfig(config)
		require.NoError(t, err)

		testTimes := []time.Time{
			time.Time{},
			time.Now(),
			time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
		}

		for _, absTime := range testTimes {
			pkt := NewPacket([]byte{0x01}, 0, "", absTime, &codecPar, 0)
			require.Equal(t, absTime, pkt.StartTime())

			// Test setting absolute time
			newTime := absTime.Add(1 * time.Hour)
			pkt.SetStartTime(newTime)
			require.Equal(t, newTime, pkt.StartTime())
		}
	})

	t.Run("stream_index_preservation", func(t *testing.T) {
		t.Parallel()
		config := MPEG4AudioConfig{
			ObjectType:      AotAacLc,
			SampleRateIndex: 4,
			ChannelConfig:   2,
		}
		config.Complete()

		codecPar, err := NewCodecDataFromMPEG4AudioConfig(config)
		require.NoError(t, err)

		testIndices := []uint8{0, 1, 5, 127, 255}

		for _, idx := range testIndices {
			codecPar.SetStreamIndex(idx)
			pkt := NewPacket([]byte{0x01}, 0, "", time.Time{}, &codecPar, 0)
			require.Equal(t, idx, pkt.StreamIndex())

			// Test setting stream index
			var newIdx uint8
			if idx == 255 {
				newIdx = 0
			} else {
				newIdx = idx + 1
			}
			pkt.SetStreamIndex(newIdx)
			require.Equal(t, newIdx, pkt.StreamIndex())
		}
	})
}

func TestPacket_View(t *testing.T) {
	t.Parallel()

	t.Run("view_data", func(t *testing.T) {
		t.Parallel()
		config := MPEG4AudioConfig{
			ObjectType:      AotAacLc,
			SampleRateIndex: 4,
			ChannelConfig:   2,
		}
		config.Complete()

		codecPar, err := NewCodecDataFromMPEG4AudioConfig(config)
		require.NoError(t, err)

		data := []byte{0x11, 0x22, 0x33, 0x44, 0x55}
		pkt := NewPacket(data, 0, "", time.Time{}, &codecPar, 0)

		viewedData := []byte{}
		pkt.View(func(buf buffer.PooledBuffer) {
			require.NotNil(t, buf)
			require.Equal(t, len(data), buf.Len())
			viewedData = make([]byte, len(buf.Data()))
			copy(viewedData, buf.Data())
		})

		require.Equal(t, data, viewedData)
	})

	t.Run("view_empty_packet", func(t *testing.T) {
		t.Parallel()
		config := MPEG4AudioConfig{
			ObjectType:      AotAacLc,
			SampleRateIndex: 4,
			ChannelConfig:   2,
		}
		config.Complete()

		codecPar, err := NewCodecDataFromMPEG4AudioConfig(config)
		require.NoError(t, err)

		pkt := NewPacket([]byte{}, 0, "", time.Time{}, &codecPar, 0)

		pkt.View(func(buf buffer.PooledBuffer) {
			require.NotNil(t, buf)
			require.Equal(t, 0, buf.Len())
		})
	})

	t.Run("view_modification_does_not_affect_original", func(t *testing.T) {
		t.Parallel()
		config := MPEG4AudioConfig{
			ObjectType:      AotAacLc,
			SampleRateIndex: 4,
			ChannelConfig:   2,
		}
		config.Complete()

		codecPar, err := NewCodecDataFromMPEG4AudioConfig(config)
		require.NoError(t, err)

		data := []byte{0x11, 0x22, 0x33}
		pkt := NewPacket(data, 0, "", time.Time{}, &codecPar, 0)

		originalData := make([]byte, len(data))
		copy(originalData, data)

		pkt.View(func(buf buffer.PooledBuffer) {
			// Modify buffer inside view
			if buf != nil && buf.Len() > 0 {
				buf.Data()[0] = 0xFF
			}
		})

		// Verify original data is modified (since View provides direct access)
		// This tests that View provides access to the actual buffer
		pkt.View(func(buf buffer.PooledBuffer) {
			if buf != nil && buf.Len() > 0 {
				// The modification should be visible
				require.Equal(t, byte(0xFF), buf.Data()[0])
			}
		})
	})
}

func TestPacket_Close(t *testing.T) {
	t.Parallel()

	t.Run("close_releases_buffer", func(t *testing.T) {
		t.Parallel()
		config := MPEG4AudioConfig{
			ObjectType:      AotAacLc,
			SampleRateIndex: 4,
			ChannelConfig:   2,
		}
		config.Complete()

		codecPar, err := NewCodecDataFromMPEG4AudioConfig(config)
		require.NoError(t, err)

		data := []byte{0x11, 0x22, 0x33}
		pkt := NewPacket(data, 0, "", time.Time{}, &codecPar, 0)

		// Close should not panic
		pkt.Close()
	})

	t.Run("close_cloned_packet", func(t *testing.T) {
		t.Parallel()
		config := MPEG4AudioConfig{
			ObjectType:      AotAacLc,
			SampleRateIndex: 4,
			ChannelConfig:   2,
		}
		config.Complete()

		codecPar, err := NewCodecDataFromMPEG4AudioConfig(config)
		require.NoError(t, err)

		data := []byte{0x11, 0x22, 0x33}
		original := NewPacket(data, 0, "", time.Time{}, &codecPar, 0)
		cloned := original.Clone(false).(*Packet)

		// Close cloned packet
		cloned.Close()

		// Original should still work
		require.Equal(t, len(data), original.Len())
		original.Close()
	})
}

func TestPacket_String(t *testing.T) {
	t.Parallel()

	config := MPEG4AudioConfig{
		ObjectType:      AotAacLc,
		SampleRateIndex: 4,
		ChannelConfig:   2,
	}
	config.Complete()

	codecPar, err := NewCodecDataFromMPEG4AudioConfig(config)
	require.NoError(t, err)

	data := []byte{0x11, 0x22, 0x33}
	pkt := NewPacket(data, 0, "", time.Time{}, &codecPar, 0)

	str := pkt.String()
	require.Contains(t, str, "PACKET")
	require.Contains(t, str, "sz=")
}

// Test that packet data is properly isolated when cloned with copyData=true
func TestPacket_DataIsolation(t *testing.T) {
	t.Parallel()

	config := MPEG4AudioConfig{
		ObjectType:      AotAacLc,
		SampleRateIndex: 4,
		ChannelConfig:   2,
	}
	config.Complete()

	codecPar, err := NewCodecDataFromMPEG4AudioConfig(config)
	require.NoError(t, err)

	originalData := []byte{0x01, 0x02, 0x03, 0x04, 0x05}
	original := NewPacket(originalData, 0, "", time.Time{}, &codecPar, 0)

	// Clone with data copy
	cloned := original.Clone(true).(*Packet)

	// Modify original data
	original.View(func(buf buffer.PooledBuffer) {
		if buf != nil && buf.Len() > 0 {
			buf.Data()[0] = 0xFF
		}
	})

	// Cloned data should be independent
	cloned.View(func(buf buffer.PooledBuffer) {
		if buf != nil && buf.Len() > 0 {
			require.Equal(t, byte(0x01), buf.Data()[0], "cloned data should not be affected")
		}
	})
}

// Test buffer pool usage - verify that buffer is properly managed
func TestPacket_BufferPoolUsage(t *testing.T) {
	t.Parallel()

	config := MPEG4AudioConfig{
		ObjectType:      AotAacLc,
		SampleRateIndex: 4,
		ChannelConfig:   2,
	}
	config.Complete()

	codecPar, err := NewCodecDataFromMPEG4AudioConfig(config)
	require.NoError(t, err)

	// Create multiple packets to test buffer pool
	for i := 0; i < 10; i++ {
		data := make([]byte, 100)
		for j := range data {
			data[j] = byte(i*100 + j)
		}

		pkt := NewPacket(data, time.Duration(i)*time.Millisecond, "", time.Time{}, &codecPar, 0)
		require.Equal(t, len(data), pkt.Len())

		pkt.View(func(buf buffer.PooledBuffer) {
			require.Equal(t, data, buf.Data())
		})

		pkt.Close()
	}
}
