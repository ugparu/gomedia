package aac

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/ugparu/gomedia"
)

// JSON structures matching tests/data/aac/ files.
type parametersJSON struct {
	Codec       string `json:"codec"`
	StreamIndex uint8  `json:"stream_index"`
	Config      string `json:"config"` // base64-encoded MPEG4AudioConfig bytes
}

type packetJSON struct {
	Codec       string `json:"codec"`
	StreamIndex uint8  `json:"stream_index"`
	TimestampNs int64  `json:"timestamp_ns"`
	DurationNs  int64  `json:"duration_ns"`
	Size        int    `json:"size"`
	Data        string `json:"data"` // base64-encoded raw AAC payload (no ADTS header)
}

type packetsJSON struct {
	Packets []packetJSON `json:"packets"`
}

const testDataDir = "../../tests/data/aac/"

func loadTestParameters(t *testing.T) (*CodecParameters, uint8) {
	t.Helper()
	raw, err := os.ReadFile(testDataDir + "parameters.json")
	require.NoError(t, err)

	var params parametersJSON
	require.NoError(t, json.Unmarshal(raw, &params))

	configBytes, err := base64.StdEncoding.DecodeString(params.Config)
	require.NoError(t, err)

	cp, err := NewCodecDataFromMPEG4AudioConfigBytes(configBytes)
	require.NoError(t, err)
	cp.SetStreamIndex(params.StreamIndex)
	return &cp, params.StreamIndex
}

// TestNewPacket verifies that NewPacket correctly stores all fields.
func TestNewPacket(t *testing.T) {
	t.Parallel()

	config := MPEG4AudioConfig{
		ObjectType:      AotAacLc,
		SampleRateIndex: 4,
		ChannelConfig:   2,
	}
	config.Complete()

	cp, err := NewCodecDataFromMPEG4AudioConfig(config)
	require.NoError(t, err)
	cp.SetStreamIndex(3)

	data := []byte{0x01, 0x02, 0x03, 0x04}
	ts := 128 * time.Millisecond
	dur := 64 * time.Millisecond
	absTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	sourceID := "rtsp://camera/stream"

	pkt := NewPacket(data, ts, sourceID, absTime, &cp, dur)
	require.NotNil(t, pkt)
	require.Equal(t, uint8(3), pkt.StreamIndex())
	require.Equal(t, ts, pkt.Timestamp())
	require.Equal(t, dur, pkt.Duration())
	require.Equal(t, sourceID, pkt.SourceID())
	require.Equal(t, data, pkt.Data())
	require.Equal(t, len(data), pkt.Len())
	require.Equal(t, absTime, pkt.StartTime())
	require.Equal(t, gomedia.AAC, pkt.CodecParameters().Type())
	require.Equal(t, uint64(44100), pkt.CodecParameters().SampleRate())
	require.Equal(t, uint8(2), pkt.CodecParameters().Channels())
}

// TestPacket_Clone_CopyData verifies Clone(true) produces an independent copy.
func TestPacket_Clone_CopyData(t *testing.T) {
	t.Parallel()

	config := MPEG4AudioConfig{
		ObjectType:      AotAacLc,
		SampleRateIndex: 3, // 48 kHz
		ChannelConfig:   2,
	}
	config.Complete()

	cp, err := NewCodecDataFromMPEG4AudioConfig(config)
	require.NoError(t, err)

	original := []byte{0xAA, 0xBB, 0xCC}
	pkt := NewPacket(original, 64*time.Millisecond, "src", time.Time{}, &cp, 32*time.Millisecond)

	cloned, ok := pkt.Clone(true).(*Packet)
	require.True(t, ok)

	require.Equal(t, pkt.StreamIndex(), cloned.StreamIndex())
	require.Equal(t, pkt.Timestamp(), cloned.Timestamp())
	require.Equal(t, pkt.Duration(), cloned.Duration())
	require.Equal(t, pkt.SourceID(), cloned.SourceID())
	require.Equal(t, pkt.Data(), cloned.Data())

	// Mutating the clone must not affect the original.
	cloned.Data()[0] = 0x00
	require.Equal(t, byte(0xAA), pkt.Data()[0], "original buffer must be unaffected")
}

// TestPacket_Clone_SharedData verifies Clone(false) shares the underlying buffer.
func TestPacket_Clone_SharedData(t *testing.T) {
	t.Parallel()

	config := MPEG4AudioConfig{
		ObjectType:      AotAacLc,
		SampleRateIndex: 4,
		ChannelConfig:   1,
	}
	config.Complete()

	cp, err := NewCodecDataFromMPEG4AudioConfig(config)
	require.NoError(t, err)

	data := []byte{0x11, 0x22, 0x33}
	pkt := NewPacket(data, 0, "src", time.Time{}, &cp, 0)

	cloned, ok := pkt.Clone(false).(*Packet)
	require.True(t, ok)

	require.Equal(t, pkt.StreamIndex(), cloned.StreamIndex())
	require.Equal(t, pkt.Data(), cloned.Data())

	// Heap-backed packets: Release is a no-op (Slot == nil).
	pkt.Release()
	cloned.Release()
}

// TestPacket_Release verifies Release is safe for heap-backed packets.
func TestPacket_Release(t *testing.T) {
	t.Parallel()

	config := MPEG4AudioConfig{
		ObjectType:      AotAacLc,
		SampleRateIndex: 4,
		ChannelConfig:   2,
	}
	cp, err := NewCodecDataFromMPEG4AudioConfig(config)
	require.NoError(t, err)

	pkt := NewPacket([]byte{0x01}, 0, "src", time.Time{}, &cp, 0)
	require.NotPanics(t, pkt.Release)
}

// TestLoadParametersFromFile loads the real test parameters and validates the decoded codec.
func TestLoadParametersFromFile(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(testDataDir + "parameters.json")
	require.NoError(t, err)

	var params parametersJSON
	require.NoError(t, json.Unmarshal(raw, &params))

	require.Equal(t, "AAC", params.Codec)
	require.Equal(t, uint8(1), params.StreamIndex)

	configBytes, err := base64.StdEncoding.DecodeString(params.Config)
	require.NoError(t, err)
	require.NotEmpty(t, configBytes)

	cp, err := NewCodecDataFromMPEG4AudioConfigBytes(configBytes)
	require.NoError(t, err)
	require.Equal(t, gomedia.AAC, cp.Type())
	require.Equal(t, uint(AotAacLc), cp.Config.ObjectType) // AAC-LC
	require.Equal(t, uint64(16000), cp.SampleRate())       // 16 kHz
	require.Equal(t, uint8(1), cp.Channels())              // mono
	require.Equal(t, gomedia.FLTP, cp.SampleFormat())
	require.Equal(t, "mp4a.40.2", cp.Tag())
}

// TestLoadPacketsFromFile loads real packets and wraps each in an aac.Packet.
func TestLoadPacketsFromFile(t *testing.T) {
	t.Parallel()

	cp, streamIdx := loadTestParameters(t)

	pktsRaw, err := os.ReadFile(testDataDir + "packets.json")
	require.NoError(t, err)

	var pkts packetsJSON
	require.NoError(t, json.Unmarshal(pktsRaw, &pkts))
	require.NotEmpty(t, pkts.Packets)

	absBase := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, entry := range pkts.Packets {
		data, decErr := base64.StdEncoding.DecodeString(entry.Data)
		require.NoError(t, decErr, "packet %d: base64 decode", i)
		require.Equal(t, entry.Size, len(data), "packet %d: declared size mismatch", i)

		ts := time.Duration(entry.TimestampNs)
		dur := time.Duration(entry.DurationNs)
		pkt := NewPacket(data, ts, "test", absBase, cp, dur)

		require.Equal(t, streamIdx, pkt.StreamIndex(), "packet %d", i)
		require.Equal(t, ts, pkt.Timestamp(), "packet %d", i)
		require.Equal(t, dur, pkt.Duration(), "packet %d", i)
		require.Equal(t, entry.Size, pkt.Len(), "packet %d", i)
		require.Equal(t, gomedia.AAC, pkt.CodecParameters().Type(), "packet %d", i)
		require.Equal(t, uint64(16000), pkt.CodecParameters().SampleRate(), "packet %d", i)
		require.Equal(t, uint8(1), pkt.CodecParameters().Channels(), "packet %d", i)
		pkt.Release()
	}
}

// TestLoadPackets_CloneRoundtrip verifies Clone on real packet data.
func TestLoadPackets_CloneRoundtrip(t *testing.T) {
	t.Parallel()

	cp, _ := loadTestParameters(t)

	pktsRaw, err := os.ReadFile(testDataDir + "packets.json")
	require.NoError(t, err)

	var pkts packetsJSON
	require.NoError(t, json.Unmarshal(pktsRaw, &pkts))
	require.NotEmpty(t, pkts.Packets)

	// Validate the first 10 packets (or all if fewer) via clone roundtrip.
	limit := min(10, len(pkts.Packets))
	for i, entry := range pkts.Packets[:limit] {
		data, _ := base64.StdEncoding.DecodeString(entry.Data)
		ts := time.Duration(entry.TimestampNs)
		dur := time.Duration(entry.DurationNs)
		pkt := NewPacket(data, ts, "src", time.Time{}, cp, dur)

		cloned, ok := pkt.Clone(true).(*Packet)
		require.True(t, ok, "packet %d: Clone(true) type assertion", i)
		require.Equal(t, pkt.Timestamp(), cloned.Timestamp(), "packet %d", i)
		require.Equal(t, pkt.Duration(), cloned.Duration(), "packet %d", i)
		require.Equal(t, pkt.Data(), cloned.Data(), "packet %d", i)
		require.Equal(t, pkt.Len(), cloned.Len(), "packet %d", i)

		// Shared-buffer clone.
		sharedClone, ok2 := pkt.Clone(false).(*Packet)
		require.True(t, ok2, "packet %d: Clone(false) type assertion", i)
		require.Equal(t, pkt.Data(), sharedClone.Data(), "packet %d shared data", i)

		cloned.Release()
		sharedClone.Release()
		pkt.Release()
	}
}
