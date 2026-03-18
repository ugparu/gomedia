package h265

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/ugparu/gomedia"
)

// JSON structures matching tests/data/hevc/ files.
type parametersJSON struct {
	URL   string          `json:"url"`
	Video *videoParamsJSON `json:"video,omitempty"`
}

type videoParamsJSON struct {
	Codec       string `json:"codec"`
	StreamIndex uint8  `json:"stream_index"`
	Record      string `json:"record,omitempty"`
	SPS         string `json:"sps,omitempty"`
	PPS         string `json:"pps,omitempty"`
	VPS         string `json:"vps,omitempty"`
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

const testDataDir = "../../tests/data/hevc/"

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

// TestNewCodecDataFromAVCDecoderConfRecord_Valid loads real HEVC decoder config
// and validates decoded fields.
func TestNewCodecDataFromAVCDecoderConfRecord_Valid(t *testing.T) {
	t.Parallel()

	cp, _ := loadTestParameters(t)

	require.Equal(t, gomedia.H265, cp.Type())
	require.Greater(t, cp.Width(), uint(0))
	require.Greater(t, cp.Height(), uint(0))
	require.NotNil(t, cp.SPS())
	require.NotNil(t, cp.PPS())
	require.NotNil(t, cp.VPS())
	require.NotEmpty(t, cp.Tag())
}

// TestNewCodecDataFromAVCDecoderConfRecord_TooShort verifies that a short input
// returns an error rather than panicking.
func TestNewCodecDataFromAVCDecoderConfRecord_TooShort(t *testing.T) {
	t.Parallel()

	_, err := NewCodecDataFromAVCDecoderConfRecord([]byte{0x01, 0x02})
	require.Error(t, err)
}

// TestNewCodecDataFromVPSAndSPSAndPPS_Valid builds codec params from
// real individual NAL units.
func TestNewCodecDataFromVPSAndSPSAndPPS_Valid(t *testing.T) {
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
	vps, err := base64.StdEncoding.DecodeString(params.Video.VPS)
	require.NoError(t, err)

	cp, err := NewCodecDataFromVPSAndSPSAndPPS(vps, sps, pps)
	require.NoError(t, err)
	require.Equal(t, gomedia.H265, cp.Type())
	require.Equal(t, sps, cp.SPS())
	require.Equal(t, pps, cp.PPS())
	require.Equal(t, vps, cp.VPS())
	require.Greater(t, cp.Width(), uint(0))
	require.Greater(t, cp.Height(), uint(0))
}

// TestNewCodecDataFromVPSAndSPSAndPPS_Empty verifies that empty inputs return
// a zero CodecParameters with nil error (SDP parameters arrive later in-band).
func TestNewCodecDataFromVPSAndSPSAndPPS_Empty(t *testing.T) {
	t.Parallel()

	cp, err := NewCodecDataFromVPSAndSPSAndPPS(nil, nil, nil)
	require.NoError(t, err, "empty VPS/SPS/PPS must not error (in-band delivery)")
	require.Equal(t, uint(0), cp.Width())
	require.Equal(t, uint(0), cp.Height())
}

// TestNewCodecDataFromVPSAndSPSAndPPS_ShortSPS verifies that a non-empty but
// too-short SPS returns an error.
func TestNewCodecDataFromVPSAndSPSAndPPS_ShortSPS(t *testing.T) {
	t.Parallel()

	vps := []byte{0x40, 0x01}
	sps := []byte{0x42, 0x01} // only 2 bytes, need >= 6
	pps := []byte{0x44, 0x01}

	_, err := NewCodecDataFromVPSAndSPSAndPPS(vps, sps, pps)
	require.Error(t, err, "SPS with fewer than 6 bytes must return error")
}

// TestCodecParameters_NilSafety verifies SPS/PPS/VPS return nil (not []byte{})
// when the underlying slices are empty.
func TestCodecParameters_NilSafety(t *testing.T) {
	t.Parallel()

	cp := &CodecParameters{}
	require.Nil(t, cp.SPS(), "SPS() on empty RecordInfo must return nil")
	require.Nil(t, cp.PPS(), "PPS() on empty RecordInfo must return nil")
	require.Nil(t, cp.VPS(), "VPS() on empty RecordInfo must return nil")
}

// TestHEVCDecoderConfRecord_MarshalUnmarshal verifies round-trip fidelity.
func TestHEVCDecoderConfRecord_MarshalUnmarshal(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(testDataDir + "parameters.json")
	require.NoError(t, err)

	var params parametersJSON
	require.NoError(t, json.Unmarshal(raw, &params))

	recordBytes, err := base64.StdEncoding.DecodeString(params.Video.Record)
	require.NoError(t, err)

	var rec HEVCDecoderConfRecord
	_, err = rec.Unmarshal(recordBytes)
	require.NoError(t, err)
	require.NotEmpty(t, rec.SPS)
	require.NotEmpty(t, rec.PPS)
	require.NotEmpty(t, rec.VPS)

	out := make([]byte, rec.Len())
	rec.Marshal(out)

	// Re-unmarshal the marshaled bytes and verify field equality.
	var rec2 HEVCDecoderConfRecord
	_, err = rec2.Unmarshal(out)
	require.NoError(t, err)
	require.Equal(t, rec.AVCProfileIndication, rec2.AVCProfileIndication)
	require.Equal(t, rec.AVCLevelIndication, rec2.AVCLevelIndication)
	require.Equal(t, rec.ProfileCompatibility, rec2.ProfileCompatibility)
	require.Equal(t, rec.SPS, rec2.SPS)
	require.Equal(t, rec.PPS, rec2.PPS)
	require.Equal(t, rec.VPS, rec2.VPS)
}

// TestHEVCDecoderConfRecord_Unmarshal_TooShort verifies short input returns
// ErrDecconfInvalid.
func TestHEVCDecoderConfRecord_Unmarshal_TooShort(t *testing.T) {
	t.Parallel()

	var rec HEVCDecoderConfRecord
	_, err := rec.Unmarshal([]byte{0x01, 0x02, 0x03})
	require.ErrorIs(t, err, ErrDecconfInvalid)
}

// TestIsDataNALU verifies RFC 7798 §1.1.4 NAL type extraction and VCL range.
func TestIsDataNALU(t *testing.T) {
	t.Parallel()

	// RFC 7798 §1.1.4: nal_unit_type = (byte0 >> 1) & 0x3F; types 0-31 are VCL.
	// H.265 NAL header = 2 bytes: forbidden(1)|type(6)|layer_id(6)|tid(3).
	makeHeader := func(nalType byte) []byte {
		// byte0 = (nalType << 1), byte1 = 0x01 (layer=0, tid=1)
		return []byte{nalType << 1, 0x01}
	}

	vcl := []byte{0, 1, 5, 9, 16, 19, 21, 31}
	for _, typ := range vcl {
		require.True(t, IsDataNALU(makeHeader(typ)), "type %d should be data NALU", typ)
	}

	nonVCL := []byte{32, 33, 34, 35, 48, 49, 63}
	for _, typ := range nonVCL {
		require.False(t, IsDataNALU(makeHeader(typ)), "type %d should NOT be data NALU", typ)
	}

	// Empty and single-byte inputs must not panic.
	require.False(t, IsDataNALU(nil))
	require.False(t, IsDataNALU([]byte{0x02}))
}

// TestIsKey verifies that IDR/BLA/CRA types are identified as keyframes.
func TestIsKey(t *testing.T) {
	t.Parallel()

	// Key types per H.265: BLA_W_LP(16)..CRA(21).
	keyTypes := []byte{
		NalUnitCodedSliceBlaWLp,
		NalUnitCodedSliceBlaWRadl,
		NalUnitCodedSliceBlaNLp,
		NalUnitCodedSliceIdrWRadl,
		NalUnitCodedSliceIdrNLp,
		NalUnitCodedSliceCra,
	}
	for _, typ := range keyTypes {
		require.True(t, IsKey(typ), "type %d should be a key frame", typ)
	}

	nonKeyTypes := []byte{
		NalUnitCodedSliceTrailR,
		NalUnitCodedSliceTsaN,
		NalUnitVps,
		NalUnitSps,
		NalUnitPps,
	}
	for _, typ := range nonKeyTypes {
		require.False(t, IsKey(typ), "type %d should NOT be a key frame", typ)
	}
}

// TestParseSliceHeaderComplete_NonVCL verifies that non-VCL NAL types
// (type >= 32) are rejected per RFC 7798 §1.1.4.
func TestParseSliceHeaderComplete_NonVCL(t *testing.T) {
	t.Parallel()

	// VPS NAL type 32: byte0 = (32 << 1) = 0x40
	packet := []byte{0x40, 0x01, 0x00, 0x00, 0x00}
	_, err := ParseSliceHeaderComplete(packet)
	require.Error(t, err, "non-VCL NAL must be rejected")
	require.Contains(t, err.Error(), "no slice header")
}

// TestParseSliceHeaderComplete_TooShort verifies that packets shorter than
// 3 bytes (2-byte header + at least 1 payload) are rejected.
func TestParseSliceHeaderComplete_TooShort(t *testing.T) {
	t.Parallel()

	_, err := ParseSliceHeaderComplete([]byte{0x02, 0x01})
	require.Error(t, err)

	_, err = ParseSliceHeaderComplete(nil)
	require.Error(t, err)
}

// TestTag verifies the Tag format: "hev1.P.C.LLL.90".
func TestTag(t *testing.T) {
	t.Parallel()

	cp, _ := loadTestParameters(t)
	tag := cp.Tag()
	require.NotEmpty(t, tag)
	require.Contains(t, tag, "hev1.")
	require.Contains(t, tag, ".90")
}

// TestNewPacket verifies that NewPacket stores all fields correctly.
func TestNewPacket(t *testing.T) {
	t.Parallel()

	cp, _ := loadTestParameters(t)

	data := []byte{0x26, 0x01, 0x00, 0x00, 0x01} // IDR_W_RADL NAL
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
	require.Equal(t, gomedia.H265, pkt.CodecParameters().Type())
}

// TestPacket_Clone_CopyData verifies Clone(true) produces an independent copy.
func TestPacket_Clone_CopyData(t *testing.T) {
	t.Parallel()

	cp, _ := loadTestParameters(t)

	original := []byte{0x02, 0x01, 0xAA, 0xBB}
	pkt := NewPacket(false, 100*time.Millisecond, time.Time{}, original, "src", cp)

	cloned, ok := pkt.Clone(true).(*Packet)
	require.True(t, ok)
	require.Equal(t, pkt.Timestamp(), cloned.Timestamp())
	require.Equal(t, pkt.StreamIndex(), cloned.StreamIndex())
	require.Equal(t, pkt.Data(), cloned.Data())
	require.False(t, cloned.IsKeyFrame())

	// Mutating the clone must not affect the original.
	cloned.Data()[0] = 0xFF
	require.Equal(t, byte(0x02), pkt.Data()[0], "original must be unaffected")
}

// TestPacket_Clone_SharedData verifies Clone(false) shares the underlying buffer.
func TestPacket_Clone_SharedData(t *testing.T) {
	t.Parallel()

	cp, _ := loadTestParameters(t)

	data := []byte{0x26, 0x01, 0x00}
	pkt := NewPacket(true, 0, time.Time{}, data, "src", cp)

	cloned, ok := pkt.Clone(false).(*Packet)
	require.True(t, ok)
	require.Equal(t, pkt.Data(), cloned.Data())

	pkt.Release()
	cloned.Release()
}

// TestPacket_Release verifies Release does not panic.
func TestPacket_Release(t *testing.T) {
	t.Parallel()

	cp, _ := loadTestParameters(t)
	pkt := NewPacket(false, 0, time.Time{}, []byte{0x01}, "src", cp)
	require.NotPanics(t, pkt.Release)
}

// TestLoadPacketsFromFile loads all real HEVC packets and validates each one.
func TestLoadPacketsFromFile(t *testing.T) {
	t.Parallel()

	cp, streamIdx := loadTestParameters(t)

	pktsRaw, err := os.ReadFile(testDataDir + "packets.json")
	require.NoError(t, err)

	var pkts packetsJSON
	require.NoError(t, json.Unmarshal(pktsRaw, &pkts))
	require.NotEmpty(t, pkts.Packets)

	var keyCount int
	absBase := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, entry := range pkts.Packets {
		data, decErr := base64.StdEncoding.DecodeString(entry.Data)
		require.NoError(t, decErr, "packet %d: base64 decode", i)
		require.Equal(t, entry.Size, len(data), "packet %d: size mismatch", i)

		ts := time.Duration(entry.TimestampNs)
		pkt := NewPacket(entry.IsKeyframe, ts, absBase, data, "test", cp)

		require.Equal(t, streamIdx, pkt.StreamIndex(), "packet %d", i)
		require.Equal(t, ts, pkt.Timestamp(), "packet %d", i)
		require.Equal(t, entry.IsKeyframe, pkt.IsKeyFrame(), "packet %d", i)
		require.Equal(t, entry.Size, pkt.Len(), "packet %d", i)
		require.Equal(t, gomedia.H265, pkt.CodecParameters().Type(), "packet %d", i)

		if entry.IsKeyframe {
			keyCount++
		}
		pkt.Release()
	}
	require.Greater(t, keyCount, 0, "expected at least one keyframe in test data")
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
