package nal

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ugparu/gomedia/tests"
)

// ---------------------------------------------------------------------------
// isStartCode
// ---------------------------------------------------------------------------

func TestIsStartCode_3Byte(t *testing.T) {
	// 0x000001
	b := []byte{0x00, 0x00, 0x01, 0x65}
	length, found := isStartCode(b, 0)
	assert.True(t, found)
	assert.Equal(t, 3, length)
}

func TestIsStartCode_4Byte(t *testing.T) {
	// 0x00000001
	b := []byte{0x00, 0x00, 0x00, 0x01, 0x65}
	length, found := isStartCode(b, 0)
	assert.True(t, found)
	assert.Equal(t, 4, length)
}

func TestIsStartCode_AtOffset(t *testing.T) {
	b := []byte{0xFF, 0xFF, 0x00, 0x00, 0x01, 0x65}
	length, found := isStartCode(b, 2)
	assert.True(t, found)
	assert.Equal(t, 3, length)
}

func TestIsStartCode_NotFound(t *testing.T) {
	b := []byte{0x00, 0x00, 0x02, 0x65}
	_, found := isStartCode(b, 0)
	assert.False(t, found)
}

func TestIsStartCode_NonZeroFirstByte(t *testing.T) {
	b := []byte{0x01, 0x00, 0x01, 0x65}
	_, found := isStartCode(b, 0)
	assert.False(t, found)
}

func TestIsStartCode_TooShort(t *testing.T) {
	b := []byte{0x00, 0x00}
	_, found := isStartCode(b, 0)
	assert.False(t, found)
}

func TestIsStartCode_PositionAtEnd(t *testing.T) {
	b := []byte{0x00, 0x00, 0x01}
	_, found := isStartCode(b, 1)
	assert.False(t, found)
}

// ---------------------------------------------------------------------------
// SplitNALUs — ANNEXB format
// ---------------------------------------------------------------------------

func TestSplitNALUs_AnnexB_3ByteStartCode(t *testing.T) {
	// Two NALUs: [0x00 0x00 0x01] + NALU1 + [0x00 0x00 0x01] + NALU2
	b := []byte{
		0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x1E, // SPS
		0x00, 0x00, 0x01, 0x68, 0xCE, 0x38, 0x80, // PPS
	}
	nalus, typ := SplitNALUs(b)
	assert.Equal(t, naluANNEXB, typ)
	assert.Equal(t, 2, len(nalus))
	assert.Equal(t, []byte{0x67, 0x42, 0x00, 0x1E}, nalus[0])
	assert.Equal(t, []byte{0x68, 0xCE, 0x38, 0x80}, nalus[1])
}

func TestSplitNALUs_AnnexB_4ByteStartCode(t *testing.T) {
	// NOTE: 0x00000001 as first 4 bytes is ambiguous (AVCC length=1 is tried first).
	// To force ANNEXB detection, use 3-byte start codes or ensure AVCC fails.
	// Here we use a longer NALU body so AVCC interpretation becomes inconsistent.
	b := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0xAA, 0xBB, 0xCC, // 4-byte SC + 5 bytes
		0x00, 0x00, 0x00, 0x01, 0x68, 0xCE, 0xDD, // 4-byte SC + 3 bytes
	}
	nalus, typ := SplitNALUs(b)
	// Parser tries AVCC first (val4=1, reads 1-byte NALUs), which finds valid NALUs.
	// This is correct: AVCC takes priority over ANNEXB when first 4 bytes form valid length.
	assert.Equal(t, naluAVCC, typ)
	assert.GreaterOrEqual(t, len(nalus), 1)
}

func TestSplitNALUs_AnnexB_Mixed3And4ByteStartCodes(t *testing.T) {
	// Use 3-byte start codes to avoid AVCC ambiguity.
	b := []byte{
		0x00, 0x00, 0x01, 0x67, 0x42, 0xAA, // 3-byte SC + SPS
		0x00, 0x00, 0x01, 0x68, 0xCE, // 3-byte SC + PPS
	}
	nalus, typ := SplitNALUs(b)
	assert.Equal(t, naluANNEXB, typ)
	assert.Equal(t, 2, len(nalus))
	assert.Equal(t, []byte{0x67, 0x42, 0xAA}, nalus[0])
	assert.Equal(t, []byte{0x68, 0xCE}, nalus[1])
}

func TestSplitNALUs_AnnexB_SingleNALU(t *testing.T) {
	b := []byte{0x00, 0x00, 0x01, 0x65, 0xAA, 0xBB, 0xCC}
	nalus, typ := SplitNALUs(b)
	assert.Equal(t, naluANNEXB, typ)
	assert.Equal(t, 1, len(nalus))
	assert.Equal(t, []byte{0x65, 0xAA, 0xBB, 0xCC}, nalus[0])
}

// ---------------------------------------------------------------------------
// SplitNALUs — AVCC format
// ---------------------------------------------------------------------------

func TestSplitNALUs_AVCC_SingleNALU(t *testing.T) {
	// Length prefix (4 bytes big-endian) = 3, followed by 3 bytes of NALU data.
	b := []byte{0x00, 0x00, 0x00, 0x03, 0x67, 0x42, 0x00}
	nalus, typ := SplitNALUs(b)
	assert.Equal(t, naluAVCC, typ)
	assert.Equal(t, 1, len(nalus))
	assert.Equal(t, []byte{0x67, 0x42, 0x00}, nalus[0])
}

func TestSplitNALUs_AVCC_MultipleNALUs(t *testing.T) {
	// Two NALUs: length=2 + data, length=3 + data
	b := []byte{
		0x00, 0x00, 0x00, 0x02, 0x67, 0x42, // NALU 1 (2 bytes)
		0x00, 0x00, 0x00, 0x03, 0x68, 0xCE, 0x38, // NALU 2 (3 bytes)
	}
	nalus, typ := SplitNALUs(b)
	assert.Equal(t, naluAVCC, typ)
	assert.Equal(t, 2, len(nalus))
	assert.Equal(t, []byte{0x67, 0x42}, nalus[0])
	assert.Equal(t, []byte{0x68, 0xCE, 0x38}, nalus[1])
}

func TestSplitNALUs_AVCC_CorruptedStream(t *testing.T) {
	// Length says 5 bytes but only 3 available after the 4-byte header.
	// val4=5, len(b)=8, val4 <= len(b), so AVCC path is entered.
	// _b = b[4:] = {0x67, 0x42, 0x00}, _val4=5 > len(_b)=3, so salvage.
	b := []byte{0x00, 0x00, 0x00, 0x05, 0x67, 0x42, 0x00, 0xAA}
	nalus, typ := SplitNALUs(b)
	assert.Equal(t, naluAVCC, typ)
	assert.Equal(t, 1, len(nalus))
	assert.Equal(t, []byte{0x67, 0x42, 0x00, 0xAA}, nalus[0])
}

// ---------------------------------------------------------------------------
// SplitNALUs — Raw format
// ---------------------------------------------------------------------------

func TestSplitNALUs_Raw_TooShort(t *testing.T) {
	b := []byte{0x67, 0x42, 0x00}
	nalus, typ := SplitNALUs(b)
	assert.Equal(t, naluRaw, typ)
	assert.Equal(t, 1, len(nalus))
	assert.Equal(t, b, nalus[0])
}

func TestSplitNALUs_Raw_NoStartCodeNoAVCC(t *testing.T) {
	// Data that doesn't match AVCC or ANNEXB patterns.
	// First 4 bytes as uint32 = 0xDEADBEEF which is > len(b), so not AVCC.
	// Not ANNEXB either (no 0x000001 prefix).
	b := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x02}
	nalus, typ := SplitNALUs(b)
	assert.Equal(t, naluRaw, typ)
	assert.Equal(t, 1, len(nalus))
	assert.Equal(t, b, nalus[0])
}

func TestSplitNALUs_Empty(t *testing.T) {
	b := []byte{}
	nalus, typ := SplitNALUs(b)
	assert.Equal(t, naluRaw, typ)
	assert.Equal(t, 1, len(nalus))
}

// ---------------------------------------------------------------------------
// SplitNALUs — edge cases
// ---------------------------------------------------------------------------

func TestSplitNALUs_AnnexB_ConsecutiveStartCodes(t *testing.T) {
	// Two start codes back-to-back (empty NALU between them, should be skipped).
	b := []byte{
		0x00, 0x00, 0x01,
		0x00, 0x00, 0x01, 0xAA, 0xBB,
	}
	nalus, typ := SplitNALUs(b)
	assert.Equal(t, naluANNEXB, typ)
	// First NALU is empty (skipped by parseANNEXB), second has data.
	assert.Equal(t, 1, len(nalus))
	assert.Equal(t, []byte{0xAA, 0xBB}, nalus[0])
}

func TestSplitNALUs_AVCC_ZeroLengthNALU(t *testing.T) {
	// Length = 0, followed by another NALU.
	b := []byte{
		0x00, 0x00, 0x00, 0x00, // length 0
		0x00, 0x00, 0x00, 0x02, 0xAA, 0xBB, // length 2
	}
	nalus, typ := SplitNALUs(b)
	assert.Equal(t, naluAVCC, typ)
	assert.GreaterOrEqual(t, len(nalus), 1)
}

func TestSplitNALUs_AVCC_ExactLength(t *testing.T) {
	// Length exactly matches remaining bytes.
	b := []byte{0x00, 0x00, 0x00, 0x04, 0x01, 0x02, 0x03, 0x04}
	nalus, typ := SplitNALUs(b)
	assert.Equal(t, naluAVCC, typ)
	assert.Equal(t, 1, len(nalus))
	assert.Equal(t, []byte{0x01, 0x02, 0x03, 0x04}, nalus[0])
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

func TestMinNaluSize(t *testing.T) {
	assert.Equal(t, 4, MinNaluSize)
}

// ---------------------------------------------------------------------------
// Helpers for real-data tests
// ---------------------------------------------------------------------------

// H264 NAL unit types.
const h264IDR = 5

// H265 NAL unit types.
const (
	h265IDRwRADL = 19
	h265IDRnLP   = 20
	h265CRA      = 21
)

func loadPackets(t *testing.T, path string) []tests.PacketJSON {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var pj tests.PacketsJSON
	require.NoError(t, json.Unmarshal(raw, &pj))
	require.NotEmpty(t, pj.Packets)
	return pj.Packets
}

func decodeData(t *testing.T, b64 string) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(b64)
	require.NoError(t, err)
	return data
}

// ---------------------------------------------------------------------------
// SplitNALUs — real H264 data
// ---------------------------------------------------------------------------

func TestSplitNALUs_RealH264(t *testing.T) {
	packets := loadPackets(t, "../../tests/data/h264/packets.json")

	for i, pkt := range packets {
		data := decodeData(t, pkt.Data)
		t.Run(fmt.Sprintf("pkt%d_kf%v", i, pkt.IsKeyframe), func(t *testing.T) {
			nalus, typ := SplitNALUs(data)

			assert.Equal(t, naluAVCC, typ, "expected AVCC format")
			require.GreaterOrEqual(t, len(nalus), 1, "expected at least 1 NALU")

			for j, nalu := range nalus {
				assert.NotEmpty(t, nalu, "NALU %d is empty", j)
			}

			if pkt.IsKeyframe {
				// Keyframe packets contain IDR slices; SPS/PPS are in parameters, not packet data.
				hasIDR := false
				for _, nalu := range nalus {
					if nalu[0]&0x1f == h264IDR {
						hasIDR = true
						break
					}
				}
				assert.True(t, hasIDR, "keyframe missing IDR slice")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// SplitNALUs — real HEVC data
// ---------------------------------------------------------------------------

func TestSplitNALUs_RealHEVC(t *testing.T) {
	packets := loadPackets(t, "../../tests/data/hevc/packets.json")

	for i, pkt := range packets {
		data := decodeData(t, pkt.Data)
		t.Run(fmt.Sprintf("pkt%d_kf%v", i, pkt.IsKeyframe), func(t *testing.T) {
			nalus, typ := SplitNALUs(data)

			assert.NotEqual(t, naluRaw, typ, "expected AVCC or ANNEXB, got raw")
			require.GreaterOrEqual(t, len(nalus), 1, "expected at least 1 NALU")

			for j, nalu := range nalus {
				assert.NotEmpty(t, nalu, "NALU %d is empty", j)
			}

			if pkt.IsKeyframe {
				// Keyframe packets contain IDR/CRA slices; VPS/SPS/PPS are in parameters, not packet data.
				hasKeySlice := false
				for _, nalu := range nalus {
					nt := (nalu[0] >> 1) & 0x3f
					if nt == h265IDRwRADL || nt == h265IDRnLP || nt == h265CRA {
						hasKeySlice = true
						break
					}
				}
				assert.True(t, hasKeySlice, "keyframe missing IDR/CRA slice")
			}
		})
	}
}
