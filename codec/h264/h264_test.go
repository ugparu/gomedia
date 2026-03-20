package h264

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/ugparu/gomedia"
)

// JSON structures matching tests/data/h264/ files.
type parametersJSON struct {
	URL   string           `json:"url"`
	Video *videoParamsJSON `json:"video,omitempty"`
}

type videoParamsJSON struct {
	Codec       string `json:"codec"`
	StreamIndex uint8  `json:"stream_index"`
	Record      string `json:"record,omitempty"` // base64-encoded AVCDecoderConfRecord
	SPS         string `json:"sps,omitempty"`    // base64-encoded SPS NALU
	PPS         string `json:"pps,omitempty"`    // base64-encoded PPS NALU
}

type packetJSON struct {
	Codec       string `json:"codec"`
	StreamIndex uint8  `json:"stream_index"`
	TimestampNs int64  `json:"timestamp_ns"`
	DurationNs  int64  `json:"duration_ns"`
	IsKeyframe  bool   `json:"is_keyframe,omitempty"`
	Size        int    `json:"size"`
	Data        string `json:"data"`
}

type packetsJSON struct {
	Packets []packetJSON `json:"packets"`
}

const testDataDir = "../../tests/data/h264/"

func loadTestParameters(t *testing.T) (*CodecParameters, uint8) {
	t.Helper()
	raw, err := os.ReadFile(testDataDir + "parameters.json")
	require.NoError(t, err)

	var params parametersJSON
	require.NoError(t, json.Unmarshal(raw, &params))
	require.NotNil(t, params.Video, "no video parameters in test file")

	recordBytes, err := base64.StdEncoding.DecodeString(params.Video.Record)
	require.NoError(t, err)

	cp, err := NewCodecDataFromAVCDecoderConfRecord(recordBytes)
	require.NoError(t, err)
	cp.SetStreamIndex(params.Video.StreamIndex)
	return &cp, params.Video.StreamIndex
}

// TestNewCodecDataFromSPSAndPPS_ShortSPS verifies that short SPS input returns an error instead of panicking.
func TestNewCodecDataFromSPSAndPPS_ShortSPS(t *testing.T) {
	t.Parallel()

	_, err := NewCodecDataFromSPSAndPPS([]byte{0x67}, []byte{0x68, 0x01})
	require.Error(t, err, "expected error for SPS with fewer than 4 bytes")

	_, err = NewCodecDataFromSPSAndPPS(nil, []byte{0x68})
	require.Error(t, err, "expected error for nil SPS")
}

// TestNewCodecDataFromSPSAndPPS_Valid builds codec params from real SPS/PPS bytes.
func TestNewCodecDataFromSPSAndPPS_Valid(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(testDataDir + "parameters.json")
	require.NoError(t, err)

	var params parametersJSON
	require.NoError(t, json.Unmarshal(raw, &params))
	require.NotNil(t, params.Video)

	sps, err := base64.StdEncoding.DecodeString(params.Video.SPS)
	require.NoError(t, err)
	pps, err := base64.StdEncoding.DecodeString(params.Video.PPS)
	require.NoError(t, err)

	cp, err := NewCodecDataFromSPSAndPPS(sps, pps)
	require.NoError(t, err)
	require.Equal(t, gomedia.H264, cp.Type())
	require.Equal(t, sps, cp.SPS())
	require.Equal(t, pps, cp.PPS())
	require.Greater(t, cp.Width(), uint(0))
	require.Greater(t, cp.Height(), uint(0))
}

// TestNewCodecDataFromHevcDecoderConfRecord_Valid loads the real AVCDecoderConfRecord
// from test data and validates decoded fields.
func TestNewCodecDataFromHevcDecoderConfRecord_Valid(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(testDataDir + "parameters.json")
	require.NoError(t, err)

	var params parametersJSON
	require.NoError(t, json.Unmarshal(raw, &params))
	require.NotNil(t, params.Video)
	require.Equal(t, "H264", params.Video.Codec)

	recordBytes, err := base64.StdEncoding.DecodeString(params.Video.Record)
	require.NoError(t, err)

	cp, err := NewCodecDataFromAVCDecoderConfRecord(recordBytes)
	require.NoError(t, err)
	require.Equal(t, gomedia.H264, cp.Type())
	require.Greater(t, cp.Width(), uint(0))
	require.Greater(t, cp.Height(), uint(0))
	require.NotNil(t, cp.SPS())
	require.NotNil(t, cp.PPS())
	require.NotEmpty(t, cp.Tag())
	require.Contains(t, cp.Tag(), "avc1.")
}

// TestCodecParameters_SPS_PPS_EmptySlices verifies SPS() and PPS() return nil
// when the underlying slices are empty, without panicking.
func TestCodecParameters_SPS_PPS_EmptySlices(t *testing.T) {
	t.Parallel()

	cp := &CodecParameters{}
	require.Nil(t, cp.SPS(), "SPS() on empty RecordInfo must return nil")
	require.Nil(t, cp.PPS(), "PPS() on empty RecordInfo must return nil")
}

// TestAVCDecoderConfRecord_MarshalUnmarshal verifies round-trip marshal/unmarshal.
func TestAVCDecoderConfRecord_MarshalUnmarshal(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(testDataDir + "parameters.json")
	require.NoError(t, err)

	var params parametersJSON
	require.NoError(t, json.Unmarshal(raw, &params))

	recordBytes, err := base64.StdEncoding.DecodeString(params.Video.Record)
	require.NoError(t, err)

	var rec AVCDecoderConfRecord
	n, err := rec.Unmarshal(recordBytes)
	require.NoError(t, err)
	require.Equal(t, len(recordBytes), n)

	// Re-marshal and compare bytes.
	out := make([]byte, rec.Len())
	rec.Marshal(out)
	require.Equal(t, recordBytes, out)
}

// TestAVCDecoderConfRecord_Unmarshal_TooShort verifies that short input returns an error.
func TestAVCDecoderConfRecord_Unmarshal_TooShort(t *testing.T) {
	t.Parallel()

	var rec AVCDecoderConfRecord
	_, err := rec.Unmarshal([]byte{0x01, 0x64})
	require.ErrorIs(t, err, ErrDecconfInvalid)
}

// TestNewPacket verifies that NewPacket stores all fields correctly.
func TestNewPacket(t *testing.T) {
	t.Parallel()

	cp, _ := loadTestParameters(t)

	data := []byte{0x00, 0x00, 0x00, 0x01, 0x65}
	ts := 500 * time.Millisecond
	absTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	sourceID := "rtsp://camera/stream"

	pkt := NewPacket(true, ts, absTime, data, sourceID, cp)
	require.NotNil(t, pkt)
	require.Equal(t, cp.StreamIndex(), pkt.StreamIndex())
	require.Equal(t, ts, pkt.Timestamp())
	require.Equal(t, sourceID, pkt.SourceID())
	require.Equal(t, data, pkt.Data())
	require.Equal(t, len(data), pkt.Len())
	require.Equal(t, absTime, pkt.StartTime())
	require.True(t, pkt.IsKeyFrame())
	require.Equal(t, gomedia.H264, pkt.CodecParameters().Type())
}

// TestPacket_Clone_CopyData verifies Clone(true) produces an independent copy.
func TestPacket_Clone_CopyData(t *testing.T) {
	t.Parallel()

	cp, _ := loadTestParameters(t)

	original := []byte{0x00, 0x00, 0x00, 0x01, 0x41, 0xAA}
	pkt := NewPacket(false, 100*time.Millisecond, time.Time{}, original, "src", cp)

	cloned, ok := pkt.Clone(true).(*Packet)
	require.True(t, ok)
	require.Equal(t, pkt.Timestamp(), cloned.Timestamp())
	require.Equal(t, pkt.StreamIndex(), cloned.StreamIndex())
	require.Equal(t, pkt.Data(), cloned.Data())
	require.False(t, cloned.IsKeyFrame())

	// Mutating the clone must not affect the original.
	cloned.Data()[0] = 0xFF
	require.Equal(t, byte(0x00), pkt.Data()[0], "original buffer must be unaffected")
}

// TestPacket_Clone_SharedData verifies Clone(false) shares the underlying buffer.
func TestPacket_Clone_SharedData(t *testing.T) {
	t.Parallel()

	cp, _ := loadTestParameters(t)

	data := []byte{0x00, 0x00, 0x00, 0x01, 0x67}
	pkt := NewPacket(true, 0, time.Time{}, data, "src", cp)

	cloned, ok := pkt.Clone(false).(*Packet)
	require.True(t, ok)
	require.Equal(t, pkt.Data(), cloned.Data())

	pkt.Release()
	cloned.Release()
}

// TestPacket_Release verifies Release is safe for heap-backed packets.
func TestPacket_Release(t *testing.T) {
	t.Parallel()

	cp, _ := loadTestParameters(t)
	pkt := NewPacket(false, 0, time.Time{}, []byte{0x01}, "src", cp)
	require.NotPanics(t, pkt.Release)
}

// TestLoadPacketsFromFile loads real packets and wraps each in an h264.Packet.
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
		pkt := NewPacket(entry.IsKeyframe, ts, absBase, data, "test", cp)

		require.Equal(t, streamIdx, pkt.StreamIndex(), "packet %d", i)
		require.Equal(t, ts, pkt.Timestamp(), "packet %d", i)
		require.Equal(t, entry.IsKeyframe, pkt.IsKeyFrame(), "packet %d", i)
		require.Equal(t, entry.Size, pkt.Len(), "packet %d", i)
		require.Equal(t, gomedia.H264, pkt.CodecParameters().Type(), "packet %d", i)
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

	limit := min(10, len(pkts.Packets))
	for i, entry := range pkts.Packets[:limit] {
		data, _ := base64.StdEncoding.DecodeString(entry.Data)
		ts := time.Duration(entry.TimestampNs)
		pkt := NewPacket(entry.IsKeyframe, ts, time.Time{}, data, "src", cp)

		cloned, ok := pkt.Clone(true).(*Packet)
		require.True(t, ok, "packet %d: Clone(true) type assertion", i)
		require.Equal(t, pkt.Timestamp(), cloned.Timestamp(), "packet %d", i)
		require.Equal(t, pkt.Data(), cloned.Data(), "packet %d", i)
		require.Equal(t, pkt.Len(), cloned.Len(), "packet %d", i)
		require.Equal(t, pkt.IsKeyFrame(), cloned.IsKeyFrame(), "packet %d", i)

		sharedClone, ok2 := pkt.Clone(false).(*Packet)
		require.True(t, ok2, "packet %d: Clone(false) type assertion", i)
		require.Equal(t, pkt.Data(), sharedClone.Data(), "packet %d shared data", i)

		cloned.Release()
		sharedClone.Release()
		pkt.Release()
	}
}

// TestRemoveH264EmulationBytes verifies removal of H.264 emulation prevention bytes (0x00 0x00 0x03).
func TestRemoveH264EmulationBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input []byte
		want  []byte
	}{
		{
			name:  "no emulation bytes",
			input: []byte{0x67, 0x64, 0x00, 0x1F},
			want:  []byte{0x67, 0x64, 0x00, 0x1F},
		},
		{
			name:  "single emulation prevention byte",
			input: []byte{0x00, 0x00, 0x03, 0x01},
			want:  []byte{0x00, 0x00, 0x01},
		},
		{
			name:  "two emulation prevention sequences",
			input: []byte{0x00, 0x00, 0x03, 0x02, 0x00, 0x00, 0x03, 0x03},
			want:  []byte{0x00, 0x00, 0x02, 0x00, 0x00, 0x03},
		},
		{
			name:  "emulation prevention byte at end",
			input: []byte{0x01, 0x00, 0x00, 0x03},
			want:  []byte{0x01, 0x00, 0x00},
		},
		{
			name:  "empty input",
			input: []byte{},
			want:  []byte{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, removeH264EmulationBytes(tt.input))
		})
	}
}

// TestAVCDecoderConfRecord_Len verifies Len() returns the correct serialized size.
func TestAVCDecoderConfRecord_Len(t *testing.T) {
	t.Parallel()

	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		var rec AVCDecoderConfRecord
		require.Equal(t, 7, rec.Len())
	})

	t.Run("one SPS one PPS", func(t *testing.T) {
		t.Parallel()
		rec := AVCDecoderConfRecord{
			SPS: [][]byte{{0x67, 0x64, 0x00, 0x1f, 0xac}}, // 5 bytes
			PPS: [][]byte{{0x68, 0xee, 0x3c, 0x80}},       // 4 bytes
		}
		// 7 + (2+5) + (2+4) = 20
		require.Equal(t, 20, rec.Len())
	})
}

// TestAVCDecoderConfRecord_MultiSPSPPS verifies marshal/unmarshal round-trip
// for records containing more than one SPS and PPS entry.
func TestAVCDecoderConfRecord_MultiSPSPPS(t *testing.T) {
	t.Parallel()

	orig := AVCDecoderConfRecord{
		AVCProfileIndication: 0x64,
		ProfileCompatibility: 0x00,
		AVCLevelIndication:   0x1f,
		LengthSizeMinusOne:   3,
		SPS: [][]byte{
			{0x67, 0x64, 0x00, 0x1f},
			{0x67, 0x42, 0x00, 0x0a},
		},
		PPS: [][]byte{
			{0x68, 0xee, 0x3c, 0x80},
			{0x68, 0xde, 0x09, 0x60},
		},
	}

	buf := make([]byte, orig.Len())
	orig.Marshal(buf)

	var decoded AVCDecoderConfRecord
	n, err := decoded.Unmarshal(buf)
	require.NoError(t, err)
	require.Equal(t, len(buf), n)
	require.Equal(t, orig.AVCProfileIndication, decoded.AVCProfileIndication)
	require.Equal(t, orig.AVCLevelIndication, decoded.AVCLevelIndication)
	require.Len(t, decoded.SPS, 2)
	require.Len(t, decoded.PPS, 2)
	require.Equal(t, orig.SPS[0], decoded.SPS[0])
	require.Equal(t, orig.SPS[1], decoded.SPS[1])
	require.Equal(t, orig.PPS[0], decoded.PPS[0])
	require.Equal(t, orig.PPS[1], decoded.PPS[1])
}

// TestAVCDecoderConfRecord_Unmarshal_TruncatedMidSPS verifies that a record
// truncated before SPS length can be read returns ErrDecconfInvalid.
func TestAVCDecoderConfRecord_Unmarshal_TruncatedMidSPS(t *testing.T) {
	t.Parallel()

	// spscount=1 (b[5] & 0x1f = 1) but only 7 bytes total; need 8 to read spslen.
	b := []byte{0x01, 0x64, 0x00, 0x1f, 0xff, 0xe1, 0x00}
	var rec AVCDecoderConfRecord
	_, err := rec.Unmarshal(b)
	require.ErrorIs(t, err, ErrDecconfInvalid)
}

// TestAVCDecoderConfRecord_Unmarshal_TruncatedAtPPS verifies that a record
// truncated before PPS data returns ErrDecconfInvalid.
func TestAVCDecoderConfRecord_Unmarshal_TruncatedAtPPS(t *testing.T) {
	t.Parallel()

	// spscount=0 (b[5] & 0x1f = 0), ppscount=1 (b[6]=1) but no PPS length bytes.
	b := []byte{0x01, 0x64, 0x00, 0x1f, 0xff, 0xe0, 0x01}
	var rec AVCDecoderConfRecord
	_, err := rec.Unmarshal(b)
	require.ErrorIs(t, err, ErrDecconfInvalid)
}

// TestTag verifies the Tag format for known SPS data.
func TestTag(t *testing.T) {
	t.Parallel()

	cp, _ := loadTestParameters(t)
	tag := cp.Tag()
	// Tag must be "avc1.PPCCLL" where PP=profile, CC=compat, LL=level (all uppercase hex)
	require.Regexp(t, `^avc1\.[0-9A-F]{6}$`, tag)
}
