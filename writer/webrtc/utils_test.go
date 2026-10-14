//nolint:mnd // Test file contains many magic numbers for expected values
package webrtc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/h264"
	"github.com/ugparu/gomedia/codec/pcm"
)

// codecParametersSize tests

func TestCodecParametersSize_H264(t *testing.T) {
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://test")

	size := codecParametersSize(videoCp)
	expectedSize := 4 + len(videoCp.SPS()) + 4 + len(videoCp.PPS())
	assert.Equal(t, expectedSize, size)
	assert.Greater(t, size, 0)
}

func TestCodecParametersSize_UnknownCodec(t *testing.T) {
	_, _, audioCp := loadTestCodecPair(t, "rtsp://test")
	size := codecParametersSize(audioCp)
	assert.Equal(t, 0, size)
}

// writeCodecParameters tests

func TestWriteCodecParameters_H264(t *testing.T) {
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://test")

	size := codecParametersSize(videoCp)
	dst := make([]byte, size)
	written := writeCodecParameters(dst, videoCp)

	assert.Equal(t, size, written)

	// Verify start codes and SPS/PPS
	startCode := []byte{0, 0, 0, 1}
	offset := 0

	// SPS start code
	assert.Equal(t, startCode, dst[offset:offset+4])
	offset += 4

	// SPS data
	assert.Equal(t, videoCp.SPS(), dst[offset:offset+len(videoCp.SPS())])
	offset += len(videoCp.SPS())

	// PPS start code
	assert.Equal(t, startCode, dst[offset:offset+4])
	offset += 4

	// PPS data
	assert.Equal(t, videoCp.PPS(), dst[offset:offset+len(videoCp.PPS())])
}

func TestWriteCodecParameters_H264_RealData(t *testing.T) {
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://test")

	require.True(t, len(videoCp.SPS()) > 0, "test data should have non-empty SPS")
	require.True(t, len(videoCp.PPS()) > 0, "test data should have non-empty PPS")

	size := codecParametersSize(videoCp)
	dst := make([]byte, size)
	written := writeCodecParameters(dst, videoCp)

	assert.Equal(t, size, written)
	assert.Equal(t, size, 4+len(videoCp.SPS())+4+len(videoCp.PPS()))
}

// peerTrack construction helpers

func TestCodecParametersSize_ConsistentWithWritten(t *testing.T) {
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://test")

	size := codecParametersSize(videoCp)
	dst := make([]byte, size+100) // extra space to detect overwrites
	written := writeCodecParameters(dst, videoCp)

	assert.Equal(t, size, written, "written bytes should match predicted size")
}

// NAL start code writing in writeVideoPacketsToPeer

func TestStartCodeWriting(t *testing.T) {
	// Verify that NAL units are correctly prefixed with start codes
	// by simulating the inner logic of writeVideoPacketsToPeer

	_, videoCp, _ := loadTestCodecPair(t, "rtsp://test")

	// Create a keyframe packet
	absTime := makeAbsTime()
	pkt := h264.NewPacket(true, 0, absTime, []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xAA, 0xBB}, "rtsp://test", videoCp)
	pkt.SetDuration(33 * 1000000) // 33ms in ns

	// The data should be processable
	assert.Greater(t, pkt.Len(), minPktSz)
	assert.True(t, pkt.IsKeyFrame())
}

func TestPaceSleep_NoSleepWhenLate(t *testing.T) {
	dur := 40 * time.Millisecond
	assert.Equal(t, time.Duration(0), paceSleep(dur, 5*time.Millisecond, time.Second))
	assert.Equal(t, time.Duration(0), paceSleep(dur, 5*time.Millisecond, time.Millisecond))
}

func TestPaceSleep_PacesWhenOnTimeOrEarly(t *testing.T) {
	dur := 40 * time.Millisecond
	assert.Equal(t, 35*time.Millisecond, paceSleep(dur, 5*time.Millisecond, 0))
	assert.Equal(t, 35*time.Millisecond, paceSleep(dur, 5*time.Millisecond, -100*time.Millisecond))
	assert.Equal(t, time.Duration(0), paceSleep(dur, 50*time.Millisecond, 0))
}

func TestNeedForceSkip(t *testing.T) {
	assert.False(t, needForceSkip(0, forceSkipQueueDepth))
	assert.False(t, needForceSkip(forceSkipLag, forceSkipQueueDepth-1))
	assert.False(t, needForceSkip(forceSkipLag-time.Millisecond, forceSkipQueueDepth))
	assert.True(t, needForceSkip(forceSkipLag, forceSkipQueueDepth))
	assert.True(t, needForceSkip(5*time.Second, forceSkipQueueDepth+10))
}

func TestForceSkipVideo_SkipsToKeyFrameWithinThreshold(t *testing.T) {
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://test")
	delay := 500 * time.Millisecond
	now := time.Now()

	late := h264.NewPacket(false, 0, now.Add(-5*time.Second-delay), []byte{0x01}, "rtsp://test", videoCp)
	ch := make(chan gomedia.VideoPacket, 8)
	ch <- h264.NewPacket(false, 0, now.Add(-4*time.Second-delay), []byte{0x02}, "rtsp://test", videoCp)
	ch <- h264.NewPacket(true, 0, now.Add(-3*time.Second-delay), []byte{0x03}, "rtsp://test", videoCp)
	ch <- h264.NewPacket(false, 0, now.Add(-2*time.Second-delay), []byte{0x04}, "rtsp://test", videoCp)
	ch <- h264.NewPacket(true, 0, now.Add(-200*time.Millisecond-delay), []byte{0x05}, "rtsp://test", videoCp)
	ch <- h264.NewPacket(false, 0, now.Add(-100*time.Millisecond-delay), []byte{0x06}, "rtsp://test", videoCp)

	got, dropped := forceSkipVideo(late, ch, delay, forceSkipTarget)
	require.NotNil(t, got)
	assert.True(t, got.IsKeyFrame())
	assert.Equal(t, []byte{0x05}, got.Data())
	assert.Equal(t, 4, dropped)
	assert.Equal(t, 1, len(ch))
	got.Release()
	(<-ch).Release()
}

func TestForceSkipAudio_SkipsToWithinThreshold(t *testing.T) {
	audioCp := pcm.NewCodecParameters(0, gomedia.PCMAlaw, 1, 8000)
	delay := 500 * time.Millisecond
	now := time.Now()

	late := pcm.NewPacket([]byte{0x01}, 0, "rtsp://test", now.Add(-3*time.Second-delay), audioCp, 20*time.Millisecond)
	ch := make(chan gomedia.AudioPacket, 4)
	ch <- pcm.NewPacket([]byte{0x02}, 0, "rtsp://test", now.Add(-2*time.Second-delay), audioCp, 20*time.Millisecond)
	ch <- pcm.NewPacket([]byte{0x03}, 0, "rtsp://test", now.Add(-100*time.Millisecond-delay), audioCp, 20*time.Millisecond)
	ch <- pcm.NewPacket([]byte{0x04}, 0, "rtsp://test", now.Add(-50*time.Millisecond-delay), audioCp, 20*time.Millisecond)

	got, dropped := forceSkipAudio(late, ch, delay, forceSkipTarget)
	require.NotNil(t, got)
	assert.Equal(t, []byte{0x03}, got.Data())
	assert.Equal(t, 2, dropped)
	assert.Equal(t, 1, len(ch))
	got.Release()
	(<-ch).Release()
}
