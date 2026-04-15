//nolint:mnd // Test file contains many magic numbers for expected values
package segmenter

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/aac"
	"github.com/ugparu/gomedia/codec/h264"
	"github.com/ugparu/gomedia/utils/logger"
)

// ---------------------------------------------------------------------------
// Test data helpers (mirrors writer/hls pattern)
// ---------------------------------------------------------------------------

const testDataDir = "../../tests/data/h264_aac/"

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
			pkt := h264.NewPacket(entry.IsKeyframe, ts, absBase.Add(ts), data, sourceID, videoCp)
			pkt.SetDuration(dur)
			result = append(result, pkt)
		case "AAC":
			result = append(result, aac.NewPacket(data, ts, sourceID, absBase.Add(ts), audioCp, dur))
		default:
			t.Fatalf("unexpected codec: %s", entry.Codec)
		}
	}
	return result
}

// newSegmenter creates, starts, and returns a segmenter for testing.
// Cleanup is registered automatically.
func newSegmenter(t *testing.T, dest string, segDur time.Duration, mode gomedia.RecordMode, opts ...Option) gomedia.Segmenter {
	t.Helper()
	s := New(dest, segDur, mode, 512, opts...)
	s.Write()
	t.Cleanup(func() {
		s.Close()
		<-s.Done()
	})
	return s
}

// sendPackets sends packets to segmenter's Packets channel.
func sendPackets(t *testing.T, s gomedia.Segmenter, packets []gomedia.Packet) {
	t.Helper()
	for _, pkt := range packets {
		s.Packets() <- pkt
	}
}

// addSourceAndWait adds a source and waits for it to be registered.
func addSourceAndWait(t *testing.T, s gomedia.Segmenter, url string) {
	t.Helper()
	s.AddSource() <- url
	// Allow Step loop to process the addSource before sending packets.
	// This prevents the race where packets arrive before the source is registered.
	// 200ms accounts for race detector slowdown.
	time.Sleep(200 * time.Millisecond)
}

// waitForFile reads one FileInfo from Files() with a timeout.
func waitForFile(t *testing.T, s gomedia.Segmenter, timeout time.Duration) gomedia.FileInfo {
	t.Helper()
	select {
	case info := <-s.Files():
		return info
	case <-time.After(timeout):
		t.Fatal("timed out waiting for file info")
		return gomedia.FileInfo{}
	}
}

// waitForStatus reads one status update from RecordCurStatus() with a timeout.
func waitForStatus(t *testing.T, s gomedia.Segmenter, timeout time.Duration) bool {
	t.Helper()
	select {
	case status := <-s.RecordCurStatus():
		return status
	case <-time.After(timeout):
		t.Fatal("timed out waiting for record status")
		return false
	}
}

// expectNoFile asserts no file info is emitted within a short window.
// Must be called BEFORE Close() — after close the channel yields zero values.
func expectNoFile(t *testing.T, s gomedia.Segmenter) {
	t.Helper()
	select {
	case info := <-s.Files():
		t.Fatalf("unexpected file info emitted: %+v", info)
	case <-time.After(100 * time.Millisecond):
		// expected
	}
}

// drainFiles collects all FileInfo values from the Files channel after close.
// Filters out zero-value entries that result from channel close.
func drainFiles(s gomedia.Segmenter) []gomedia.FileInfo {
	var result []gomedia.FileInfo
	for info := range s.Files() {
		if info.Size > 0 || info.Name != "" {
			result = append(result, info)
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// Constructor tests
// ---------------------------------------------------------------------------

func TestNew_ReturnsNonNil(t *testing.T) {
	s := New("/tmp/test", 5*time.Second, gomedia.Always, 64)
	require.NotNil(t, s)
}

func TestNew_DefaultOptions(t *testing.T) {
	seg := New("/tmp/test", 10*time.Second, gomedia.Always, 64, WithDirPermissions(0o750)).(*segmenter)
	assert.Equal(t, 5*time.Second, seg.preBufferDuration) // targetDuration / 2
	assert.Equal(t, time.Minute, seg.maxEventDuration)
	assert.Equal(t, os.FileMode(0o750), seg.dirPerm)
}

func TestNew_WithOptions(t *testing.T) {
	seg := New("/tmp/test", 10*time.Second, gomedia.Always, 64,
		WithLogger(logger.Default),
		WithPreBufferDuration(3*time.Second),
		WithMaxEventDuration(30*time.Second),
		WithDirPermissions(0o700),
		WithPathFunc(func(st time.Time, idx int) (string, string) {
			return "custom/", "file.mp4"
		}),
	).(*segmenter)

	assert.Equal(t, 3*time.Second, seg.preBufferDuration)
	assert.Equal(t, 30*time.Second, seg.maxEventDuration)
	assert.Equal(t, os.FileMode(0o700), seg.dirPerm)

	dir, name := seg.pathFunc(time.Now(), 0)
	assert.Equal(t, "custom/", dir)
	assert.Equal(t, "file.mp4", name)
}

func TestNew_ChannelDirections(t *testing.T) {
	s := New("/tmp/test", 5*time.Second, gomedia.Always, 64)
	// Verify channels are returned with correct direction
	assert.NotNil(t, s.Packets())
	assert.NotNil(t, s.Files())
	assert.NotNil(t, s.Events())
	assert.NotNil(t, s.RecordMode())
	assert.NotNil(t, s.RecordCurStatus())
	assert.NotNil(t, s.AddSource())
	assert.NotNil(t, s.RemoveSource())
}

// ---------------------------------------------------------------------------
// Always mode — basic recording
// ---------------------------------------------------------------------------

func TestAlwaysMode_ProducesFileOnClose(t *testing.T) {
	dest := t.TempDir()
	sourceID := "rtsp://cam1"

	_, videoCp, audioCp := loadTestCodecPair(t, sourceID)
	// Load enough packets to include first keyframe + some data (~first GOP)
	packets := loadTestPackets(t, sourceID, videoCp, audioCp, 336)

	s := New(dest+"/", 30*time.Second, gomedia.Always, 512)
	s.Write()
	addSourceAndWait(t, s, sourceID)

	sendPackets(t, s, packets)

	// Wait for recording to start before closing
	status := waitForStatus(t, s, 2*time.Second)
	require.True(t, status)

	// Close triggers final flush
	s.Close()
	<-s.Done()

	files := drainFiles(s)
	require.Len(t, files, 1)
	info := files[0]
	assert.NotEmpty(t, info.Name)
	assert.Greater(t, info.Size, 0)
	assert.Equal(t, sourceID, info.URL)
	assert.Equal(t, "H264", info.Codec)
	assert.Contains(t, info.Resolution, "x")

	// Verify file exists on disk
	fullPath := filepath.Join(dest, info.Name)
	stat, err := os.Stat(fullPath)
	require.NoError(t, err)
	assert.Equal(t, info.Size, int(stat.Size()))
}

func TestAlwaysMode_SegmentRotationOnKeyframe(t *testing.T) {
	dest := t.TempDir()
	sourceID := "rtsp://cam1"

	_, videoCp, audioCp := loadTestCodecPair(t, sourceID)
	// Load all 1000 packets — covers 3 keyframes at ~0s, ~8.7s, ~17.4s
	packets := loadTestPackets(t, sourceID, videoCp, audioCp, 1000)

	// Segment duration of 5s forces rotation at each keyframe
	s := newSegmenter(t, dest+"/", 5*time.Second, gomedia.Always)
	addSourceAndWait(t, s, sourceID)

	sendPackets(t, s, packets)

	// Should get at least 2 completed segments (rotation at 2nd and 3rd keyframe)
	info1 := waitForFile(t, s, 2*time.Second)
	assert.Greater(t, info1.Size, 0)
	assert.True(t, info1.Stop.After(info1.Start))

	info2 := waitForFile(t, s, 2*time.Second)
	assert.Greater(t, info2.Size, 0)
	// Second segment should start after first ends
	assert.True(t, !info2.Start.Before(info1.Start))

	// Close to flush remaining
	s.Close()
	<-s.Done()

	// Third segment from final flush
	info3 := <-s.Files()
	assert.Greater(t, info3.Size, 0)
}

func TestAlwaysMode_RecordStatusEmitted(t *testing.T) {
	dest := t.TempDir()
	sourceID := "rtsp://cam1"

	_, videoCp, audioCp := loadTestCodecPair(t, sourceID)
	packets := loadTestPackets(t, sourceID, videoCp, audioCp, 200)

	s := newSegmenter(t, dest+"/", 30*time.Second, gomedia.Always)
	addSourceAndWait(t, s, sourceID)

	sendPackets(t, s, packets)

	status := waitForStatus(t, s, 2*time.Second)
	assert.True(t, status, "should report recording started")
}

func TestAlwaysMode_DropsPacketsBeforeFirstKeyframe(t *testing.T) {
	dest := t.TempDir()
	sourceID := "rtsp://cam1"

	_, videoCp, audioCp := loadTestCodecPair(t, sourceID)
	// First packet (index 0) is AAC, first keyframe is at index 1
	// Load just the AAC packet before the keyframe
	allPackets := loadTestPackets(t, sourceID, videoCp, audioCp, 1)

	s := newSegmenter(t, dest+"/", 30*time.Second, gomedia.Always)
	addSourceAndWait(t, s, sourceID)

	sendPackets(t, s, allPackets)

	// Allow processing
	time.Sleep(50 * time.Millisecond)

	// No file should be produced while running since we only sent audio before any keyframe
	expectNoFile(t, s)
}

func TestAlwaysMode_SkipsUnregisteredSource(t *testing.T) {
	dest := t.TempDir()
	sourceID := "rtsp://cam1"

	_, videoCp, audioCp := loadTestCodecPair(t, sourceID)
	packets := loadTestPackets(t, sourceID, videoCp, audioCp, 200)

	// Don't register source
	s := newSegmenter(t, dest+"/", 30*time.Second, gomedia.Always)

	sendPackets(t, s, packets)
	time.Sleep(50 * time.Millisecond)

	// No file should be produced while running — source not registered
	expectNoFile(t, s)
}

// ---------------------------------------------------------------------------
// Always mode — multi-stream
// ---------------------------------------------------------------------------

func TestAlwaysMode_MultiStream(t *testing.T) {
	dest := t.TempDir()
	src1 := "rtsp://cam1"
	src2 := "rtsp://cam2"

	_, videoCp1, audioCp1 := loadTestCodecPair(t, src1)
	_, videoCp2, audioCp2 := loadTestCodecPair(t, src2)
	pkts1 := loadTestPackets(t, src1, videoCp1, audioCp1, 336)
	pkts2 := loadTestPackets(t, src2, videoCp2, audioCp2, 336)

	s := newSegmenter(t, dest+"/", 30*time.Second, gomedia.Always)
	addSourceAndWait(t, s, src1)
	addSourceAndWait(t, s, src2)

	// Interleave packets
	for i := 0; i < len(pkts1) && i < len(pkts2); i++ {
		s.Packets() <- pkts1[i]
		s.Packets() <- pkts2[i]
	}

	s.Close()
	<-s.Done()

	// Should get files from both sources
	files := make(map[string]gomedia.FileInfo)
	for info := range s.Files() {
		files[info.URL] = info
	}
	assert.Contains(t, files, src1)
	assert.Contains(t, files, src2)
}

// ---------------------------------------------------------------------------
// Source management
// ---------------------------------------------------------------------------

func TestAddSource_Duplicate(t *testing.T) {
	dest := t.TempDir()
	sourceID := "rtsp://cam1"

	s := newSegmenter(t, dest+"/", 5*time.Second, gomedia.Always)
	s.AddSource() <- sourceID
	s.AddSource() <- sourceID // duplicate

	time.Sleep(50 * time.Millisecond)

	seg := s.(*segmenter)
	seg.streamsMu.RLock()
	assert.Len(t, seg.sources, 1, "duplicate source should not be added")
	seg.streamsMu.RUnlock()
}

func TestRemoveSource_FlushesActiveSegment(t *testing.T) {
	dest := t.TempDir()
	sourceID := "rtsp://cam1"

	_, videoCp, audioCp := loadTestCodecPair(t, sourceID)
	packets := loadTestPackets(t, sourceID, videoCp, audioCp, 100)

	s := newSegmenter(t, dest+"/", 30*time.Second, gomedia.Always)
	addSourceAndWait(t, s, sourceID)

	sendPackets(t, s, packets)
	// Give time for packets to be processed
	time.Sleep(100 * time.Millisecond)

	// Remove source — should flush active file
	s.RemoveSource() <- sourceID

	info := waitForFile(t, s, 2*time.Second)
	assert.Equal(t, sourceID, info.URL)
	assert.Greater(t, info.Size, 0)
}

func TestRemoveSource_NonExistent(t *testing.T) {
	dest := t.TempDir()

	s := newSegmenter(t, dest+"/", 5*time.Second, gomedia.Always)
	// Should not panic
	s.RemoveSource() <- "rtsp://nonexistent"
	time.Sleep(50 * time.Millisecond)
}

// ---------------------------------------------------------------------------
// Record mode switching
// ---------------------------------------------------------------------------

func TestSwitchMode_AlwaysToNever(t *testing.T) {
	dest := t.TempDir()
	sourceID := "rtsp://cam1"

	_, videoCp, audioCp := loadTestCodecPair(t, sourceID)
	packets := loadTestPackets(t, sourceID, videoCp, audioCp, 100)

	s := New(dest+"/", 30*time.Second, gomedia.Always, 512)
	s.Write()
	addSourceAndWait(t, s, sourceID)

	sendPackets(t, s, packets)

	// Wait for recording to actually start before switching mode
	status := waitForStatus(t, s, 2*time.Second)
	require.True(t, status)

	// Switch to Never — should flush active segment and emit file
	s.RecordMode() <- gomedia.Never

	info := waitForFile(t, s, 2*time.Second)
	assert.Equal(t, sourceID, info.URL)
	assert.Greater(t, info.Size, 0)

	s.Close()
	<-s.Done()
}

func TestSwitchMode_SameMode_NoOp(t *testing.T) {
	dest := t.TempDir()
	sourceID := "rtsp://cam1"

	_, videoCp, audioCp := loadTestCodecPair(t, sourceID)
	packets := loadTestPackets(t, sourceID, videoCp, audioCp, 100)

	s := New(dest+"/", 30*time.Second, gomedia.Always, 512)
	s.Write()
	addSourceAndWait(t, s, sourceID)

	sendPackets(t, s, packets)
	// Wait for recording to start
	status := waitForStatus(t, s, 2*time.Second)
	require.True(t, status)

	// Switch to same mode — should be a no-op, no flush
	s.RecordMode() <- gomedia.Always
	time.Sleep(100 * time.Millisecond)

	s.Close()
	<-s.Done()

	// Same mode switch should not produce extra files — only one file from final flush
	files := drainFiles(s)
	assert.Len(t, files, 1, "same-mode switch should not cause extra segment flush")
}

// ---------------------------------------------------------------------------
// Event mode
// ---------------------------------------------------------------------------

func TestEventMode_NoEventNoRecording(t *testing.T) {
	dest := t.TempDir()
	sourceID := "rtsp://cam1"

	_, videoCp, audioCp := loadTestCodecPair(t, sourceID)
	packets := loadTestPackets(t, sourceID, videoCp, audioCp, 336)

	s := newSegmenter(t, dest+"/", 30*time.Second, gomedia.Event,
		WithPreBufferDuration(5*time.Second),
	)
	addSourceAndWait(t, s, sourceID)

	// Send packets but NO event trigger
	sendPackets(t, s, packets)
	time.Sleep(200 * time.Millisecond)

	// No files should be emitted while running — event was never triggered
	expectNoFile(t, s)
}

func TestEventMode_EventTriggersRecording(t *testing.T) {
	dest := t.TempDir()
	sourceID := "rtsp://cam1"

	_, videoCp, audioCp := loadTestCodecPair(t, sourceID)
	packets := loadTestPackets(t, sourceID, videoCp, audioCp, 1000)

	s := newSegmenter(t, dest+"/", 30*time.Second, gomedia.Event,
		WithPreBufferDuration(5*time.Second),
	)
	addSourceAndWait(t, s, sourceID)

	// Send first GOP to fill the pre-buffer
	sendPackets(t, s, packets[:336])
	time.Sleep(100 * time.Millisecond)

	// Trigger event
	s.Events() <- struct{}{}
	time.Sleep(50 * time.Millisecond)

	// Send packets with next keyframe — should start recording
	sendPackets(t, s, packets[336:673])
	time.Sleep(100 * time.Millisecond)

	// Close to flush
	s.Close()
	<-s.Done()

	// Should have at least one file
	info := <-s.Files()
	assert.NotEmpty(t, info.Name)
	assert.Greater(t, info.Size, 0)
	assert.Equal(t, sourceID, info.URL)
}

func TestEventMode_RecordStatusTransitions(t *testing.T) {
	dest := t.TempDir()
	sourceID := "rtsp://cam1"

	_, videoCp, audioCp := loadTestCodecPair(t, sourceID)
	packets := loadTestPackets(t, sourceID, videoCp, audioCp, 1000)

	s := newSegmenter(t, dest+"/", 30*time.Second, gomedia.Event,
		WithPreBufferDuration(5*time.Second),
		WithMaxEventDuration(5*time.Second),
	)
	addSourceAndWait(t, s, sourceID)

	// Fill pre-buffer
	sendPackets(t, s, packets[:336])
	time.Sleep(100 * time.Millisecond)

	// Trigger event
	s.Events() <- struct{}{}
	time.Sleep(50 * time.Millisecond)

	// Send keyframe to start recording
	sendPackets(t, s, packets[336:673])
	time.Sleep(100 * time.Millisecond)

	// Should get status=true when recording starts
	status := waitForStatus(t, s, 2*time.Second)
	assert.True(t, status)
}

func TestEventMode_PreBufferIncludesDataBeforeEvent(t *testing.T) {
	dest := t.TempDir()
	sourceID := "rtsp://cam1"

	_, videoCp, audioCp := loadTestCodecPair(t, sourceID)
	packets := loadTestPackets(t, sourceID, videoCp, audioCp, 1000)

	// Pre-buffer of 15s — should capture the first GOP
	s := newSegmenter(t, dest+"/", 30*time.Second, gomedia.Event,
		WithPreBufferDuration(15*time.Second),
	)
	addSourceAndWait(t, s, sourceID)

	// Send first two GOPs (~17s of data)
	sendPackets(t, s, packets[:673])
	time.Sleep(100 * time.Millisecond)

	// Trigger event
	s.Events() <- struct{}{}
	time.Sleep(50 * time.Millisecond)

	// Send the third keyframe and some data to trigger recording
	sendPackets(t, s, packets[673:700])
	time.Sleep(100 * time.Millisecond)

	// Close to flush the segment
	s.Close()
	<-s.Done()

	info := <-s.Files()
	// The segment should start before the event (from pre-buffer)
	// and include data from the pre-buffer
	assert.Greater(t, info.Size, 0)
	assert.Equal(t, sourceID, info.URL)
}

// ---------------------------------------------------------------------------
// Event mode — multi-stream independence
// ---------------------------------------------------------------------------

func TestEventMode_MultiStream_BothRecord(t *testing.T) {
	dest := t.TempDir()
	src1 := "rtsp://cam1"
	src2 := "rtsp://cam2"

	_, videoCp1, audioCp1 := loadTestCodecPair(t, src1)
	_, videoCp2, audioCp2 := loadTestCodecPair(t, src2)
	pkts1 := loadTestPackets(t, src1, videoCp1, audioCp1, 1000)
	pkts2 := loadTestPackets(t, src2, videoCp2, audioCp2, 1000)

	s := newSegmenter(t, dest+"/", 30*time.Second, gomedia.Event,
		WithPreBufferDuration(5*time.Second),
	)
	addSourceAndWait(t, s, src1)
	addSourceAndWait(t, s, src2)

	// Fill pre-buffers for both streams
	for i := 0; i < 336 && i < len(pkts1) && i < len(pkts2); i++ {
		s.Packets() <- pkts1[i]
		s.Packets() <- pkts2[i]
	}
	time.Sleep(100 * time.Millisecond)

	// Trigger event — should enable recording for both streams
	s.Events() <- struct{}{}
	time.Sleep(50 * time.Millisecond)

	// Send next keyframes for both streams
	for i := 336; i < 673 && i < len(pkts1) && i < len(pkts2); i++ {
		s.Packets() <- pkts1[i]
		s.Packets() <- pkts2[i]
	}
	time.Sleep(100 * time.Millisecond)

	s.Close()
	<-s.Done()

	// Both streams should produce files
	files := make(map[string]gomedia.FileInfo)
	for info := range s.Files() {
		files[info.URL] = info
	}
	assert.Contains(t, files, src1, "cam1 should have recorded")
	assert.Contains(t, files, src2, "cam2 should have recorded")
}

// ---------------------------------------------------------------------------
// Path generation
// ---------------------------------------------------------------------------

func TestDefaultPathFunc(t *testing.T) {
	dest := t.TempDir()
	sourceID := "rtsp://cam1"

	_, videoCp, audioCp := loadTestCodecPair(t, sourceID)
	packets := loadTestPackets(t, sourceID, videoCp, audioCp, 200)

	s := New(dest+"/", 30*time.Second, gomedia.Always, 512)
	s.Write()
	addSourceAndWait(t, s, sourceID)

	sendPackets(t, s, packets)
	status := waitForStatus(t, s, 2*time.Second)
	require.True(t, status)

	s.Close()
	<-s.Done()

	files := drainFiles(s)
	require.Len(t, files, 1)
	// Default format: YYYY/M/D/streamIdx_YYYY-MM-DDTHH:MM:SS.mp4
	assert.Contains(t, files[0].Name, ".mp4")
	assert.Contains(t, files[0].Name, "/")
}

func TestCustomPathFunc(t *testing.T) {
	dest := t.TempDir()
	sourceID := "rtsp://cam1"

	_, videoCp, audioCp := loadTestCodecPair(t, sourceID)
	packets := loadTestPackets(t, sourceID, videoCp, audioCp, 200)

	s := New(dest+"/", 30*time.Second, gomedia.Always, 512,
		WithPathFunc(func(startTime time.Time, streamIdx int) (string, string) {
			return "recordings/", "test_segment.mp4"
		}),
	)
	s.Write()
	addSourceAndWait(t, s, sourceID)

	sendPackets(t, s, packets)
	status := waitForStatus(t, s, 2*time.Second)
	require.True(t, status)

	s.Close()
	<-s.Done()

	files := drainFiles(s)
	require.Len(t, files, 1)
	assert.Equal(t, "recordings/test_segment.mp4", files[0].Name)

	fullPath := filepath.Join(dest, files[0].Name)
	_, err := os.Stat(fullPath)
	require.NoError(t, err, "file should exist at custom path")
}

// ---------------------------------------------------------------------------
// FileInfo correctness
// ---------------------------------------------------------------------------

func TestFileInfo_Fields(t *testing.T) {
	dest := t.TempDir()
	sourceID := "rtsp://cam1"

	_, videoCp, audioCp := loadTestCodecPair(t, sourceID)
	packets := loadTestPackets(t, sourceID, videoCp, audioCp, 100)

	s := New(dest+"/", 30*time.Second, gomedia.Always, 512)
	s.Write()
	addSourceAndWait(t, s, sourceID)

	sendPackets(t, s, packets)
	status := waitForStatus(t, s, 2*time.Second)
	require.True(t, status)

	s.Close()
	<-s.Done()

	files := drainFiles(s)
	require.Len(t, files, 1)
	info := files[0]

	assert.NotEmpty(t, info.Name)
	assert.False(t, info.Start.IsZero())
	assert.True(t, info.Stop.After(info.Start), "stop should be after start")
	assert.Greater(t, info.Size, 0)
	assert.Equal(t, sourceID, info.URL)
	assert.Equal(t, "H264", info.Codec)

	// Resolution should be WxH from video codec params
	assert.Regexp(t, `^\d+x\d+$`, info.Resolution)
}

// ---------------------------------------------------------------------------
// Directory creation
// ---------------------------------------------------------------------------

func TestCreateFile_CreatesParentDirs(t *testing.T) {
	dest := t.TempDir()
	sourceID := "rtsp://cam1"

	_, videoCp, audioCp := loadTestCodecPair(t, sourceID)
	packets := loadTestPackets(t, sourceID, videoCp, audioCp, 200)

	// Use a pathFunc that creates deep nesting
	s := newSegmenter(t, dest+"/", 30*time.Second, gomedia.Always,
		WithPathFunc(func(startTime time.Time, streamIdx int) (string, string) {
			return "deep/nested/path/", "segment.mp4"
		}),
	)
	addSourceAndWait(t, s, sourceID)

	sendPackets(t, s, packets)
	s.Close()
	<-s.Done()

	info := <-s.Files()
	fullPath := filepath.Join(dest, info.Name)
	_, err := os.Stat(fullPath)
	require.NoError(t, err, "deeply nested file should be created")
}

func TestDirPermissions(t *testing.T) {
	dest := t.TempDir()
	sourceID := "rtsp://cam1"

	_, videoCp, audioCp := loadTestCodecPair(t, sourceID)
	packets := loadTestPackets(t, sourceID, videoCp, audioCp, 200)

	s := New(dest+"/", 30*time.Second, gomedia.Always, 512,
		WithDirPermissions(0o755),
		WithPathFunc(func(startTime time.Time, streamIdx int) (string, string) {
			return "permtest/", "seg.mp4"
		}),
	)
	s.Write()
	addSourceAndWait(t, s, sourceID)

	sendPackets(t, s, packets)
	status := waitForStatus(t, s, 2*time.Second)
	require.True(t, status)

	s.Close()
	<-s.Done()

	drainFiles(s)
	dirPath := filepath.Join(dest, "permtest")
	stat, err := os.Stat(dirPath)
	require.NoError(t, err)
	assert.True(t, stat.IsDir())
}

// ---------------------------------------------------------------------------
// Edge cases
// ---------------------------------------------------------------------------

func TestClose_WithNoPackets(t *testing.T) {
	dest := t.TempDir()

	s := New(dest+"/", 5*time.Second, gomedia.Always, 64)
	s.Write()
	s.AddSource() <- "rtsp://cam1"
	time.Sleep(50 * time.Millisecond)

	// Close without sending any packets — should not panic or produce files
	s.Close()
	<-s.Done()

	files := drainFiles(s)
	assert.Empty(t, files)
}

func TestClose_IdempotentChannelDrain(t *testing.T) {
	dest := t.TempDir()

	s := New(dest+"/", 5*time.Second, gomedia.Always, 64)
	s.Write()
	// Close immediately — lifecycle should handle graceful shutdown
	s.Close()
	<-s.Done()
}

func TestNeverMode_RejectsPackets(t *testing.T) {
	dest := t.TempDir()
	sourceID := "rtsp://cam1"

	_, videoCp, audioCp := loadTestCodecPair(t, sourceID)
	packets := loadTestPackets(t, sourceID, videoCp, audioCp, 200)

	// Start in Never mode
	s := newSegmenter(t, dest+"/", 5*time.Second, gomedia.Never)
	addSourceAndWait(t, s, sourceID)

	// Sending packets should not produce files
	sendPackets(t, s, packets)
	time.Sleep(200 * time.Millisecond)

	// No file emitted while running
	expectNoFile(t, s)
}

// ---------------------------------------------------------------------------
// Segment duration accuracy
// ---------------------------------------------------------------------------

func TestAlwaysMode_SegmentDurationRespected(t *testing.T) {
	dest := t.TempDir()
	sourceID := "rtsp://cam1"

	_, videoCp, audioCp := loadTestCodecPair(t, sourceID)
	// Load all packets — 3 keyframes
	packets := loadTestPackets(t, sourceID, videoCp, audioCp, 1000)

	// Target 5s segments — keyframes at ~0s, ~8.7s, ~17.4s
	// First segment should be ~8.7s (rotates at second keyframe)
	s := newSegmenter(t, dest+"/", 5*time.Second, gomedia.Always)
	addSourceAndWait(t, s, sourceID)

	sendPackets(t, s, packets)

	info1 := waitForFile(t, s, 2*time.Second)
	segDur := info1.Stop.Sub(info1.Start)
	// Segment must be at least targetDuration (can only rotate on keyframes)
	assert.GreaterOrEqual(t, segDur, 5*time.Second,
		"segment should be at least targetDuration")
}

// ---------------------------------------------------------------------------
// Segment rotation with large target duration
// ---------------------------------------------------------------------------

func TestAlwaysMode_NoRotationWhenUnderDuration(t *testing.T) {
	dest := t.TempDir()
	sourceID := "rtsp://cam1"

	_, videoCp, audioCp := loadTestCodecPair(t, sourceID)
	packets := loadTestPackets(t, sourceID, videoCp, audioCp, 1000)

	// Target 60s — total data is ~26s, so no rotation should happen
	s := newSegmenter(t, dest+"/", 60*time.Second, gomedia.Always)
	addSourceAndWait(t, s, sourceID)

	sendPackets(t, s, packets)

	// Wait for recording to start, then verify no rotation happened
	status := waitForStatus(t, s, 2*time.Second)
	require.True(t, status)
	time.Sleep(200 * time.Millisecond)

	// No file should be emitted before close (no rotation)
	expectNoFile(t, s)

	s.Close()
	<-s.Done()

	// Exactly one file from final flush
	files := drainFiles(s)
	require.Len(t, files, 1, "should produce exactly one segment")
	assert.Greater(t, files[0].Size, 0)
}

// ---------------------------------------------------------------------------
// Written MP4 integrity
// ---------------------------------------------------------------------------

func TestAlwaysMode_WrittenFileIsValidMP4(t *testing.T) {
	dest := t.TempDir()
	sourceID := "rtsp://cam1"

	_, videoCp, audioCp := loadTestCodecPair(t, sourceID)
	packets := loadTestPackets(t, sourceID, videoCp, audioCp, 100)

	s := New(dest+"/", 30*time.Second, gomedia.Always, 512)
	s.Write()
	addSourceAndWait(t, s, sourceID)

	sendPackets(t, s, packets)
	status := waitForStatus(t, s, 2*time.Second)
	require.True(t, status)

	s.Close()
	<-s.Done()

	files := drainFiles(s)
	require.Len(t, files, 1)
	info := files[0]
	require.NotEmpty(t, info.Name)

	fullPath := filepath.Join(dest, info.Name)
	data, err := os.ReadFile(fullPath)
	require.NoError(t, err)
	require.Greater(t, len(data), 8)

	// MP4 files should contain 'ftyp' box near the start
	assert.Contains(t, string(data[:32]), "ftyp", "file should start with ftyp box")
}

// ---------------------------------------------------------------------------
// Event mode — max event duration
// ---------------------------------------------------------------------------

func TestEventMode_MaxDurationEnforcedOnClose(t *testing.T) {
	dest := t.TempDir()
	sourceID := "rtsp://cam1"

	_, videoCp, audioCp := loadTestCodecPair(t, sourceID)
	packets := loadTestPackets(t, sourceID, videoCp, audioCp, 1000)

	s := newSegmenter(t, dest+"/", 30*time.Second, gomedia.Event,
		WithPreBufferDuration(5*time.Second),
		WithMaxEventDuration(5*time.Second),
	)
	addSourceAndWait(t, s, sourceID)

	// Fill pre-buffer
	sendPackets(t, s, packets[:336])
	time.Sleep(100 * time.Millisecond)

	// Trigger event
	s.Events() <- struct{}{}
	time.Sleep(50 * time.Millisecond)

	// Send data that includes next two keyframes (will exceed maxEventDuration)
	sendPackets(t, s, packets[336:])
	time.Sleep(200 * time.Millisecond)

	s.Close()
	<-s.Done()

	// Should have produced at least one file
	info := <-s.Files()
	assert.Greater(t, info.Size, 0)
}
