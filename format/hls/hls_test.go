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

// Test data helpers

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
	videoCp, err := h264.NewCodecDataFromAVCDecoderConfRecord(recordBytes)
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

// Mux / initialization tests

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

// WritePacket tests

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

// Manifest structure tests

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
	assert.Contains(t, m, "#EXT-X-PART-INF:PART-TARGET=0.49995")
	assert.NotContains(t, m, "#EXT-X-INDEPENDENT-SEGMENTS")
	assert.Contains(t, m, "#EXT-X-MEDIA-SEQUENCE:0")
	assert.Contains(t, m, "#EXT-X-MAP:URI=\"init.mp4?v=0\"")
}

func TestManifest_Header_KeyframeSplit(t *testing.T) {
	mxr, _, vCp, aCp := initMuxer(t, 2*time.Second, 3, WithKeyframeSplit(true))
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 5)
	for _, pkt := range packets {
		require.NoError(t, mxr.WritePacket(pkt))
	}

	m, err := mxr.GetIndexM3u8(context.Background(), -1, -1)
	require.NoError(t, err)
	assert.Contains(t, m, "#EXT-X-INDEPENDENT-SEGMENTS")
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

// GetInit tests

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

// GetSegment tests

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

// GetFragment tests

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

// GetIndexM3u8 tests

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

// GetMasterEntry tests

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

// UpdateCodecParameters tests

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

// Segment eviction tests

func TestSegmentEviction(t *testing.T) {
	// segmentCount=2: with keyframe-aligned segments (~8.7s each from 3 keyframes
	// in test data), 3 segments are created; keeping 2 triggers eviction of the oldest.
	mxr, _, vCp, aCp := initMuxer(t, 1*time.Second, 2)
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

// Target duration bounds (strict time-based splitting)

func TestTimeBasedSplit_RespectsTargetDuration(t *testing.T) {
	targetDur := 2 * time.Second
	mxr, _, vCp, aCp := initMuxer(t, targetDur, 255)
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 1000)

	for _, pkt := range packets {
		require.NoError(t, mxr.WritePacket(pkt))
	}

	m, err := mxr.GetIndexM3u8(context.Background(), -1, -1)
	require.NoError(t, err)

	var extinfCount int
	for _, line := range strings.Split(m, "\n") {
		if !strings.HasPrefix(line, "#EXTINF:") {
			continue
		}
		var dur float64
		_, scanErr := fmt.Sscanf(line, "#EXTINF:%f", &dur)
		require.NoError(t, scanErr)
		assert.LessOrEqual(t, dur, targetDur.Seconds(),
			"segment duration %.5f exceeds target %.5f", dur, targetDur.Seconds())
		extinfCount++
	}
	assert.Greater(t, extinfCount, 1, "expected multiple completed segments")
}

// WithMediaName option tests

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

// Release tests

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

// Segment/fragment MP4 content validation

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

// Segment lazy generation caching

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

// End-to-end: full pipeline test

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

// Discontinuity sequence tracking

func TestDiscontinuitySequence_AfterEviction(t *testing.T) {
	mxr, pair, vCp, aCp := initMuxer(t, 1*time.Second, 3)
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 500)

	// With keyframe-aligned segments the first segment completes at the second
	// keyframe (packet ~336), so we pass all packets and let the helper stop
	// as soon as a segment finishes.
	n := writePacketsUntilSegment(t, mxr, packets)

	require.NoError(t, mxr.UpdateCodecParameters(pair))

	for _, pkt := range packets[n:] {
		require.NoError(t, mxr.WritePacket(pkt))
	}

	m, err := mxr.GetIndexM3u8(context.Background(), -1, -1)
	require.NoError(t, err)
	// If the discontinuity segment was evicted, discontinuity sequence should be present
	if !strings.Contains(m, "#EXT-X-DISCONTINUITY") {
		assert.Contains(t, m, "#EXT-X-DISCONTINUITY-SEQUENCE:")
	}
}

// Concurrent access test

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

// Keyframe gate tests

func TestKeyframeSplit_DropsUntilFirstKeyframe(t *testing.T) {
	mxr, _, vCp, aCp := initMuxer(t, 2*time.Second, 3, WithKeyframeSplit(true))
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 400)

	// Feed everything before the second GOP except the opening keyframe
	// (index 1): audio and mid-GOP video packets must all be dropped.
	for i, pkt := range packets[:336] {
		if i == 1 {
			continue
		}
		require.NoError(t, mxr.WritePacket(pkt))
	}
	assert.Empty(t, mxr.getCurSegment().fragments[0].packets)

	// The next keyframe (index 336) opens the gate.
	for _, pkt := range packets[336:] {
		require.NoError(t, mxr.WritePacket(pkt))
	}
	seg, ok := mxr.getSegment(0)
	require.True(t, ok)
	require.NotEmpty(t, seg.fragments[0].packets)
	vFirst, isVideo := seg.fragments[0].packets[0].(gomedia.VideoPacket)
	require.True(t, isVideo)
	assert.True(t, vFirst.IsKeyFrame())
}

func TestNoKeyframeSplit_AcceptsPreKeyframePackets(t *testing.T) {
	mxr, _, vCp, aCp := initMuxer(t, 2*time.Second, 3)
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 10)

	// Default strategy is unchanged: mid-GOP packets are written as before.
	for i, pkt := range packets {
		if i == 1 {
			continue
		}
		require.NoError(t, mxr.WritePacket(pkt))
	}
	seg, ok := mxr.getSegment(0)
	require.True(t, ok)
	assert.NotEmpty(t, seg.fragments[0].packets)
}

// Timestamp wrap tests

func TestWrap_EmitsDiscontinuityAndResetsTimeline(t *testing.T) {
	mxr, _, vCp, aCp := initMuxer(t, 2*time.Second, 10,
		WithKeyframeSplit(true), WithMaxTimestamp(5*time.Second))
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 1000)

	for _, pkt := range packets {
		require.NoError(t, mxr.WritePacket(pkt))
	}

	m, err := mxr.GetIndexM3u8(context.Background(), -1, -1)
	require.NoError(t, err)
	assert.Contains(t, m, "#EXT-X-DISCONTINUITY\n")

	var discoSeg *segment
	mxr.segments.RLock()
	for _, id := range mxr.segments.segIDs {
		seg := mxr.segments.segments[id]
		if seg.discontinuity {
			discoSeg = seg
			break
		}
	}
	mxr.segments.RUnlock()
	require.NotNil(t, discoSeg)

	// The post-wrap segment opens with the keyframe that reset the epoch.
	require.NotEmpty(t, discoSeg.fragments[0].packets)
	first := discoSeg.fragments[0].packets[0]
	vFirst, isVideo := first.(gomedia.VideoPacket)
	require.True(t, isVideo)
	assert.True(t, vFirst.IsKeyFrame())
	assert.Equal(t, time.Duration(0), first.Timestamp())

	// The timeline stays bounded: maxTS plus at most one GOP of overshoot
	// (fixture GOP is ~14.5s) and never goes negative.
	mxr.segments.RLock()
	for _, id := range mxr.segments.segIDs {
		for _, frag := range mxr.segments.segments[id].fragments {
			for _, pkt := range frag.packets {
				assert.GreaterOrEqual(t, pkt.Timestamp(), time.Duration(0))
				assert.Less(t, pkt.Timestamp(), 15*time.Second)
			}
		}
	}
	mxr.segments.RUnlock()
}

// Max segment duration cap tests

func TestKeyframeSplit_CapForcesMidGopRotation(t *testing.T) {
	// Fixture GOP is ~14.5s; with a 3s cap segments must be force-cut mid-GOP.
	mxr, _, vCp, aCp := initMuxer(t, 2*time.Second, 5,
		WithKeyframeSplit(true), WithMaxSegmentDuration(3*time.Second))
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 300)
	for _, pkt := range packets {
		require.NoError(t, mxr.WritePacket(pkt))
	}

	m, err := mxr.GetIndexM3u8(context.Background(), -1, -1)
	require.NoError(t, err)

	// The cap is the honest TARGETDURATION upper bound.
	assert.Contains(t, m, "#EXT-X-TARGETDURATION:3")
	// After the first forced cut the global independence guarantee is gone.
	assert.NotContains(t, m, "#EXT-X-INDEPENDENT-SEGMENTS")

	// Every closed segment must respect the cap (+one frame of tolerance).
	for _, line := range strings.Split(m, "\n") {
		if !strings.HasPrefix(line, "#EXTINF:") {
			continue
		}
		var dur float64
		_, scanErr := fmt.Sscanf(line, "#EXTINF:%f", &dur)
		require.NoError(t, scanErr)
		assert.LessOrEqual(t, dur, 3.1, "segment duration exceeds cap: %s", line)
	}
}

func TestKeyframeSplit_NoCapSplitKeepsIndependentTag(t *testing.T) {
	// Cap large enough to never fire within the fed data: the tag must stay
	// and TARGETDURATION must advertise the cap.
	mxr, _, vCp, aCp := initMuxer(t, 2*time.Second, 3,
		WithKeyframeSplit(true), WithMaxSegmentDuration(20*time.Second))
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 100)
	for _, pkt := range packets {
		require.NoError(t, mxr.WritePacket(pkt))
	}

	m, err := mxr.GetIndexM3u8(context.Background(), -1, -1)
	require.NoError(t, err)
	assert.Contains(t, m, "#EXT-X-INDEPENDENT-SEGMENTS")
	assert.Contains(t, m, "#EXT-X-TARGETDURATION:20")
}

func TestKeyframeSplit_NoCapConfigured_UnboundedAndHonestDefaults(t *testing.T) {
	// Without WithMaxSegmentDuration nothing is force-cut: rotation waits for
	// a keyframe however long the GOP (fixture GOP ~14.5s), the INDEPENDENT
	// tag stays, and TARGETDURATION advertises the desired segment duration.
	mxr, _, vCp, aCp := initMuxer(t, 2*time.Second, 3, WithKeyframeSplit(true))
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 300)
	for _, pkt := range packets {
		require.NoError(t, mxr.WritePacket(pkt))
	}

	m, err := mxr.GetIndexM3u8(context.Background(), -1, -1)
	require.NoError(t, err)
	assert.Contains(t, m, "#EXT-X-INDEPENDENT-SEGMENTS")
	assert.Contains(t, m, "#EXT-X-TARGETDURATION:2")
	assert.False(t, mxr.capSplitSeen)
}

func TestMaxSegmentDuration_ClampedToSegmentDuration(t *testing.T) {
	mxr := newTestMuxer(t, 4*time.Second, 3,
		WithKeyframeSplit(true), WithMaxSegmentDuration(time.Second))
	assert.Equal(t, 4*time.Second, mxr.maxSegmentDuration)
	assert.Contains(t, mxr.header, "#EXT-X-TARGETDURATION:4")
}

// Duration-based eviction tests

func TestDurationEviction_KeepsMinPlaylistDuration(t *testing.T) {
	// Time-based 1s segments, keep at least 3s of closed segments.
	mxr, _, vCp, aCp := initMuxer(t, time.Second, 3, WithMinPlaylistDuration(3*time.Second))
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 500)
	for _, pkt := range packets {
		require.NoError(t, mxr.WritePacket(pkt))
	}

	// Eviction must have happened (fixture holds ~14s of media).
	assert.Greater(t, mxr.mediaSequence, int64(0))

	var closed time.Duration
	var oldest time.Duration
	for i, id := range mxr.segIDs[:len(mxr.segIDs)-1] {
		seg, ok := mxr.getSegment(id)
		require.True(t, ok)
		if i == 0 {
			oldest = seg.duration
		}
		closed += seg.duration
	}
	// The retained closed segments satisfy the minimum...
	assert.GreaterOrEqual(t, closed, 3*time.Second)
	// ...and evicting one more would violate it (window is tight).
	assert.Less(t, closed-oldest, 3*time.Second)
}

func TestDurationEviction_CountIsIrrelevant(t *testing.T) {
	// segmentCount=1 must not shrink the playlist below the duration bound.
	mxr, _, vCp, aCp := initMuxer(t, time.Second, 1, WithMinPlaylistDuration(5*time.Second))
	defer mxr.Release()
	packets := loadTestPackets(t, vCp, aCp, 500)
	for _, pkt := range packets {
		require.NoError(t, mxr.WritePacket(pkt))
	}

	var closed time.Duration
	for _, id := range mxr.segIDs[:len(mxr.segIDs)-1] {
		if seg, ok := mxr.getSegment(id); ok {
			closed += seg.duration
		}
	}
	assert.GreaterOrEqual(t, closed, 5*time.Second)
	assert.Greater(t, len(mxr.segIDs), 5)
}
