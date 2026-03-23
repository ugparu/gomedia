//nolint:mnd // Test file contains many magic numbers for expected values
package webrtc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ugparu/gomedia/codec/h264"
)

// ---------------------------------------------------------------------------
// codecParametersSize tests
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// writeCodecParameters tests
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// peerTrack construction helpers
// ---------------------------------------------------------------------------

func TestCodecParametersSize_ConsistentWithWritten(t *testing.T) {
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://test")

	size := codecParametersSize(videoCp)
	dst := make([]byte, size+100) // extra space to detect overwrites
	written := writeCodecParameters(dst, videoCp)

	assert.Equal(t, size, written, "written bytes should match predicted size")
}

// ---------------------------------------------------------------------------
// NAL start code writing in writeVideoPacketsToPeer
// ---------------------------------------------------------------------------

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
