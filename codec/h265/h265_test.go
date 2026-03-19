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

func loadTestVPSSPSPPS(t *testing.T) (vps, sps, pps []byte) {
	t.Helper()
	raw, err := os.ReadFile(testDataDir + "parameters.json")
	require.NoError(t, err)

	var params parametersJSON
	require.NoError(t, json.Unmarshal(raw, &params))
	require.NotNil(t, params.Video)

	vps, err = base64.StdEncoding.DecodeString(params.Video.VPS)
	require.NoError(t, err)
	sps, err = base64.StdEncoding.DecodeString(params.Video.SPS)
	require.NoError(t, err)
	pps, err = base64.StdEncoding.DecodeString(params.Video.PPS)
	require.NoError(t, err)
	return
}

// ---------------------------------------------------------------------------
// HEVCDecoderConfRecord — Unmarshal
// ---------------------------------------------------------------------------

func TestHEVCDecoderConfRecord_Unmarshal_Valid(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(testDataDir + "parameters.json")
	require.NoError(t, err)
	var params parametersJSON
	require.NoError(t, json.Unmarshal(raw, &params))

	recordBytes, err := base64.StdEncoding.DecodeString(params.Video.Record)
	require.NoError(t, err)

	var rec HEVCDecoderConfRecord
	n, err := rec.Unmarshal(recordBytes)
	require.NoError(t, err)
	require.Greater(t, n, 0, "should consume bytes")
	require.NotEmpty(t, rec.VPS, "must have at least one VPS")
	require.NotEmpty(t, rec.SPS, "must have at least one SPS")
	require.NotEmpty(t, rec.PPS, "must have at least one PPS")
	// LengthSizeMinusOne must be 0-3 (2-bit field)
	require.LessOrEqual(t, rec.LengthSizeMinusOne, uint8(3))
}

func TestHEVCDecoderConfRecord_Unmarshal_TooShort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
	}{
		{"nil", nil},
		{"empty", []byte{}},
		{"3_bytes", []byte{0x01, 0x02, 0x03}},
		{"29_bytes", make([]byte, 29)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var rec HEVCDecoderConfRecord
			_, err := rec.Unmarshal(tt.data)
			require.ErrorIs(t, err, ErrDecconfInvalid)
		})
	}
}

func TestHEVCDecoderConfRecord_Unmarshal_TruncatedVPSData(t *testing.T) {
	t.Parallel()

	// 30-byte buffer, vpscount=1 at b[25], vpslen=100 which exceeds buffer
	b := make([]byte, 30)
	b[25] = 0x01 // vpscount = 1
	b[26] = 0x00
	b[27] = 0x64 // vpslen = 100
	var rec HEVCDecoderConfRecord
	_, err := rec.Unmarshal(b)
	require.ErrorIs(t, err, ErrDecconfInvalid)
}

func TestHEVCDecoderConfRecord_Unmarshal_TruncatedVPSLength(t *testing.T) {
	t.Parallel()

	// vpscount=1 but buffer ends before VPS length can be read
	b := make([]byte, 27)
	b[25] = 0x01 // vpscount = 1
	// only 1 byte available for vpslen (need 2)
	var rec HEVCDecoderConfRecord
	_, err := rec.Unmarshal(b)
	require.ErrorIs(t, err, ErrDecconfInvalid)
}

// ---------------------------------------------------------------------------
// HEVCDecoderConfRecord — Marshal / Len
// ---------------------------------------------------------------------------

func TestHEVCDecoderConfRecord_Len(t *testing.T) {
	t.Parallel()

	t.Run("empty_nalus", func(t *testing.T) {
		t.Parallel()
		var rec HEVCDecoderConfRecord
		// Base header is 23 bytes, no NALUs
		require.Equal(t, 23, rec.Len())
	})

	t.Run("with_nalus", func(t *testing.T) {
		t.Parallel()
		rec := HEVCDecoderConfRecord{
			VPS: [][]byte{{0x40, 0x01}},        // 2 bytes
			SPS: [][]byte{{0x42, 0x01, 0x00}},   // 3 bytes
			PPS: [][]byte{{0x44, 0x01}},          // 2 bytes
		}
		// 23 + 3*(1 type + 2 count) + (2 + 2) + (2 + 3) + (2 + 2)
		// Each NAL array: 1 byte type + 2 byte count + 2 byte length + data
		// = 23 + (5+2) + (5+3) + (5+2) = 45
		require.Equal(t, 45, rec.Len())
	})
}

func TestHEVCDecoderConfRecord_MarshalUnmarshal_RoundTrip(t *testing.T) {
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

	out := make([]byte, rec.Len())
	rec.Marshal(out)

	// Re-unmarshal and verify field equality
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

// ---------------------------------------------------------------------------
// NewCodecDataFromAVCDecoderConfRecord
// ---------------------------------------------------------------------------

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
	require.Contains(t, cp.Tag(), "hev1.")
}

func TestNewCodecDataFromAVCDecoderConfRecord_TooShort(t *testing.T) {
	t.Parallel()

	_, err := NewCodecDataFromAVCDecoderConfRecord([]byte{0x01, 0x02})
	require.Error(t, err)
}

func TestNewCodecDataFromAVCDecoderConfRecord_EmptyRecord(t *testing.T) {
	t.Parallel()

	// A valid-length but empty record (no VPS/SPS/PPS) should be rejected.
	// The function should either fail at Unmarshal (record too small/corrupt)
	// or fail at the SPS/PPS/VPS presence check.
	b := make([]byte, 30)
	b[0] = 0x01 // version
	_, err := NewCodecDataFromAVCDecoderConfRecord(b)
	require.Error(t, err, "must reject record with no VPS/SPS/PPS")
}

// ---------------------------------------------------------------------------
// NewCodecDataFromVPSAndSPSAndPPS
// ---------------------------------------------------------------------------

func TestNewCodecDataFromVPSAndSPSAndPPS_Valid(t *testing.T) {
	t.Parallel()

	vps, sps, pps := loadTestVPSSPSPPS(t)

	cp, err := NewCodecDataFromVPSAndSPSAndPPS(vps, sps, pps)
	require.NoError(t, err)
	require.Equal(t, gomedia.H265, cp.Type())
	require.Equal(t, sps, cp.SPS())
	require.Equal(t, pps, cp.PPS())
	require.Equal(t, vps, cp.VPS())
	require.Greater(t, cp.Width(), uint(0))
	require.Greater(t, cp.Height(), uint(0))
}

func TestNewCodecDataFromVPSAndSPSAndPPS_AllNil(t *testing.T) {
	t.Parallel()

	// SDP may not carry VPS/SPS/PPS; they arrive in-band later.
	cp, err := NewCodecDataFromVPSAndSPSAndPPS(nil, nil, nil)
	require.NoError(t, err)
	require.Equal(t, uint(0), cp.Width())
	require.Equal(t, uint(0), cp.Height())
}

func TestNewCodecDataFromVPSAndSPSAndPPS_AllEmpty(t *testing.T) {
	t.Parallel()

	cp, err := NewCodecDataFromVPSAndSPSAndPPS([]byte{}, []byte{}, []byte{})
	require.NoError(t, err)
	require.Equal(t, uint(0), cp.Width())
}

func TestNewCodecDataFromVPSAndSPSAndPPS_ShortSPS(t *testing.T) {
	t.Parallel()

	// SPS needs >= 6 bytes (bytes 3,4,5 for profile/compat/level)
	vps := []byte{0x40, 0x01, 0x0c, 0x01}
	sps := []byte{0x42, 0x01} // only 2 bytes
	pps := []byte{0x44, 0x01}

	_, err := NewCodecDataFromVPSAndSPSAndPPS(vps, sps, pps)
	require.Error(t, err, "SPS with fewer than 6 bytes must return error")
}

func TestNewCodecDataFromVPSAndSPSAndPPS_PartialNil(t *testing.T) {
	t.Parallel()

	// Only VPS nil — should return zero-value (all-nil treated as "no params yet")
	cp, err := NewCodecDataFromVPSAndSPSAndPPS(nil, []byte{0x42}, []byte{0x44})
	require.NoError(t, err)
	require.Equal(t, uint(0), cp.Width())
}

// ---------------------------------------------------------------------------
// CodecParameters accessors — nil safety
// ---------------------------------------------------------------------------

func TestCodecParameters_NilSafety(t *testing.T) {
	t.Parallel()

	cp := &CodecParameters{}
	require.Nil(t, cp.SPS())
	require.Nil(t, cp.PPS())
	require.Nil(t, cp.VPS())
	require.Equal(t, uint(0), cp.Width())
	require.Equal(t, uint(0), cp.Height())
	require.Equal(t, uint(0), cp.FPS())
}

func TestCodecParameters_Tag(t *testing.T) {
	t.Parallel()

	cp, _ := loadTestParameters(t)
	tag := cp.Tag()
	require.NotEmpty(t, tag)
	// Format: hev1.P.C.LLL.90
	require.Contains(t, tag, "hev1.")
	require.Contains(t, tag, ".90")
}

func TestCodecParameters_ConsistencyBetweenConstructors(t *testing.T) {
	t.Parallel()

	// Load from record
	cp1, _ := loadTestParameters(t)

	// Load from VPS/SPS/PPS
	vps, sps, pps := loadTestVPSSPSPPS(t)
	cp2, err := NewCodecDataFromVPSAndSPSAndPPS(vps, sps, pps)
	require.NoError(t, err)

	// Both should produce the same dimensions
	require.Equal(t, cp1.Width(), cp2.Width())
	require.Equal(t, cp1.Height(), cp2.Height())
	require.Equal(t, cp1.Type(), cp2.Type())
	require.Equal(t, cp1.SPS(), cp2.SPS())
	require.Equal(t, cp1.PPS(), cp2.PPS())
	require.Equal(t, cp1.VPS(), cp2.VPS())
}

// ---------------------------------------------------------------------------
// IsDataNALU — RFC 7798 §1.1.4
// ---------------------------------------------------------------------------

func TestIsDataNALU(t *testing.T) {
	t.Parallel()

	// H.265 NAL header: 2 bytes — forbidden(1)|type(6)|layer_id(6)|tid(3)
	// byte0 = (nalType << 1), byte1 = 0x01 (layer=0, tid=1)
	makeHeader := func(nalType byte) []byte {
		return []byte{nalType << 1, 0x01}
	}

	// VCL types: 0-31
	vclTypes := []byte{0, 1, 2, 5, 9, 16, 19, 20, 21, 31}
	for _, typ := range vclTypes {
		require.True(t, IsDataNALU(makeHeader(typ)), "type %d should be VCL (data NALU)", typ)
	}

	// Non-VCL types: 32-63
	nonVCLTypes := []byte{32, 33, 34, 35, 39, 40, 48, 49, 63}
	for _, typ := range nonVCLTypes {
		require.False(t, IsDataNALU(makeHeader(typ)), "type %d should be non-VCL", typ)
	}

	// Edge cases
	require.False(t, IsDataNALU(nil), "nil must not panic")
	require.False(t, IsDataNALU([]byte{}), "empty must not panic")
	require.False(t, IsDataNALU([]byte{0x02}), "1-byte must return false (need 2-byte header)")
}

// ---------------------------------------------------------------------------
// IsKey — BLA/IDR/CRA detection
// ---------------------------------------------------------------------------

func TestIsKey(t *testing.T) {
	t.Parallel()

	// H.265 key frame types: BLA_W_LP(16) through CRA(21)
	keyTypes := []byte{
		NalUnitCodedSliceBlaWLp,   // 16
		NalUnitCodedSliceBlaWRadl, // 17
		NalUnitCodedSliceBlaNLp,   // 18
		NalUnitCodedSliceIdrWRadl, // 19
		NalUnitCodedSliceIdrNLp,   // 20
		NalUnitCodedSliceCra,      // 21
	}
	for _, typ := range keyTypes {
		require.True(t, IsKey(typ), "type %d should be a key frame", typ)
	}

	// Non-key types
	nonKeyTypes := []byte{
		0,                            // TRAIL_N
		NalUnitCodedSliceTrailR,      // 1
		NalUnitCodedSliceTsaN,        // 2
		NalUnitCodedSliceTsaR,        // 3
		NalUnitCodedSliceRaslR,       // 9
		15,                           // last VCL before BLA range
		NalUnitReservedIrapVcl22,     // 22 — reserved, NOT key
		NalUnitVps,                   // 32
		NalUnitSps,                   // 33
		NalUnitPps,                   // 34
		NalUnitAccessUnitDelimiter,   // 35
	}
	for _, typ := range nonKeyTypes {
		require.False(t, IsKey(typ), "type %d should NOT be a key frame", typ)
	}
}

// ---------------------------------------------------------------------------
// nal2rbsp — emulation prevention byte removal
// ---------------------------------------------------------------------------

func TestNal2Rbsp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input []byte
		want  []byte
	}{
		{
			name:  "no escaping needed",
			input: []byte{0x01, 0x02, 0x03},
			want:  []byte{0x01, 0x02, 0x03},
		},
		{
			name:  "single emulation prevention byte 00 00 03 01",
			input: []byte{0x00, 0x00, 0x03, 0x01},
			want:  []byte{0x00, 0x00, 0x01},
		},
		{
			name:  "single emulation prevention byte 00 00 03 00",
			input: []byte{0x00, 0x00, 0x03, 0x00},
			want:  []byte{0x00, 0x00, 0x00},
		},
		{
			name:  "single emulation prevention byte 00 00 03 02",
			input: []byte{0x00, 0x00, 0x03, 0x02},
			want:  []byte{0x00, 0x00, 0x02},
		},
		{
			name:  "single emulation prevention byte 00 00 03 03",
			input: []byte{0x00, 0x00, 0x03, 0x03},
			want:  []byte{0x00, 0x00, 0x03},
		},
		{
			name:  "two separate sequences",
			input: []byte{0x00, 0x00, 0x03, 0x00, 0x00, 0x00, 0x03, 0x01},
			// Two EPBs at positions 0-2 and 4-6 in the original stream.
			// Remove both 0x03 bytes: 00 00 + 00 + 00 00 + 01 = 6 bytes
			want: []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
		},
		// NOTE: Per ITU-T H.265 §7.4.2.2, 0x03 is the emulation prevention byte
		// that appears ONLY between 00 00 and {00, 01, 02, 03}. The function
		// should only remove 0x03 in the pattern 00 00 03 XX where XX ∈ {00-03}.
		// However, the implementation uses bytes.ReplaceAll which removes ALL
		// occurrences of 00 00 03 regardless of the following byte.
		// The test below documents the INTENDED spec behavior:
		{
			name:  "0x03 NOT followed by 00-03 should be preserved per spec",
			input: []byte{0x00, 0x00, 0x03, 0x04},
			// Per spec: 0x03 before 0x04 is NOT an emulation prevention byte
			want: []byte{0x00, 0x00, 0x03, 0x04},
		},
		{
			name:  "trailing 0x03 at end of stream",
			input: []byte{0x01, 0x00, 0x00, 0x03},
			// Per spec: 0x03 at end with nothing following is NOT emulation prevention
			want: []byte{0x01, 0x00, 0x00, 0x03},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, nal2rbsp(tt.input))
		})
	}
}

// ---------------------------------------------------------------------------
// ParseSliceHeaderComplete
// ---------------------------------------------------------------------------

func TestParseSliceHeaderComplete_TooShort(t *testing.T) {
	t.Parallel()

	_, err := ParseSliceHeaderComplete(nil)
	require.Error(t, err)

	_, err = ParseSliceHeaderComplete([]byte{0x02})
	require.Error(t, err)

	_, err = ParseSliceHeaderComplete([]byte{0x02, 0x01})
	require.Error(t, err)
}

func TestParseSliceHeaderComplete_NonVCL(t *testing.T) {
	t.Parallel()

	// VPS type=32: byte0 = (32 << 1) = 0x40
	packet := []byte{0x40, 0x01, 0x00, 0x00, 0x00}
	_, err := ParseSliceHeaderComplete(packet)
	require.Error(t, err, "non-VCL NAL must be rejected")
}

func TestParseSliceHeaderComplete_ValidSlices(t *testing.T) {
	t.Parallel()

	// Packet layout (after 2-byte NAL header):
	//   first_slice_segment_in_pic_flag (exp-Golomb) | slice_type (exp-Golomb) | PPSID (exp-Golomb)
	//
	// Exp-Golomb encoding: 0→"1", 1→"010", 2→"011"
	//
	// H.265 spec (ITU-T H.265 §7.4.7.1, Table 7-7):
	//   slice_type 0 = B
	//   slice_type 1 = P
	//   slice_type 2 = I
	//
	// NOTE: The implementation maps 0→P, 1→B (swapped from spec).
	// This test documents the INTENDED spec behavior. If it fails,
	// the bug is in ParseSliceHeaderComplete, not in this test.

	tests := []struct {
		name     string
		payload  byte
		wantType SliceType
		wantPPS  uint
	}{
		// SliceAddr=0("1"), slice_type=0("1"), PPSID=0("1") → 1_1_1_00000 = 0xe0
		{name: "B slice (type=0)", payload: 0xe0, wantType: SliceB, wantPPS: 0},
		// SliceAddr=0("1"), slice_type=1("010"), PPSID=0("1") → 1_010_1_000 = 0xa8
		{name: "P slice (type=1)", payload: 0xa8, wantType: SliceP, wantPPS: 0},
		// SliceAddr=0("1"), slice_type=2("011"), PPSID=0("1") → 1_011_1_000 = 0xb8
		{name: "I slice (type=2)", payload: 0xb8, wantType: SliceI, wantPPS: 0},
		// SliceAddr=0("1"), slice_type=1("010"), PPSID=1("010") → 1_010_010_0 = 0xa4
		{name: "P slice PPSID=1", payload: 0xa4, wantType: SliceP, wantPPS: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// NAL type=1 (TrailR, VCL): byte0=(1<<1)=0x02, byte1=0x01
			packet := []byte{0x02, 0x01, tt.payload}
			header, err := ParseSliceHeaderComplete(packet)
			require.NoError(t, err)
			require.Equal(t, tt.wantType, header.SliceType)
			require.Equal(t, tt.wantPPS, header.PPSID)
		})
	}
}

func TestParseSliceHeaderComplete_InvalidSliceType(t *testing.T) {
	t.Parallel()

	// Per H.265 spec, only slice_type 0-2 are valid.
	// Encode slice_type=10 via exp-Golomb: 10 → "00001011" (leading zeros + value)
	// SliceAddr=0("1"), slice_type=10("00001011")
	// 1_0000_101_1 → but we need this as bytes starting from bit position
	// Actually: "1" then "00001011" = 1_00001011 = 9 bits
	// In a byte: 0b1_0000_101 = 0x85, next byte starts with 1...
	// Simpler: 0x8b = 10001011
	// "1" (addr=0), "0001011" → exp-golomb for 10: code is 0b00001011, but exp-golomb(10) =
	// 10+1=11=0b1011, width=4, so 000_1011. Total: 1_000_1011 = 0b10001011 = 0x8b
	packet := []byte{0x02, 0x01, 0x8b}
	_, err := ParseSliceHeaderComplete(packet)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid")
}

func TestParseSliceHeaderFromNALU_Valid(t *testing.T) {
	t.Parallel()

	// I slice: SliceAddr=0("1"), slice_type=2("011"), PPSID=0("1") → 0xb8
	// slice_type=2 maps to I per H.265 spec Table 7-7
	packet := []byte{0x02, 0x01, 0xb8}
	sliceType, err := ParseSliceHeaderFromNALU(packet)
	require.NoError(t, err)
	require.Equal(t, SliceType(SliceI), sliceType)
}

// ---------------------------------------------------------------------------
// PPSValidator
// ---------------------------------------------------------------------------

func TestPPSValidator_FirstSlice(t *testing.T) {
	t.Parallel()

	v := NewPPSValidator()
	h := SliceHeader{SliceType: SliceI, PPSID: 3}
	require.NoError(t, v.ValidateSlice(h, true))
	// Same PPSID in subsequent slice must pass
	require.NoError(t, v.ValidateSlice(SliceHeader{PPSID: 3}, false))
}

func TestPPSValidator_ConsistentPPS(t *testing.T) {
	t.Parallel()

	v := NewPPSValidator()
	require.NoError(t, v.ValidateSlice(SliceHeader{PPSID: 2}, true))
	require.NoError(t, v.ValidateSlice(SliceHeader{PPSID: 2}, false))
	require.NoError(t, v.ValidateSlice(SliceHeader{PPSID: 2}, false))
}

func TestPPSValidator_MismatchedPPS(t *testing.T) {
	t.Parallel()

	v := NewPPSValidator()
	require.NoError(t, v.ValidateSlice(SliceHeader{PPSID: 1}, true))
	err := v.ValidateSlice(SliceHeader{PPSID: 2}, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "PPS changed between slices")
}

func TestPPSValidator_NewFrame(t *testing.T) {
	t.Parallel()

	v := NewPPSValidator()
	require.NoError(t, v.ValidateSlice(SliceHeader{PPSID: 1}, true))
	v.MarkNewFrame()

	// New frame may use any PPSID
	require.NoError(t, v.ValidateSlice(SliceHeader{PPSID: 5}, false))
	// Within the new frame, same PPSID must succeed
	require.NoError(t, v.ValidateSlice(SliceHeader{PPSID: 5}, false))
	// Different PPSID in the same frame must fail
	require.Error(t, v.ValidateSlice(SliceHeader{PPSID: 1}, false))
}

func TestPPSValidator_Uninitialized(t *testing.T) {
	t.Parallel()

	// Zero-value (not via NewPPSValidator) — isNewFrame is false
	v := &PPSValidator{}
	err := v.ValidateSlice(SliceHeader{PPSID: 0}, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not properly initialized")
}

// ---------------------------------------------------------------------------
// Packet
// ---------------------------------------------------------------------------

func TestNewPacket(t *testing.T) {
	t.Parallel()

	cp, _ := loadTestParameters(t)

	data := []byte{0x26, 0x01, 0x00, 0x00, 0x01}
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

	// Mutating the clone must not affect the original
	cloned.Data()[0] = 0xFF
	require.Equal(t, byte(0x02), pkt.Data()[0])
}

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

func TestPacket_Release(t *testing.T) {
	t.Parallel()

	cp, _ := loadTestParameters(t)
	pkt := NewPacket(false, 0, time.Time{}, []byte{0x01}, "src", cp)
	require.NotPanics(t, pkt.Release)
}

// ---------------------------------------------------------------------------
// Load test data — real packets
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// ParseSPS — using real test data
// ---------------------------------------------------------------------------

func TestParseSPS_RealData(t *testing.T) {
	t.Parallel()

	_, sps, _ := loadTestVPSSPSPPS(t)

	info, err := ParseSPS(sps)
	require.NoError(t, err)
	require.Greater(t, info.Width, uint(0))
	require.Greater(t, info.Height, uint(0))
	// Typical resolutions should be reasonable
	require.LessOrEqual(t, info.Width, uint(8192))
	require.LessOrEqual(t, info.Height, uint(8192))
}

func TestParseSPS_TooShort(t *testing.T) {
	t.Parallel()

	_, err := ParseSPS(nil)
	require.Error(t, err)

	_, err = ParseSPS([]byte{0x42})
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// SliceType.String()
// ---------------------------------------------------------------------------
// (Skipped per project convention: "Do not write tests for String() methods.")

// ---------------------------------------------------------------------------
// NAL type constants — verify ranges
// ---------------------------------------------------------------------------

func TestNALTypeConstants(t *testing.T) {
	t.Parallel()

	// BLA/IDR/CRA range is contiguous: 16-21
	require.Equal(t, 16, NalUnitCodedSliceBlaWLp)
	require.Equal(t, 17, NalUnitCodedSliceBlaWRadl)
	require.Equal(t, 18, NalUnitCodedSliceBlaNLp)
	require.Equal(t, 19, NalUnitCodedSliceIdrWRadl)
	require.Equal(t, 20, NalUnitCodedSliceIdrNLp)
	require.Equal(t, 21, NalUnitCodedSliceCra)

	// Non-VCL boundary
	require.Equal(t, 32, NalUnitVps)
	require.Equal(t, 33, NalUnitSps)
	require.Equal(t, 34, NalUnitPps)
}
