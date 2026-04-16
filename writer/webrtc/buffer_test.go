//nolint:mnd // Test file contains many magic numbers for expected values
package webrtc

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/aac"
	"github.com/ugparu/gomedia/codec/h264"
	"github.com/ugparu/gomedia/tests"
	"github.com/ugparu/gomedia/utils/logger"
)

const testDataDir = "../../tests/data/h264_aac/"

// ---------------------------------------------------------------------------
// Test data helpers
// ---------------------------------------------------------------------------

func loadTestCodecPair(t *testing.T, sourceID string) (gomedia.CodecParametersPair, *h264.CodecParameters, *aac.CodecParameters) {
	t.Helper()
	raw, err := os.ReadFile(testDataDir + "parameters.json")
	require.NoError(t, err)

	var params tests.ParametersJSON
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

	var pkts tests.PacketsJSON
	require.NoError(t, json.Unmarshal(raw, &pkts))

	absBase := time.Now()
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

func makeVideoPacket(t *testing.T, videoCp *h264.CodecParameters, sourceID string, keyframe bool, ts time.Duration, dur time.Duration, absTime time.Time) gomedia.VideoPacket {
	t.Helper()
	data := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xAA, 0xBB, 0xCC, 0xDD, 0xEE} // fake NAL
	pkt := h264.NewPacket(keyframe, ts, absTime, data, sourceID, videoCp)
	pkt.SetDuration(dur)
	return pkt
}

func makeAudioPacket(t *testing.T, audioCp *aac.CodecParameters, sourceID string, ts time.Duration, dur time.Duration, absTime time.Time) gomedia.AudioPacket {
	t.Helper()
	data := []byte{0xFF, 0xF1, 0x50, 0x40, 0x02, 0x7F, 0xFC} // fake AAC frame
	return aac.NewPacket(data, ts, sourceID, absTime, audioCp, dur)
}

func makeAbsTime() time.Time {
	return time.Now()
}

func newBuffer(targetDuration time.Duration) *Buffer {
	return &Buffer{
		log:             logger.Default,
		targetDuration:  targetDuration,
		hardCapDuration: targetDuration + time.Second,
	}
}

// ---------------------------------------------------------------------------
// Buffer tests
// ---------------------------------------------------------------------------

func TestBuffer_AddPacket_DropsBeforeKeyframe(t *testing.T) {
	_, videoCp, audioCp := loadTestCodecPair(t, "rtsp://test")
	buf := newBuffer(10 * time.Second)
	absTime := time.Now()

	// Non-keyframe video should be dropped before first keyframe
	pkt := makeVideoPacket(t, videoCp, "rtsp://test", false, 0, 33*time.Millisecond, absTime)
	assert.False(t, buf.AddPacket(pkt))
	assert.Len(t, buf.gops, 0)

	// Audio before first keyframe should also be dropped
	aPkt := makeAudioPacket(t, audioCp, "rtsp://test", 0, 21*time.Millisecond, absTime)
	assert.False(t, buf.AddPacket(aPkt))
	assert.Len(t, buf.gops, 0)
}

func TestBuffer_AddPacket_StartsGoPOnKeyframe(t *testing.T) {
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://test")
	buf := newBuffer(10 * time.Second)
	absTime := time.Now()

	kf := makeVideoPacket(t, videoCp, "rtsp://test", true, 0, 33*time.Millisecond, absTime)
	assert.True(t, buf.AddPacket(kf))
	assert.Len(t, buf.gops, 1)
	assert.Len(t, buf.gops[0].packets, 1)
	assert.Equal(t, 33*time.Millisecond, buf.duration)
}

func TestBuffer_AddPacket_SubsequentPacketsAppendToCurrentGoP(t *testing.T) {
	_, videoCp, audioCp := loadTestCodecPair(t, "rtsp://test")
	buf := newBuffer(10 * time.Second)
	absTime := time.Now()

	kf := makeVideoPacket(t, videoCp, "rtsp://test", true, 0, 33*time.Millisecond, absTime)
	buf.AddPacket(kf)

	// Non-keyframe video appends to current GOP
	p2 := makeVideoPacket(t, videoCp, "rtsp://test", false, 33*time.Millisecond, 33*time.Millisecond, absTime.Add(33*time.Millisecond))
	assert.True(t, buf.AddPacket(p2))
	assert.Len(t, buf.gops, 1)
	assert.Len(t, buf.gops[0].packets, 2)

	// Audio appends to current GOP but doesn't add to duration
	a1 := makeAudioPacket(t, audioCp, "rtsp://test", 0, 21*time.Millisecond, absTime)
	assert.True(t, buf.AddPacket(a1))
	assert.Len(t, buf.gops[0].packets, 3)
	assert.Equal(t, 66*time.Millisecond, buf.duration) // only video duration counted
}

func TestBuffer_AddPacket_NewKeyframeStartsNewGoP(t *testing.T) {
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://test")
	buf := newBuffer(10 * time.Second)
	absTime := time.Now()

	kf1 := makeVideoPacket(t, videoCp, "rtsp://test", true, 0, 33*time.Millisecond, absTime)
	buf.AddPacket(kf1)

	p := makeVideoPacket(t, videoCp, "rtsp://test", false, 33*time.Millisecond, 33*time.Millisecond, absTime.Add(33*time.Millisecond))
	buf.AddPacket(p)

	kf2 := makeVideoPacket(t, videoCp, "rtsp://test", true, 66*time.Millisecond, 33*time.Millisecond, absTime.Add(66*time.Millisecond))
	buf.AddPacket(kf2)

	assert.Len(t, buf.gops, 2)
	assert.Len(t, buf.gops[0].packets, 2) // kf1 + p
	assert.Len(t, buf.gops[1].packets, 1) // kf2
}

func TestBuffer_AdjustSize_RemovesOldGoPsWhenOverTarget(t *testing.T) {
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://test")
	buf := newBuffer(50 * time.Millisecond) // very short target
	absTime := time.Now()

	// Fill two GOPs, each 33ms
	kf1 := makeVideoPacket(t, videoCp, "rtsp://test", true, 0, 33*time.Millisecond, absTime)
	buf.AddPacket(kf1)

	kf2 := makeVideoPacket(t, videoCp, "rtsp://test", true, 33*time.Millisecond, 33*time.Millisecond, absTime.Add(33*time.Millisecond))
	buf.AddPacket(kf2)

	// Both GOPs fit (66ms total, removing first would leave 33ms < 50ms target)
	assert.Len(t, buf.gops, 2)

	// Third GOP pushes over - now 99ms total, removing first leaves 66ms >= 50ms
	kf3 := makeVideoPacket(t, videoCp, "rtsp://test", true, 66*time.Millisecond, 33*time.Millisecond, absTime.Add(66*time.Millisecond))
	buf.AddPacket(kf3)

	assert.Len(t, buf.gops, 2) // oldest GOP removed
	assert.Equal(t, 66*time.Millisecond, buf.duration)
}

func TestBuffer_AddPacket_HardCapShiftsSingleGOPAndKeepsKeyframe(t *testing.T) {
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://test")
	buf := newBuffer(200 * time.Millisecond)
	absTime := time.Now()

	kf := makeVideoPacket(t, videoCp, "rtsp://test", true, 0, 100*time.Millisecond, absTime)
	assert.True(t, buf.AddPacket(kf))

	for i := 1; i <= 12; i++ {
		ts := time.Duration(i) * 100 * time.Millisecond
		p := makeVideoPacket(t, videoCp, "rtsp://test", false, ts, 100*time.Millisecond, absTime.Add(ts))
		assert.True(t, buf.AddPacket(p))
	}

	assert.Len(t, buf.gops, 1)
	assert.Len(t, buf.gops[0].packets, 12)
	assert.Equal(t, 1200*time.Millisecond, buf.duration)

	first, ok := buf.gops[0].packets[0].(gomedia.VideoPacket)
	require.True(t, ok)
	assert.True(t, first.IsKeyFrame())
	assert.Equal(t, absTime, first.StartTime())

	second, ok := buf.gops[0].packets[1].(gomedia.VideoPacket)
	require.True(t, ok)
	assert.False(t, second.IsKeyFrame())
	assert.Equal(t, absTime.Add(200*time.Millisecond), second.StartTime())
}

func TestBuffer_AddPacket_HardCapDropsWholeGOPWhenPossible(t *testing.T) {
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://test")
	buf := newBuffer(200 * time.Millisecond)
	absTime := time.Now()

	kf1 := makeVideoPacket(t, videoCp, "rtsp://test", true, 0, 100*time.Millisecond, absTime)
	assert.True(t, buf.AddPacket(kf1))

	kf2TS := 100 * time.Millisecond
	kf2 := makeVideoPacket(t, videoCp, "rtsp://test", true, kf2TS, 100*time.Millisecond, absTime.Add(kf2TS))
	assert.True(t, buf.AddPacket(kf2))

	for i := 2; i <= 12; i++ {
		ts := time.Duration(i) * 100 * time.Millisecond
		p := makeVideoPacket(t, videoCp, "rtsp://test", false, ts, 100*time.Millisecond, absTime.Add(ts))
		assert.True(t, buf.AddPacket(p))
	}

	assert.Len(t, buf.gops, 1)
	assert.Equal(t, 1200*time.Millisecond, buf.duration)

	first, ok := buf.gops[0].packets[0].(gomedia.VideoPacket)
	require.True(t, ok)
	assert.True(t, first.IsKeyFrame())
	assert.Equal(t, absTime.Add(kf2TS), first.StartTime())

	second, ok := buf.gops[0].packets[1].(gomedia.VideoPacket)
	require.True(t, ok)
	assert.False(t, second.IsKeyFrame())
	assert.Equal(t, absTime.Add(200*time.Millisecond), second.StartTime())
}

func TestBuffer_AddPacket_HardCapPreservesOnlyKeyframes(t *testing.T) {
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://test")
	buf := newBuffer(5 * time.Second)
	absTime := time.Now()

	kf1 := makeVideoPacket(t, videoCp, "rtsp://test", true, 0, 4*time.Second, absTime)
	assert.True(t, buf.AddPacket(kf1))

	kf2 := makeVideoPacket(t, videoCp, "rtsp://test", true, 4*time.Second, 4*time.Second, absTime.Add(4*time.Second))
	assert.True(t, buf.AddPacket(kf2))

	assert.Len(t, buf.gops, 2)
	assert.Len(t, buf.gops[0].packets, 1)
	assert.Len(t, buf.gops[1].packets, 1)
	assert.Equal(t, 8*time.Second, buf.duration)
}

func TestBuffer_GetBuffer_EmptyBuffer(t *testing.T) {
	buf := newBuffer(10 * time.Second)
	seed, rest := buf.GetBuffer(time.Now())
	assert.Nil(t, seed)
	assert.Nil(t, rest)
}

func TestBuffer_GetBuffer_AllPacketsAfterTimestamp(t *testing.T) {
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://test")
	buf := newBuffer(10 * time.Second)
	absTime := time.Now().Add(-5 * time.Second) // packets in the past

	kf := makeVideoPacket(t, videoCp, "rtsp://test", true, 0, 33*time.Millisecond, absTime)
	buf.AddPacket(kf)

	p := makeVideoPacket(t, videoCp, "rtsp://test", false, 33*time.Millisecond, 33*time.Millisecond, absTime.Add(33*time.Millisecond))
	buf.AddPacket(p)

	// Timestamp before all packets → gopsID < 0 → returns all as restBuf
	seed, rest := buf.GetBuffer(absTime.Add(-10 * time.Second))
	assert.Nil(t, seed)
	assert.Len(t, rest, 2)
}

func TestBuffer_GetBuffer_SplitsAtTimestamp(t *testing.T) {
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://test")
	buf := newBuffer(10 * time.Second)
	absTime := time.Now()

	kf := makeVideoPacket(t, videoCp, "rtsp://test", true, 0, 33*time.Millisecond, absTime)
	buf.AddPacket(kf)

	p1 := makeVideoPacket(t, videoCp, "rtsp://test", false, 33*time.Millisecond, 33*time.Millisecond, absTime.Add(33*time.Millisecond))
	buf.AddPacket(p1)

	p2 := makeVideoPacket(t, videoCp, "rtsp://test", false, 66*time.Millisecond, 33*time.Millisecond, absTime.Add(66*time.Millisecond))
	buf.AddPacket(p2)

	// Split in the middle of the GOP
	seed, rest := buf.GetBuffer(absTime.Add(50 * time.Millisecond))
	// kf and p1 are before ts, p2 is after
	assert.Len(t, seed, 2) // kf and p1 (video only in seedBuf)
	assert.Len(t, rest, 1) // p2
}

func TestBuffer_GetBuffer_MultipleGOPs(t *testing.T) {
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://test")
	buf := newBuffer(10 * time.Second)
	absTime := time.Now()

	// GOP 1
	kf1 := makeVideoPacket(t, videoCp, "rtsp://test", true, 0, 33*time.Millisecond, absTime)
	buf.AddPacket(kf1)
	p1 := makeVideoPacket(t, videoCp, "rtsp://test", false, 33*time.Millisecond, 33*time.Millisecond, absTime.Add(33*time.Millisecond))
	buf.AddPacket(p1)

	// GOP 2
	kf2 := makeVideoPacket(t, videoCp, "rtsp://test", true, 100*time.Millisecond, 33*time.Millisecond, absTime.Add(100*time.Millisecond))
	buf.AddPacket(kf2)
	p2 := makeVideoPacket(t, videoCp, "rtsp://test", false, 133*time.Millisecond, 33*time.Millisecond, absTime.Add(133*time.Millisecond))
	buf.AddPacket(p2)

	// Timestamp between GOP1 and GOP2 → selects GOP1 for splitting, GOP2 goes to rest
	seed, rest := buf.GetBuffer(absTime.Add(50 * time.Millisecond))
	assert.Len(t, seed, 2) // kf1, p1 (both before ts)
	assert.Len(t, rest, 2) // kf2, p2 (entire GOP2)
}

func TestBuffer_Reset_ClearsAll(t *testing.T) {
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://test")
	buf := newBuffer(10 * time.Second)
	absTime := time.Now()

	kf := makeVideoPacket(t, videoCp, "rtsp://test", true, 0, 33*time.Millisecond, absTime)
	buf.AddPacket(kf)

	buf.Reset()
	assert.Len(t, buf.gops, 0)
	assert.Equal(t, time.Duration(0), buf.duration)

	// Can still add packets after reset
	kf2 := makeVideoPacket(t, videoCp, "rtsp://test", true, 100*time.Millisecond, 33*time.Millisecond, absTime.Add(100*time.Millisecond))
	assert.True(t, buf.AddPacket(kf2))
}

func TestBuffer_Close_NilifiesSlice(t *testing.T) {
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://test")
	buf := newBuffer(10 * time.Second)
	absTime := time.Now()

	kf := makeVideoPacket(t, videoCp, "rtsp://test", true, 0, 33*time.Millisecond, absTime)
	buf.AddPacket(kf)

	buf.Close()
	assert.Nil(t, buf.gops)
	assert.Equal(t, time.Duration(0), buf.duration)
}

func TestBuffer_WithRealTestData(t *testing.T) {
	_, videoCp, audioCp := loadTestCodecPair(t, "rtsp://test")
	packets := loadTestPackets(t, "rtsp://test", videoCp, audioCp, 100)
	require.True(t, len(packets) > 0)

	buf := newBuffer(2 * time.Second)

	storedCount := 0
	for _, pkt := range packets {
		if buf.AddPacket(pkt) {
			storedCount++
		}
	}

	assert.Greater(t, storedCount, 0)
	assert.Greater(t, len(buf.gops), 0)

	// First packet in first GOP should be a keyframe
	firstPkt, ok := buf.gops[0].packets[0].(gomedia.VideoPacket)
	assert.True(t, ok)
	assert.True(t, firstPkt.IsKeyFrame())
}
