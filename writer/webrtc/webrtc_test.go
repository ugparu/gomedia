//nolint:mnd // Test file contains many magic numbers for expected values
package webrtc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/utils/logger"
)

// ---------------------------------------------------------------------------
// Constructor tests
// ---------------------------------------------------------------------------

func TestNew_ReturnsNonNil(t *testing.T) {
	w := New(64, 5*time.Second)
	require.NotNil(t, w)
}

func TestNew_WithOptions(t *testing.T) {
	w := New(64, 5*time.Second,
		WithLogger(logger.Default),
		WithSignalingHandler(&DefaultSignalingHandler{}),
	)
	require.NotNil(t, w)
}

func TestNew_ChannelsAreAccessible(t *testing.T) {
	w := New(64, 5*time.Second)
	assert.NotNil(t, w.Packets())
	assert.NotNil(t, w.Peers())
	assert.NotNil(t, w.AddSource())
	assert.NotNil(t, w.RemoveSource())
}

// ---------------------------------------------------------------------------
// Lifecycle tests
// ---------------------------------------------------------------------------

func TestWriter_WriteAndClose(t *testing.T) {
	w := New(64, 5*time.Second)
	w.Write()

	done := make(chan struct{})
	go func() {
		w.Close()
		<-w.Done()
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for writer to close")
	}
}

func TestWriter_DoubleWriteDoesNotPanic(t *testing.T) {
	w := New(64, 5*time.Second)
	w.Write()
	w.Write() // FailSafeAsyncManager should handle this gracefully
	w.Close()
	<-w.Done()
}

func TestWriter_CloseWithoutWriteDoesNotPanic(t *testing.T) {
	w := New(64, 5*time.Second)
	w.Close()
	<-w.Done()
}

// ---------------------------------------------------------------------------
// SortedResolutions tests
// ---------------------------------------------------------------------------

func TestWriter_SortedResolutions_EmptyInitially(t *testing.T) {
	w := New(64, 5*time.Second)
	w.Write()
	defer func() { w.Close(); <-w.Done() }()

	codec := w.SortedResolutions()
	assert.NotNil(t, codec)
	assert.Len(t, codec.Resolutions, 0)
	assert.False(t, codec.HasAudio)
}

// ---------------------------------------------------------------------------
// Source management tests
// ---------------------------------------------------------------------------

func newTestWriter(t *testing.T) gomedia.WebRTCStreamer {
	t.Helper()
	w := New(64, 5*time.Second, WithLogger(logger.Default))
	w.Write()
	t.Cleanup(func() {
		w.Close()
		<-w.Done()
	})
	return w
}

func TestWriter_AddSource_CreatesStreamOnFirstPacket(t *testing.T) {
	w := newTestWriter(t)
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://cam1")

	w.AddSource() <- "rtsp://cam1"

	// Give the event loop time to process
	time.Sleep(50 * time.Millisecond)

	// Stream isn't created until codec params arrive with the first packet
	absTime := time.Now()
	pkt := makeVideoPacket(t, videoCp, "rtsp://cam1", true, 0, 33*time.Millisecond, absTime)
	w.Packets() <- pkt

	// Wait for processing
	time.Sleep(50 * time.Millisecond)

	resolutions := w.SortedResolutions()
	assert.Len(t, resolutions.Resolutions, 1)
	assert.Equal(t, "rtsp://cam1", resolutions.Resolutions[0].URL)
}

func TestWriter_RemoveSource(t *testing.T) {
	w := newTestWriter(t)
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://cam1")

	w.AddSource() <- "rtsp://cam1"
	time.Sleep(50 * time.Millisecond)

	// Send packet to create stream
	absTime := time.Now()
	pkt := makeVideoPacket(t, videoCp, "rtsp://cam1", true, 0, 33*time.Millisecond, absTime)
	w.Packets() <- pkt
	time.Sleep(50 * time.Millisecond)

	assert.Len(t, w.SortedResolutions().Resolutions, 1)

	// Remove source
	w.RemoveSource() <- "rtsp://cam1"
	time.Sleep(50 * time.Millisecond)

	assert.Len(t, w.SortedResolutions().Resolutions, 0)
}

func TestWriter_MultipleSources(t *testing.T) {
	w := newTestWriter(t)
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://cam1")

	w.AddSource() <- "rtsp://cam1"
	w.AddSource() <- "rtsp://cam2"
	time.Sleep(50 * time.Millisecond)

	absTime := time.Now()

	pkt1 := makeVideoPacket(t, videoCp, "rtsp://cam1", true, 0, 33*time.Millisecond, absTime)
	w.Packets() <- pkt1

	pkt2 := makeVideoPacket(t, videoCp, "rtsp://cam2", true, 0, 33*time.Millisecond, absTime)
	w.Packets() <- pkt2
	time.Sleep(50 * time.Millisecond)

	resolutions := w.SortedResolutions()
	assert.Len(t, resolutions.Resolutions, 2)
}

func TestWriter_DuplicateSourceIgnored(t *testing.T) {
	w := newTestWriter(t)

	w.AddSource() <- "rtsp://cam1"
	w.AddSource() <- "rtsp://cam1" // duplicate
	time.Sleep(50 * time.Millisecond)

	// Should not cause issues - just one source registered
}

func TestWriter_PacketsFromUnregisteredSourceIgnored(t *testing.T) {
	w := newTestWriter(t)
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://cam1")

	// Don't add source, just send packet
	absTime := time.Now()
	pkt := makeVideoPacket(t, videoCp, "rtsp://unregistered", true, 0, 33*time.Millisecond, absTime)
	w.Packets() <- pkt
	time.Sleep(50 * time.Millisecond)

	assert.Len(t, w.SortedResolutions().Resolutions, 0)
}

// ---------------------------------------------------------------------------
// Peer connection tests (without real WebRTC - testing error paths)
// ---------------------------------------------------------------------------

func TestWriter_PeerWithEmptyTargetURL(t *testing.T) {
	w := newTestWriter(t)

	peer := &gomedia.WebRTCPeer{
		SDP:       "",
		TargetURL: "",
		Done:      make(chan struct{}),
	}
	w.Peers() <- peer

	select {
	case <-peer.Done:
		assert.Error(t, peer.Err)
		assert.Contains(t, peer.Err.Error(), "target URL is empty")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for peer error")
	}
}

func TestWriter_PeerWithNonexistentStream(t *testing.T) {
	w := newTestWriter(t)

	peer := &gomedia.WebRTCPeer{
		SDP:       "",
		TargetURL: "rtsp://nonexistent",
		Done:      make(chan struct{}),
	}
	w.Peers() <- peer

	select {
	case <-peer.Done:
		assert.Error(t, peer.Err)
		assert.Contains(t, peer.Err.Error(), "target stream not found")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for peer error")
	}
}

// ---------------------------------------------------------------------------
// extractFmtpLineFromSDP tests
// ---------------------------------------------------------------------------

func TestExtractFmtpLineFromSDP_H264(t *testing.T) {
	sdp := `v=0
o=- 0 0 IN IP4 127.0.0.1
s=-
t=0 0
m=video 9 UDP/TLS/RTP/SAVPF 96 102
a=rtpmap:96 VP8/90000
a=rtpmap:102 H264/90000
a=fmtp:102 level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f
a=fmtp:96 max-fs=1200`

	result := extractFmtpLineFromSDP(sdp, gomedia.H264)
	assert.Equal(t, "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f", result)
}

func TestExtractFmtpLineFromSDP_H265(t *testing.T) {
	sdp := `v=0
m=video 9 UDP/TLS/RTP/SAVPF 45
a=rtpmap:45 H265/90000
a=fmtp:45 level-id=180;profile-id=1;tier-flag=0`

	result := extractFmtpLineFromSDP(sdp, gomedia.H265)
	assert.Equal(t, "level-id=180;profile-id=1;tier-flag=0", result)
}

func TestExtractFmtpLineFromSDP_NoMatch(t *testing.T) {
	sdp := `v=0
m=video 9 UDP/TLS/RTP/SAVPF 96
a=rtpmap:96 VP8/90000`

	result := extractFmtpLineFromSDP(sdp, gomedia.H264)
	assert.Empty(t, result)
}

func TestExtractFmtpLineFromSDP_NoFmtpLine(t *testing.T) {
	sdp := `v=0
m=video 9 UDP/TLS/RTP/SAVPF 102
a=rtpmap:102 H264/90000`

	result := extractFmtpLineFromSDP(sdp, gomedia.H264)
	assert.Empty(t, result)
}

func TestExtractFmtpLineFromSDP_UnsupportedCodec(t *testing.T) {
	result := extractFmtpLineFromSDP("", gomedia.VP8)
	assert.Empty(t, result)
}

// ---------------------------------------------------------------------------
// Packet flow with real test data
// ---------------------------------------------------------------------------

func TestWriter_RealDataFlow(t *testing.T) {
	w := newTestWriter(t)
	_, videoCp, audioCp := loadTestCodecPair(t, "rtsp://test")

	w.AddSource() <- "rtsp://test"
	time.Sleep(50 * time.Millisecond)

	packets := loadTestPackets(t, "rtsp://test", videoCp, audioCp, 100)
	for _, pkt := range packets {
		w.Packets() <- pkt
	}
	time.Sleep(100 * time.Millisecond)

	resolutions := w.SortedResolutions()
	assert.Len(t, resolutions.Resolutions, 1)
	assert.Greater(t, resolutions.Resolutions[0].Width, 0)
	assert.Greater(t, resolutions.Resolutions[0].Height, 0)
}

func TestWriter_RemoveAndReAddSource(t *testing.T) {
	w := newTestWriter(t)
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://cam1")

	w.AddSource() <- "rtsp://cam1"
	time.Sleep(50 * time.Millisecond)

	absTime := time.Now()
	pkt := makeVideoPacket(t, videoCp, "rtsp://cam1", true, 0, 33*time.Millisecond, absTime)
	w.Packets() <- pkt
	time.Sleep(50 * time.Millisecond)

	assert.Len(t, w.SortedResolutions().Resolutions, 1)

	// Remove
	w.RemoveSource() <- "rtsp://cam1"
	time.Sleep(50 * time.Millisecond)
	assert.Len(t, w.SortedResolutions().Resolutions, 0)

	// Re-add
	w.AddSource() <- "rtsp://cam1"
	time.Sleep(50 * time.Millisecond)

	pkt2 := makeVideoPacket(t, videoCp, "rtsp://cam1", true, 100*time.Millisecond, 33*time.Millisecond, absTime.Add(100*time.Millisecond))
	w.Packets() <- pkt2
	time.Sleep(50 * time.Millisecond)

	assert.Len(t, w.SortedResolutions().Resolutions, 1)
}

func TestWriter_HasAudioFlagSet(t *testing.T) {
	w := newTestWriter(t)
	_, videoCp, audioCp := loadTestCodecPair(t, "rtsp://cam1")

	w.AddSource() <- "rtsp://cam1"
	time.Sleep(50 * time.Millisecond)

	absTime := time.Now()

	// Send video first to create stream
	vPkt := makeVideoPacket(t, videoCp, "rtsp://cam1", true, 0, 33*time.Millisecond, absTime)
	w.Packets() <- vPkt
	time.Sleep(50 * time.Millisecond)

	// Send audio to set audio codec params
	aPkt := makeAudioPacket(t, audioCp, "rtsp://cam1", 0, 21*time.Millisecond, absTime)
	w.Packets() <- aPkt
	time.Sleep(50 * time.Millisecond)

	codec := w.SortedResolutions()
	assert.True(t, codec.HasAudio)
}
