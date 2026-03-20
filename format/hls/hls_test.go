//nolint:mnd // Test file contains many magic numbers for expected values
package hls

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/aac"
	"github.com/ugparu/gomedia/codec/h264"
	"github.com/ugparu/gomedia/utils/logger"
)

const testDataDir = "../../tests/data/h264_aac/"

// ---------------------------------------------------------------------------
// Test data helpers
// ---------------------------------------------------------------------------

type parametersJSON struct {
	URL   string          `json:"url"`
	Video *videoParamJSON `json:"video,omitempty"`
	Audio *audioParamJSON `json:"audio,omitempty"`
}

type videoParamJSON struct {
	Codec       string `json:"codec"`
	StreamIndex uint8  `json:"stream_index"`
	Record      string `json:"record,omitempty"`
}

type audioParamJSON struct {
	Codec       string `json:"codec"`
	StreamIndex uint8  `json:"stream_index"`
	Config      string `json:"config,omitempty"`
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

func loadTestCodecPair(t *testing.T) (gomedia.CodecParametersPair, *h264.CodecParameters, *aac.CodecParameters) {
	t.Helper()
	raw, err := os.ReadFile(testDataDir + "parameters.json")
	require.NoError(t, err)

	var params parametersJSON
	require.NoError(t, json.Unmarshal(raw, &params))
	require.NotNil(t, params.Video)
	require.NotNil(t, params.Audio)

	recordBytes, err := base64.StdEncoding.DecodeString(params.Video.Record)
	require.NoError(t, err)
	videoCp, err := h264.NewCodecDataFromHevcDecoderConfRecord(recordBytes)
	require.NoError(t, err)
	videoCp.SetStreamIndex(params.Video.StreamIndex)

	configBytes, err := base64.StdEncoding.DecodeString(params.Audio.Config)
	require.NoError(t, err)
	audioCp, err := aac.NewCodecDataFromMPEG4AudioConfigBytes(configBytes)
	require.NoError(t, err)
	audioCp.SetStreamIndex(params.Audio.StreamIndex)

	pair := gomedia.CodecParametersPair{
		SourceID:             "test",
		VideoCodecParameters: &videoCp,
		AudioCodecParameters: &audioCp,
	}
	return pair, &videoCp, &audioCp
}

func loadTestPackets(t *testing.T, videoCp *h264.CodecParameters, audioCp *aac.CodecParameters, limit int) []gomedia.Packet {
	t.Helper()
	raw, err := os.ReadFile(testDataDir + "packets.json")
	require.NoError(t, err)

	var pkts packetsJSON
	require.NoError(t, json.Unmarshal(raw, &pkts))

	absBase := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	var result []gomedia.Packet
	for i, entry := range pkts.Packets {
		if i >= limit {
			break
		}
		data, decErr := base64.StdEncoding.DecodeString(entry.Data)
		require.NoError(t, decErr)
		ts := time.Duration(entry.TimestampNs)
		dur := time.Duration(entry.DurationNs)
		switch entry.Codec {
		case "H264":
			pkt := h264.NewPacket(entry.IsKeyframe, ts, absBase, data, "test", videoCp)
			pkt.SetDuration(dur)
			result = append(result, pkt)
		case "AAC":
			result = append(result, aac.NewPacket(data, ts, "test", absBase, audioCp, dur))
		default:
			t.Fatalf("unexpected codec: %s", entry.Codec)
		}
	}
	return result
}

func newTestMuxer(t *testing.T, segDuration time.Duration, segCount uint8, opts ...MuxerOption) *muxer {
	t.Helper()
	return NewHLSMuxer(segDuration, segCount, 1.5, logger.Default, opts...).(*muxer)
}

func initMuxer(t *testing.T, segDuration time.Duration, segCount uint8, opts ...MuxerOption) (*muxer, gomedia.CodecParametersPair, *h264.CodecParameters, *aac.CodecParameters) {
	t.Helper()
	mxr := newTestMuxer(t, segDuration, segCount, opts...)
	pair, vCp, aCp := loadTestCodecPair(t)
	require.NoError(t, mxr.Mux(pair))
	return mxr, pair, vCp, aCp
}

// writePacketsUntilSegment writes packets until at least one segment finishes.
// Returns the number of packets written.
func writePacketsUntilSegment(t *testing.T, mxr gomedia.HLSMuxer, packets []gomedia.Packet) int {
	t.Helper()
	for i, pkt := range packets {
		require.NoError(t, mxr.WritePacket(pkt))
		m, err := mxr.GetIndexM3u8(context.Background(), -1, -1)
		require.NoError(t, err)
		if strings.Contains(m, "#EXTINF:") {
			return i + 1
		}
	}
	t.Fatal("no segment completed within available packets")
	return 0
}

// ---------------------------------------------------------------------------
// Mux / initialization tests
// ---------------------------------------------------------------------------

func TestMux_Success(t *testing.T) {
	pair, _, _ := loadTestCodecPair(t)
	mxr := newTestMuxer(t, 2*time.Second, 3)
	err := mxr.Mux(pair)
	require.NoError(t, err)
}

func TestMux_NoCodecData(t *testing.T) {
	mxr := newTestMuxer(t, 2*time.Second, 3)
	err := mxr.Mux(gomedia.CodecParametersPair{})
	require.Error(t, err)
}

func TestMux_VideoOnly(t *testing.T) {
	pair, _, _ := loadTestCodecPair(t)
	pair.AudioCodecParameters = nil
	mxr := newTestMuxer(t, 2*time.Second, 3)
	err := mxr.Mux(pair)
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// WritePacket tests
// ---------------------------------------------------------------------------

func TestWritePacket_NilPacket(t *testing.T) {
	mxr, _, _, _ := initMuxer(t, 2*time.Second, 3)
	defer mxr.Release()
	err := mxr.WritePacket(nil)
	require.Error(t, err)
}

func TestWritePacket_ProducesManifest(t *testing.T) {
	mxr, _, vCp, aCp := initMuxer(t, 2*time.Second, 3)
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 10)

	for _, pkt := range packets {
		require.NoError(t, mxr.WritePacket(pkt))
	}

	m, err := mxr.GetIndexM3u8(context.Background(), -1, -1)
	require.NoError(t, err)
	assert.NotEmpty(t, m)
	assert.Contains(t, m, "#EXTM3U")
	assert.Contains(t, m, "#EXT-X-VERSION:7")
}

func TestWritePacket_FragmentsCreated(t *testing.T) {
	mxr, _, vCp, aCp := initMuxer(t, 2*time.Second, 3)
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 50)

	for _, pkt := range packets {
		require.NoError(t, mxr.WritePacket(pkt))
	}

	m, err := mxr.GetIndexM3u8(context.Background(), -1, -1)
	require.NoError(t, err)
	// With ~43ms video packets, after ~15 video packets (~645ms > 495ms target)
	// at least one fragment should have a PART entry
	assert.Contains(t, m, "EXT-X-PART")
}

func TestWritePacket_SegmentCompletes(t *testing.T) {
	mxr, _, vCp, aCp := initMuxer(t, 2*time.Second, 3)
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 500)

	n := writePacketsUntilSegment(t, mxr, packets)
	assert.Greater(t, n, 0)

	m, err := mxr.GetIndexM3u8(context.Background(), -1, -1)
	require.NoError(t, err)
	assert.Contains(t, m, "#EXTINF:")
	assert.Contains(t, m, "segment/0/media.m4s")
}

// ---------------------------------------------------------------------------
// Manifest structure tests
// ---------------------------------------------------------------------------

func TestManifest_Header(t *testing.T) {
	mxr, _, vCp, aCp := initMuxer(t, 2*time.Second, 3)
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 5)
	for _, pkt := range packets {
		require.NoError(t, mxr.WritePacket(pkt))
	}

	m, err := mxr.GetIndexM3u8(context.Background(), -1, -1)
	require.NoError(t, err)

	assert.Contains(t, m, "#EXT-X-TARGETDURATION:2")
	assert.Contains(t, m, "#EXT-X-SERVER-CONTROL:CAN-BLOCK-RELOAD=YES")
	assert.Contains(t, m, "#EXT-X-PART-INF:PART-TARGET=0.50000")
	assert.Contains(t, m, "#EXT-X-INDEPENDENT-SEGMENTS")
	assert.Contains(t, m, "#EXT-X-MEDIA-SEQUENCE:0")
	assert.Contains(t, m, "#EXT-X-MAP:URI=\"init.mp4?v=0\"")
}

func TestManifest_TargetDurationCeiled(t *testing.T) {
	mxr, _, vCp, aCp := initMuxer(t, 2500*time.Millisecond, 3)
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 5)
	for _, pkt := range packets {
		require.NoError(t, mxr.WritePacket(pkt))
	}

	m, err := mxr.GetIndexM3u8(context.Background(), -1, -1)
	require.NoError(t, err)
	assert.Contains(t, m, "#EXT-X-TARGETDURATION:3")
}

func TestManifest_ProgramDateTime(t *testing.T) {
	mxr, _, vCp, aCp := initMuxer(t, 2*time.Second, 3)
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 500)
	writePacketsUntilSegment(t, mxr, packets)

	m, err := mxr.GetIndexM3u8(context.Background(), -1, -1)
	require.NoError(t, err)
	assert.Contains(t, m, "#EXT-X-PROGRAM-DATE-TIME:")
	for _, line := range strings.Split(m, "\n") {
		if strings.HasPrefix(line, "#EXT-X-PROGRAM-DATE-TIME:") {
			assert.True(t, strings.HasSuffix(line, "Z"), "program date time should end with Z (UTC)")
		}
	}
}

func TestManifest_PreloadHint(t *testing.T) {
	mxr, _, vCp, aCp := initMuxer(t, 2*time.Second, 3)
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 5)
	for _, pkt := range packets {
		require.NoError(t, mxr.WritePacket(pkt))
	}

	m, err := mxr.GetIndexM3u8(context.Background(), -1, -1)
	require.NoError(t, err)
	assert.Contains(t, m, "#EXT-X-PRELOAD-HINT:TYPE=PART")
}

func TestManifest_IndependentFragments(t *testing.T) {
	mxr, _, vCp, aCp := initMuxer(t, 2*time.Second, 3)
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 100)
	for _, pkt := range packets {
		require.NoError(t, mxr.WritePacket(pkt))
	}

	m, err := mxr.GetIndexM3u8(context.Background(), -1, -1)
	require.NoError(t, err)
	assert.Contains(t, m, "INDEPENDENT=YES")
}

// ---------------------------------------------------------------------------
// GetInit tests
// ---------------------------------------------------------------------------

func TestGetInit_ReturnsValidData(t *testing.T) {
	mxr, _, _, _ := initMuxer(t, 2*time.Second, 3)
	defer mxr.Release()

	data, err := mxr.GetInit()
	require.NoError(t, err)
	require.NotEmpty(t, data)
	// fMP4 init segment starts with ftyp box
	assert.Equal(t, "ftyp", string(data[4:8]))
}

func TestGetInit_IsCached(t *testing.T) {
	mxr, _, _, _ := initMuxer(t, 2*time.Second, 3)
	defer mxr.Release()

	data1, err := mxr.GetInit()
	require.NoError(t, err)
	data2, err := mxr.GetInit()
	require.NoError(t, err)
	assert.Equal(t, data1, data2)
}

func TestGetInitByVersion_InvalidVersion(t *testing.T) {
	mxr, _, _, _ := initMuxer(t, 2*time.Second, 3)
	defer mxr.Release()

	_, err := mxr.GetInitByVersion(99)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ---------------------------------------------------------------------------
// GetSegment tests
// ---------------------------------------------------------------------------

func TestGetSegment_AfterCompletion(t *testing.T) {
	mxr, _, vCp, aCp := initMuxer(t, 2*time.Second, 3)
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 500)
	writePacketsUntilSegment(t, mxr, packets)

	ctx := context.Background()
	data, err := mxr.GetSegment(ctx, 0)
	require.NoError(t, err)
	require.NotEmpty(t, data)
}

func TestGetSegment_NotFound(t *testing.T) {
	mxr, _, _, _ := initMuxer(t, 2*time.Second, 3)
	defer mxr.Release()

	_, err := mxr.GetSegment(context.Background(), 999)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestGetSegment_ContextCancelled(t *testing.T) {
	mxr, _, _, _ := initMuxer(t, 2*time.Second, 3)
	defer mxr.Release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := mxr.GetSegment(ctx, 0)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// GetFragment tests
// ---------------------------------------------------------------------------

func TestGetFragment_AfterCompletion(t *testing.T) {
	mxr, _, vCp, aCp := initMuxer(t, 2*time.Second, 3)
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 500)
	writePacketsUntilSegment(t, mxr, packets)

	ctx := context.Background()
	data, err := mxr.GetFragment(ctx, 0, 0)
	require.NoError(t, err)
	require.NotEmpty(t, data)
}

func TestGetFragment_SegmentNotFound(t *testing.T) {
	mxr, _, _, _ := initMuxer(t, 2*time.Second, 3)
	defer mxr.Release()

	_, err := mxr.GetFragment(context.Background(), 999, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ---------------------------------------------------------------------------
// GetIndexM3u8 tests
// ---------------------------------------------------------------------------

func TestGetIndexM3u8_ImmediateReturn(t *testing.T) {
	mxr, _, vCp, aCp := initMuxer(t, 2*time.Second, 3)
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 5)
	for _, pkt := range packets {
		require.NoError(t, mxr.WritePacket(pkt))
	}

	m, err := mxr.GetIndexM3u8(context.Background(), -1, -1)
	require.NoError(t, err)
	assert.Contains(t, m, "#EXTM3U")
}

func TestGetIndexM3u8_BlockingTimeout(t *testing.T) {
	mxr, _, _, _ := initMuxer(t, 2*time.Second, 3)
	defer mxr.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := mxr.GetIndexM3u8(ctx, 100, -1)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// GetMasterEntry tests
// ---------------------------------------------------------------------------

func TestGetMasterEntry_WithVideoAndAudio(t *testing.T) {
	mxr, _, _, _ := initMuxer(t, 2*time.Second, 3)
	defer mxr.Release()

	entry, err := mxr.GetMasterEntry()
	require.NoError(t, err)
	assert.Contains(t, entry, "#EXT-X-STREAM-INF:")
	assert.Contains(t, entry, "BANDWIDTH=")
	assert.Contains(t, entry, "RESOLUTION=")
	assert.Contains(t, entry, "CODECS=")
	assert.Contains(t, entry, "FRAME-RATE=")
	assert.Contains(t, entry, "avc1.")
	assert.Contains(t, entry, "mp4a.")
}

func TestGetMasterEntry_VideoOnly(t *testing.T) {
	pair, _, _ := loadTestCodecPair(t)
	pair.AudioCodecParameters = nil
	mxr := newTestMuxer(t, 2*time.Second, 3)
	require.NoError(t, mxr.Mux(pair))
	defer mxr.Release()

	entry, err := mxr.GetMasterEntry()
	require.NoError(t, err)
	assert.Contains(t, entry, "avc1.")
	assert.NotContains(t, entry, "mp4a.")
}

func TestGetMasterEntry_AudioOnly_ReturnsError(t *testing.T) {
	pair, _, _ := loadTestCodecPair(t)
	pair.VideoCodecParameters = nil
	mxr := newTestMuxer(t, 2*time.Second, 3)
	require.NoError(t, mxr.Mux(pair))
	defer mxr.Release()

	_, err := mxr.GetMasterEntry()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no video codec")
}

// ---------------------------------------------------------------------------
// UpdateCodecParameters tests
// ---------------------------------------------------------------------------

func TestUpdateCodecParameters_NilCodecs(t *testing.T) {
	mxr, _, _, _ := initMuxer(t, 2*time.Second, 3)
	defer mxr.Release()

	err := mxr.UpdateCodecParameters(gomedia.CodecParametersPair{})
	require.Error(t, err)
}

func TestUpdateCodecParameters_Discontinuity(t *testing.T) {
	mxr, pair, vCp, aCp := initMuxer(t, 2*time.Second, 3)
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 500)

	writePacketsUntilSegment(t, mxr, packets)

	err := mxr.UpdateCodecParameters(pair)
	require.NoError(t, err)

	m, err := mxr.GetIndexM3u8(context.Background(), -1, -1)
	require.NoError(t, err)
	assert.Contains(t, m, "#EXT-X-DISCONTINUITY")
}

func TestUpdateCodecParameters_NewInitVersion(t *testing.T) {
	mxr, pair, vCp, aCp := initMuxer(t, 2*time.Second, 3)
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 100)

	for _, pkt := range packets {
		require.NoError(t, mxr.WritePacket(pkt))
	}

	init0, err := mxr.GetInitByVersion(0)
	require.NoError(t, err)
	require.NotEmpty(t, init0)

	require.NoError(t, mxr.UpdateCodecParameters(pair))

	init1, err := mxr.GetInitByVersion(1)
	require.NoError(t, err)
	require.NotEmpty(t, init1)

	m, err := mxr.GetIndexM3u8(context.Background(), -1, -1)
	require.NoError(t, err)
	assert.Contains(t, m, "init.mp4?v=1")
}

// ---------------------------------------------------------------------------
// Segment eviction tests
// ---------------------------------------------------------------------------

func TestSegmentEviction(t *testing.T) {
	mxr, _, vCp, aCp := initMuxer(t, 1*time.Second, 3)
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 1000)

	for _, pkt := range packets {
		require.NoError(t, mxr.WritePacket(pkt))
	}

	m, err := mxr.GetIndexM3u8(context.Background(), -1, -1)
	require.NoError(t, err)

	extinfCount := strings.Count(m, "#EXTINF:")
	assert.Greater(t, extinfCount, 0, "should have completed segments")

	assert.Contains(t, m, "#EXT-X-MEDIA-SEQUENCE:")

	// Evicted segments should not be accessible
	_, err = mxr.GetSegment(context.Background(), 0)
	assert.Error(t, err, "segment 0 should have been evicted")
}

// ---------------------------------------------------------------------------
// WithMediaName option tests
// ---------------------------------------------------------------------------

func TestWithMediaName(t *testing.T) {
	mxr, _, vCp, aCp := initMuxer(t, 2*time.Second, 3, WithMediaName("custom"))
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 500)
	writePacketsUntilSegment(t, mxr, packets)

	m, err := mxr.GetIndexM3u8(context.Background(), -1, -1)
	require.NoError(t, err)
	assert.Contains(t, m, "custom.m4s")
	assert.NotContains(t, m, "media.m4s")
}

// ---------------------------------------------------------------------------
// Release tests
// ---------------------------------------------------------------------------

func TestRelease_CleansUp(t *testing.T) {
	mxr, _, vCp, aCp := initMuxer(t, 2*time.Second, 3)
	packets := loadTestPackets(t, vCp, aCp, 100)
	for _, pkt := range packets {
		require.NoError(t, mxr.WritePacket(pkt))
	}

	mxr.Release()

	_, err := mxr.GetSegment(context.Background(), 0)
	assert.Error(t, err)
}

func TestRelease_DoubleRelease(t *testing.T) {
	mxr, _, _, _ := initMuxer(t, 2*time.Second, 3)
	mxr.Release()
	mxr.Release()
}

// ---------------------------------------------------------------------------
// Segment/fragment MP4 content validation
// ---------------------------------------------------------------------------

func TestSegmentMP4_ContainsFmp4Boxes(t *testing.T) {
	mxr, _, vCp, aCp := initMuxer(t, 2*time.Second, 3)
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 500)
	writePacketsUntilSegment(t, mxr, packets)

	data, err := mxr.GetSegment(context.Background(), 0)
	require.NoError(t, err)
	require.True(t, len(data) > 8)
	assert.Equal(t, "styp", string(data[4:8]))
}

func TestFragmentMP4_ContainsFmp4Boxes(t *testing.T) {
	mxr, _, vCp, aCp := initMuxer(t, 2*time.Second, 3)
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 500)
	writePacketsUntilSegment(t, mxr, packets)

	data, err := mxr.GetFragment(context.Background(), 0, 0)
	require.NoError(t, err)
	require.True(t, len(data) > 8)
	assert.Equal(t, "styp", string(data[4:8]))
}

// ---------------------------------------------------------------------------
// Segment lazy generation caching
// ---------------------------------------------------------------------------

func TestSegmentMP4_Cached(t *testing.T) {
	mxr, _, vCp, aCp := initMuxer(t, 2*time.Second, 3)
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 500)
	writePacketsUntilSegment(t, mxr, packets)

	data1, err := mxr.GetSegment(context.Background(), 0)
	require.NoError(t, err)
	data2, err := mxr.GetSegment(context.Background(), 0)
	require.NoError(t, err)
	assert.Equal(t, data1, data2)
}

// ---------------------------------------------------------------------------
// End-to-end: full pipeline test
// ---------------------------------------------------------------------------

func TestEndToEnd_FullPipeline(t *testing.T) {
	mxr, _, vCp, aCp := initMuxer(t, 2*time.Second, 3)
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 1000)

	for _, pkt := range packets {
		require.NoError(t, mxr.WritePacket(pkt))
	}

	ctx := context.Background()

	// 1. Master entry should be valid
	master, err := mxr.GetMasterEntry()
	require.NoError(t, err)
	assert.Contains(t, master, "#EXT-X-STREAM-INF:")

	// 2. Init segment should be valid fMP4
	initData, err := mxr.GetInit()
	require.NoError(t, err)
	assert.Equal(t, "ftyp", string(initData[4:8]))

	// 3. Manifest should have segments
	m, err := mxr.GetIndexM3u8(ctx, -1, -1)
	require.NoError(t, err)
	assert.Contains(t, m, "#EXTINF:")

	// 4. Fetch a completed segment from the manifest
	for _, line := range strings.Split(m, "\n") {
		if strings.HasPrefix(line, "segment/") {
			var segID uint64
			_, scanErr := fmt.Sscanf(line, "segment/%d/", &segID)
			if scanErr != nil {
				continue
			}
			data, getErr := mxr.GetSegment(ctx, segID)
			require.NoError(t, getErr)
			assert.NotEmpty(t, data)
			break
		}
	}
}

// ---------------------------------------------------------------------------
// Discontinuity sequence tracking
// ---------------------------------------------------------------------------

func TestDiscontinuitySequence_AfterEviction(t *testing.T) {
	mxr, pair, vCp, aCp := initMuxer(t, 1*time.Second, 3)
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 500)

	writePacketsUntilSegment(t, mxr, packets[:200])

	require.NoError(t, mxr.UpdateCodecParameters(pair))

	for _, pkt := range packets[200:] {
		require.NoError(t, mxr.WritePacket(pkt))
	}

	m, err := mxr.GetIndexM3u8(context.Background(), -1, -1)
	require.NoError(t, err)
	// If the discontinuity segment was evicted, discontinuity sequence should be present
	if !strings.Contains(m, "#EXT-X-DISCONTINUITY") {
		assert.Contains(t, m, "#EXT-X-DISCONTINUITY-SEQUENCE:")
	}
}

// ---------------------------------------------------------------------------
// Concurrent access test
// ---------------------------------------------------------------------------

func TestConcurrentAccess(t *testing.T) {
	mxr, _, vCp, aCp := initMuxer(t, 2*time.Second, 3)
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 500)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, pkt := range packets {
			if err := mxr.WritePacket(pkt); err != nil {
				return
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		default:
			_, _ = mxr.GetIndexM3u8(context.Background(), -1, -1)
			_, _ = mxr.GetInit()
			_, _ = mxr.GetMasterEntry()
		}
	}
}
