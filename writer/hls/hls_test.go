//nolint:mnd // Test file contains many magic numbers for expected values
package hls

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/aac"
	"github.com/ugparu/gomedia/codec/h264"
	"github.com/ugparu/gomedia/utils/logger"
)

var variantPathRe = regexp.MustCompile(`\d+/([0-9a-f]{8})/\S+\.m3u8`)

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

func loadTestCodecPair(t *testing.T, sourceID string) (gomedia.CodecParametersPair, *h264.CodecParameters, *aac.CodecParameters) {
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
		SourceID:             sourceID,
		VideoCodecParameters: &videoCp,
		AudioCodecParameters: &audioCp,
	}
	return pair, &videoCp, &audioCp
}

func loadTestPackets(t *testing.T, sourceID string, videoCp *h264.CodecParameters, audioCp *aac.CodecParameters, limit int) []gomedia.Packet {
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
			pkt := h264.NewPacket(entry.IsKeyframe, ts, absBase, data, sourceID, videoCp)
			pkt.SetDuration(dur)
			result = append(result, pkt)
		case "AAC":
			result = append(result, aac.NewPacket(data, ts, sourceID, absBase, audioCp, dur))
		default:
			t.Fatalf("unexpected codec: %s", entry.Codec)
		}
	}
	return result
}

// newWriter creates an HLS writer for testing, starts it, and returns it.
// The caller must call Close() when done.
func newWriter(t *testing.T, id uint64, segCount uint8, segDur time.Duration, opts ...Option) gomedia.HLSStreamer {
	t.Helper()
	w := New(id, segCount, segDur, 512, 1.5, opts...)
	w.Write()
	t.Cleanup(func() {
		w.Close()
		<-w.Done()
	})
	return w
}

// sendPackets sends all packets to the writer's Packets channel.
func sendPackets(t *testing.T, w gomedia.HLSStreamer, packets []gomedia.Packet) {
	t.Helper()
	for _, pkt := range packets {
		w.Packets() <- pkt
	}
}

// waitForMaster polls until the master playlist is non-empty or timeout.
func waitForMaster(t *testing.T, w gomedia.HLSStreamer, timeout time.Duration) string {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for master playlist")
		default:
		}
		m, err := w.GetMasterPlaylist()
		require.NoError(t, err)
		if m != "" {
			return m
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// extractUIDs parses all muxer UIDs from a master playlist, returned in order.
func extractUIDs(t *testing.T, master string) []string {
	t.Helper()
	matches := variantPathRe.FindAllStringSubmatch(master, -1)
	uids := make([]string, 0, len(matches))
	for _, m := range matches {
		uids = append(uids, m[1])
	}
	return uids
}

// waitForSegment polls until the index manifest contains #EXTINF (a completed segment).
func waitForSegment(t *testing.T, w gomedia.HLSStreamer, uid string, timeout time.Duration) string {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for segment completion")
		default:
		}
		m, err := w.GetIndexM3u8(context.Background(), uid, -1, -1)
		if err != nil {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		if strings.Contains(m, "#EXTINF:") {
			return m
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// ---------------------------------------------------------------------------
// Constructor and options
// ---------------------------------------------------------------------------

func TestNew_ReturnsNonNil(t *testing.T) {
	w := New(1, 3, 2*time.Second, 64, 1.5)
	require.NotNil(t, w)
}

func TestNew_WithOptions(t *testing.T) {
	w := New(1, 3, 2*time.Second, 64, 1.5,
		WithLogger(logger.Default),
		WithIndexName("custom.m3u8"),
		WithMediaName("stream"),
		WithVersion(9),
	).(*hlsWriter)

	assert.Equal(t, "custom.m3u8", w.indexName)
	assert.Equal(t, "stream", w.mediaName)
	assert.Equal(t, 9, w.version)
}

func TestNew_DefaultValues(t *testing.T) {
	w := New(42, 5, 3*time.Second, 64, 2.0).(*hlsWriter)

	assert.Equal(t, uint64(42), w.id)
	assert.Equal(t, uint8(5), w.segmentCount)
	assert.Equal(t, 3*time.Second, w.segmentDuration)
	assert.Equal(t, "index.m3u8", w.indexName)
	assert.Equal(t, "media", w.mediaName)
	assert.Equal(t, 7, w.version)
	assert.Equal(t, 2.0, w.partHoldBack)
}

// ---------------------------------------------------------------------------
// Channel accessors
// ---------------------------------------------------------------------------

func TestPackets_ReturnsNonNilChannel(t *testing.T) {
	w := New(1, 3, 2*time.Second, 64, 1.5)
	assert.NotNil(t, w.Packets())
}

func TestAddSource_ReturnsNonNilChannel(t *testing.T) {
	w := New(1, 3, 2*time.Second, 64, 1.5)
	assert.NotNil(t, w.AddSource())
}

func TestRemoveSource_ReturnsNonNilChannel(t *testing.T) {
	w := New(1, 3, 2*time.Second, 64, 1.5)
	assert.NotNil(t, w.RemoveSource())
}

// ---------------------------------------------------------------------------
// Lifecycle: Write / Close / Done
// ---------------------------------------------------------------------------

func TestWrite_Close_Done(t *testing.T) {
	w := New(1, 3, 2*time.Second, 64, 1.5)
	w.Write()
	w.Close()
	select {
	case <-w.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done not signaled after Close")
	}
}

func TestClose_WithoutWrite(t *testing.T) {
	w := New(1, 3, 2*time.Second, 64, 1.5)
	// Close without Write should not panic.
	w.Close()
}

// ---------------------------------------------------------------------------
// Single source: packet processing
// ---------------------------------------------------------------------------

func TestSingleSource_MasterPlaylistGenerated(t *testing.T) {
	w := newWriter(t, 100, 3, 2*time.Second)
	_, vCp, aCp := loadTestCodecPair(t, "src1")
	packets := loadTestPackets(t, "src1", vCp, aCp, 50)

	sendPackets(t, w, packets)
	master := waitForMaster(t, w, 3*time.Second)

	assert.Contains(t, master, "#EXTM3U")
	assert.Contains(t, master, "#EXT-X-VERSION:7")
	assert.Contains(t, master, "#EXT-X-STREAM-INF:")
	assert.Contains(t, master, "BANDWIDTH=")
	assert.Contains(t, master, "RESOLUTION=")
	assert.Contains(t, master, "CODECS=")
	assert.Contains(t, master, "FRAME-RATE=")

	uids := extractUIDs(t, master)
	require.Len(t, uids, 1)
	assert.Contains(t, master, fmt.Sprintf("100/%s/index.m3u8", uids[0]))
}

func TestSingleSource_MasterPlaylistUsesCustomVersion(t *testing.T) {
	w := newWriter(t, 100, 3, 2*time.Second, WithVersion(9))
	_, vCp, aCp := loadTestCodecPair(t, "src1")
	packets := loadTestPackets(t, "src1", vCp, aCp, 50)

	sendPackets(t, w, packets)
	master := waitForMaster(t, w, 3*time.Second)

	assert.Contains(t, master, "#EXT-X-VERSION:9")
}

func TestSingleSource_MasterPlaylistUsesCustomIndexName(t *testing.T) {
	w := newWriter(t, 55, 3, 2*time.Second, WithIndexName("playlist.m3u8"))
	_, vCp, aCp := loadTestCodecPair(t, "src1")
	packets := loadTestPackets(t, "src1", vCp, aCp, 50)

	sendPackets(t, w, packets)
	master := waitForMaster(t, w, 3*time.Second)

	assert.Regexp(t, `55/[0-9a-f]{8}/playlist\.m3u8`, master)
}

func TestSingleSource_IndexM3u8Available(t *testing.T) {
	w := newWriter(t, 1, 3, 2*time.Second)
	_, vCp, aCp := loadTestCodecPair(t, "src1")
	packets := loadTestPackets(t, "src1", vCp, aCp, 500)

	sendPackets(t, w, packets)
	master := waitForMaster(t, w, 3*time.Second)
	uids := extractUIDs(t, master)
	require.Len(t, uids, 1)

	manifest := waitForSegment(t, w, uids[0], 3*time.Second)
	assert.Contains(t, manifest, "#EXTM3U")
	assert.Contains(t, manifest, "#EXT-X-TARGETDURATION:")
	assert.Contains(t, manifest, "#EXT-X-MEDIA-SEQUENCE:")
	assert.Contains(t, manifest, "#EXT-X-MAP:URI=\"init.mp4?v=0\"")
}

func TestSingleSource_GetInitReturnsData(t *testing.T) {
	w := newWriter(t, 1, 3, 2*time.Second)
	_, vCp, aCp := loadTestCodecPair(t, "src1")
	packets := loadTestPackets(t, "src1", vCp, aCp, 50)

	sendPackets(t, w, packets)
	master := waitForMaster(t, w, 3*time.Second)
	uids := extractUIDs(t, master)
	require.Len(t, uids, 1)

	init, err := w.GetInit(uids[0])
	require.NoError(t, err)
	assert.NotEmpty(t, init)
}

func TestSingleSource_GetInitByVersion(t *testing.T) {
	w := newWriter(t, 1, 3, 2*time.Second)
	_, vCp, aCp := loadTestCodecPair(t, "src1")
	packets := loadTestPackets(t, "src1", vCp, aCp, 50)

	sendPackets(t, w, packets)
	master := waitForMaster(t, w, 3*time.Second)
	uids := extractUIDs(t, master)
	require.Len(t, uids, 1)

	init, err := w.GetInitByVersion(uids[0], 0)
	require.NoError(t, err)
	assert.NotEmpty(t, init)
}

func TestSingleSource_GetSegmentAfterCompletion(t *testing.T) {
	w := newWriter(t, 1, 3, 2*time.Second)
	_, vCp, aCp := loadTestCodecPair(t, "src1")
	packets := loadTestPackets(t, "src1", vCp, aCp, 500)

	sendPackets(t, w, packets)
	master := waitForMaster(t, w, 3*time.Second)
	uids := extractUIDs(t, master)
	require.Len(t, uids, 1)
	waitForSegment(t, w, uids[0], 3*time.Second)

	seg, err := w.GetSegment(context.Background(), uids[0], 0)
	require.NoError(t, err)
	assert.NotEmpty(t, seg)
}

func TestSingleSource_GetFragmentAfterCompletion(t *testing.T) {
	w := newWriter(t, 1, 3, 2*time.Second)
	_, vCp, aCp := loadTestCodecPair(t, "src1")
	packets := loadTestPackets(t, "src1", vCp, aCp, 500)

	sendPackets(t, w, packets)
	master := waitForMaster(t, w, 3*time.Second)
	uids := extractUIDs(t, master)
	require.Len(t, uids, 1)
	waitForSegment(t, w, uids[0], 3*time.Second)

	frag, err := w.GetFragment(context.Background(), uids[0], 0, 0)
	require.NoError(t, err)
	assert.NotEmpty(t, frag)
}

// ---------------------------------------------------------------------------
// Invalid index lookups
// ---------------------------------------------------------------------------

func TestGetIndexM3u8_InvalidUID(t *testing.T) {
	w := newWriter(t, 1, 3, 2*time.Second)
	_, err := w.GetIndexM3u8(context.Background(), "nonexistent", -1, -1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestGetInit_InvalidUID(t *testing.T) {
	w := newWriter(t, 1, 3, 2*time.Second)
	_, err := w.GetInit("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestGetInitByVersion_InvalidUID(t *testing.T) {
	w := newWriter(t, 1, 3, 2*time.Second)
	_, err := w.GetInitByVersion("nonexistent", 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestGetSegment_InvalidUID(t *testing.T) {
	w := newWriter(t, 1, 3, 2*time.Second)
	_, err := w.GetSegment(context.Background(), "nonexistent", 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestGetFragment_InvalidUID(t *testing.T) {
	w := newWriter(t, 1, 3, 2*time.Second)
	_, err := w.GetFragment(context.Background(), "nonexistent", 0, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ---------------------------------------------------------------------------
// Empty state
// ---------------------------------------------------------------------------

func TestGetMasterPlaylist_EmptyBeforePackets(t *testing.T) {
	w := newWriter(t, 1, 3, 2*time.Second)
	master, err := w.GetMasterPlaylist()
	require.NoError(t, err)
	assert.Empty(t, master)
}

// ---------------------------------------------------------------------------
// Source removal
// ---------------------------------------------------------------------------

func TestRemoveSource_RemovesMuxer(t *testing.T) {
	w := newWriter(t, 1, 3, 2*time.Second)
	_, vCp, aCp := loadTestCodecPair(t, "src1")
	packets := loadTestPackets(t, "src1", vCp, aCp, 50)

	sendPackets(t, w, packets)
	master := waitForMaster(t, w, 3*time.Second)
	uids := extractUIDs(t, master)
	require.Len(t, uids, 1)

	_, err := w.GetInit(uids[0])
	require.NoError(t, err)

	// Remove the source.
	w.RemoveSource() <- "src1"

	// Give the Step loop time to process the removal.
	time.Sleep(100 * time.Millisecond)

	master, err = w.GetMasterPlaylist()
	require.NoError(t, err)
	assert.NotContains(t, master, "#EXT-X-STREAM-INF:")

	// Old UID should no longer resolve.
	_, err = w.GetInit(uids[0])
	assert.Error(t, err)
}

func TestRemoveSource_NonexistentSource(t *testing.T) {
	w := newWriter(t, 1, 3, 2*time.Second)

	// Removing a source that was never added should not panic or error.
	w.RemoveSource() <- "nonexistent"
	time.Sleep(50 * time.Millisecond)

	master, err := w.GetMasterPlaylist()
	require.NoError(t, err)
	// No variant entries should be present.
	assert.NotContains(t, master, "#EXT-X-STREAM-INF:")
}

// ---------------------------------------------------------------------------
// AddSource channel
// ---------------------------------------------------------------------------

func TestAddSource_DoesNotBlock(t *testing.T) {
	w := newWriter(t, 1, 3, 2*time.Second)

	// Sending to AddSource should not block (channel is buffered and drained).
	done := make(chan struct{})
	go func() {
		w.AddSource() <- "test-url"
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("AddSource() send blocked")
	}
}

// ---------------------------------------------------------------------------
// Multi-source
// ---------------------------------------------------------------------------

func TestMultiSource_MasterPlaylistContainsAllSources(t *testing.T) {
	w := newWriter(t, 200, 3, 2*time.Second)
	_, vCp1, aCp1 := loadTestCodecPair(t, "src1")
	_, vCp2, aCp2 := loadTestCodecPair(t, "src2")

	packets1 := loadTestPackets(t, "src1", vCp1, aCp1, 50)
	packets2 := loadTestPackets(t, "src2", vCp2, aCp2, 50)

	sendPackets(t, w, packets1)
	sendPackets(t, w, packets2)
	waitForMaster(t, w, 3*time.Second)

	// Wait for both sources to register.
	time.Sleep(200 * time.Millisecond)
	master, err := w.GetMasterPlaylist()
	require.NoError(t, err)

	// Should have two variant entries with distinct UIDs.
	uids := extractUIDs(t, master)
	require.Len(t, uids, 2)
	assert.NotEqual(t, uids[0], uids[1])
	assert.Equal(t, 2, strings.Count(master, "#EXT-X-STREAM-INF:"))
}

func TestMultiSource_IndependentIndexPlaylists(t *testing.T) {
	w := newWriter(t, 1, 3, 2*time.Second)
	_, vCp1, aCp1 := loadTestCodecPair(t, "src1")
	_, vCp2, aCp2 := loadTestCodecPair(t, "src2")

	packets1 := loadTestPackets(t, "src1", vCp1, aCp1, 500)
	packets2 := loadTestPackets(t, "src2", vCp2, aCp2, 500)

	sendPackets(t, w, packets1)
	sendPackets(t, w, packets2)

	// Wait for both sources to appear in master.
	time.Sleep(200 * time.Millisecond)
	master, err := w.GetMasterPlaylist()
	require.NoError(t, err)
	uids := extractUIDs(t, master)
	require.Len(t, uids, 2)

	// Wait for both to have segments.
	waitForSegment(t, w, uids[0], 5*time.Second)
	waitForSegment(t, w, uids[1], 5*time.Second)

	m0, err := w.GetIndexM3u8(context.Background(), uids[0], -1, -1)
	require.NoError(t, err)
	m1, err := w.GetIndexM3u8(context.Background(), uids[1], -1, -1)
	require.NoError(t, err)

	// Both should be valid HLS playlists.
	assert.Contains(t, m0, "#EXTM3U")
	assert.Contains(t, m1, "#EXTM3U")
}

func TestMultiSource_RemoveOneKeepsOther(t *testing.T) {
	w := newWriter(t, 1, 3, 2*time.Second)
	_, vCp1, aCp1 := loadTestCodecPair(t, "src1")
	_, vCp2, aCp2 := loadTestCodecPair(t, "src2")

	packets1 := loadTestPackets(t, "src1", vCp1, aCp1, 50)
	packets2 := loadTestPackets(t, "src2", vCp2, aCp2, 50)

	sendPackets(t, w, packets1)
	sendPackets(t, w, packets2)

	// Wait for both sources.
	time.Sleep(200 * time.Millisecond)
	master, err := w.GetMasterPlaylist()
	require.NoError(t, err)
	require.Equal(t, 2, strings.Count(master, "#EXT-X-STREAM-INF:"))

	// Remove one source.
	w.RemoveSource() <- "src1"
	time.Sleep(200 * time.Millisecond)

	master, err = w.GetMasterPlaylist()
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(master, "#EXT-X-STREAM-INF:"))
}

// ---------------------------------------------------------------------------
// Audio-only source (no muxer created until video arrives)
// ---------------------------------------------------------------------------

func TestAudioOnly_NoMasterPlaylistEntry(t *testing.T) {
	w := newWriter(t, 1, 3, 2*time.Second)
	_, vCp, aCp := loadTestCodecPair(t, "audio-only")

	// Load all packets but send only audio.
	packets := loadTestPackets(t, "audio-only", vCp, aCp, 50)
	audioOnly := make([]gomedia.Packet, 0)
	for _, p := range packets {
		if _, ok := p.(gomedia.AudioPacket); ok {
			audioOnly = append(audioOnly, p)
		}
	}
	require.NotEmpty(t, audioOnly)
	sendPackets(t, w, audioOnly)

	time.Sleep(200 * time.Millisecond)
	master, err := w.GetMasterPlaylist()
	require.NoError(t, err)

	// No video means no master entry (GetMasterEntry requires video).
	assert.NotContains(t, master, "#EXT-X-STREAM-INF:")
}

// ---------------------------------------------------------------------------
// GetSegment with context cancellation
// ---------------------------------------------------------------------------

func TestGetSegment_ContextCancelled(t *testing.T) {
	w := newWriter(t, 1, 3, 2*time.Second)
	_, vCp, aCp := loadTestCodecPair(t, "src1")
	packets := loadTestPackets(t, "src1", vCp, aCp, 50)

	sendPackets(t, w, packets)
	master := waitForMaster(t, w, 3*time.Second)
	uids := extractUIDs(t, master)
	require.Len(t, uids, 1)

	// Request a segment that may not be finished, with an already cancelled context.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := w.GetSegment(ctx, uids[0], 999)
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// Concurrent access
// ---------------------------------------------------------------------------

func TestConcurrentAccess(t *testing.T) {
	w := newWriter(t, 1, 3, 2*time.Second)
	_, vCp, aCp := loadTestCodecPair(t, "src1")
	packets := loadTestPackets(t, "src1", vCp, aCp, 500)

	var wg sync.WaitGroup

	// Writer goroutine.
	wg.Add(1)
	go func() {
		defer wg.Done()
		sendPackets(t, w, packets)
	}()

	// Reader goroutines accessing HLS methods concurrently.
	// UID is dynamic so readers try whatever UID the master currently has.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				master, _ := w.GetMasterPlaylist()
				uids := variantPathRe.FindAllStringSubmatch(master, -1)
				if len(uids) > 0 {
					uid := uids[0][1]
					_, _ = w.GetIndexM3u8(context.Background(), uid, -1, -1)
					_, _ = w.GetInit(uid)
					_, _ = w.GetSegment(context.Background(), uid, 0)
					_, _ = w.GetFragment(context.Background(), uid, 0, 0)
				}
				time.Sleep(5 * time.Millisecond)
			}
		}()
	}

	wg.Wait()
}

// ---------------------------------------------------------------------------
// End-to-end pipeline
// ---------------------------------------------------------------------------

func TestEndToEnd_FullPipeline(t *testing.T) {
	w := newWriter(t, 42, 3, 2*time.Second, WithMediaName("custom"))
	_, vCp, aCp := loadTestCodecPair(t, "cam1")
	packets := loadTestPackets(t, "cam1", vCp, aCp, 500)

	sendPackets(t, w, packets)

	// 1. Master playlist should appear with a UID-based variant path.
	master := waitForMaster(t, w, 3*time.Second)
	assert.Contains(t, master, "#EXTM3U")
	uids := extractUIDs(t, master)
	require.Len(t, uids, 1)
	assert.Contains(t, master, fmt.Sprintf("42/%s/index.m3u8", uids[0]))

	// 2. Init segment should be available.
	init, err := w.GetInit(uids[0])
	require.NoError(t, err)
	assert.NotEmpty(t, init)

	// 3. Index manifest should have segments.
	manifest := waitForSegment(t, w, uids[0], 3*time.Second)
	assert.Contains(t, manifest, "#EXTINF:")
	assert.Contains(t, manifest, "#EXT-X-MAP:URI=\"init.mp4?v=0\"")

	// 4. Segment data should be retrievable.
	seg, err := w.GetSegment(context.Background(), uids[0], 0)
	require.NoError(t, err)
	assert.NotEmpty(t, seg)

	// 5. Fragment data should be retrievable.
	frag, err := w.GetFragment(context.Background(), uids[0], 0, 0)
	require.NoError(t, err)
	assert.NotEmpty(t, frag)
}

// ---------------------------------------------------------------------------
// Parallel multi-instance: 4 independent HLS streams with concurrent readers
// ---------------------------------------------------------------------------

// TestParallelInstances_FourStreamsWithConcurrentReaders emulates a real
// deployment: 4 independent HLS writer instances (4 camera streams), each
// receiving packets concurrently, while multiple reader goroutines per
// instance poll init/manifest/segments/fragments — exactly what happens
// when browsers connect to separate HLS streams.
func TestParallelInstances_FourStreamsWithConcurrentReaders(t *testing.T) {
	const (
		numStreams     = 4
		readersPerStream = 3
		pktLimit       = 500
		readIterations = 30
	)

	_, vCp, aCp := loadTestCodecPair(t, "cam1")

	writers := make([]gomedia.HLSStreamer, numStreams)
	packetSets := make([][]gomedia.Packet, numStreams)

	for i := 0; i < numStreams; i++ {
		sourceID := fmt.Sprintf("stream-%d", i)
		writers[i] = newWriter(t, uint64(i+1), 3, 2*time.Second)
		packetSets[i] = loadTestPackets(t, sourceID, vCp, aCp, pktLimit)
	}

	var wg sync.WaitGroup

	// Launch a writer goroutine per stream (concurrent packet injection).
	for i := 0; i < numStreams; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sendPackets(t, writers[idx], packetSets[idx])
		}(i)
	}

	// Launch reader goroutines per stream (emulating browser players).
	for i := 0; i < numStreams; i++ {
		for r := 0; r < readersPerStream; r++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				w := writers[idx]
				for j := 0; j < readIterations; j++ {
					master, _ := w.GetMasterPlaylist()
					uids := variantPathRe.FindAllStringSubmatch(master, -1)
					if len(uids) > 0 {
						uid := uids[0][1]
						_, _ = w.GetIndexM3u8(context.Background(), uid, -1, -1)
						_, _ = w.GetInit(uid)
						_, _ = w.GetSegment(context.Background(), uid, 0)
						_, _ = w.GetFragment(context.Background(), uid, 0, 0)
					}
					time.Sleep(2 * time.Millisecond)
				}
			}(i)
		}
	}

	wg.Wait()

	// After all packets are consumed, every stream must have produced valid data.
	for i := 0; i < numStreams; i++ {
		w := writers[i]

		master := waitForMaster(t, w, 3*time.Second)
		assert.Contains(t, master, "#EXTM3U", "stream %d master missing header", i)
		assert.Contains(t, master, "#EXT-X-STREAM-INF:", "stream %d master missing variant", i)

		uids := extractUIDs(t, master)
		require.NotEmpty(t, uids, "stream %d has no UIDs", i)
		uid := uids[0]

		initData, err := w.GetInit(uid)
		require.NoError(t, err, "stream %d GetInit", i)
		assert.NotEmpty(t, initData, "stream %d init empty", i)

		manifest := waitForSegment(t, w, uid, 3*time.Second)
		assert.Contains(t, manifest, "#EXTINF:", "stream %d missing segment", i)

		seg, err := w.GetSegment(context.Background(), uid, 0)
		require.NoError(t, err, "stream %d GetSegment", i)
		assert.NotEmpty(t, seg, "stream %d segment empty", i)

		frag, err := w.GetFragment(context.Background(), uid, 0, 0)
		require.NoError(t, err, "stream %d GetFragment", i)
		assert.NotEmpty(t, frag, "stream %d fragment empty", i)
	}
}
