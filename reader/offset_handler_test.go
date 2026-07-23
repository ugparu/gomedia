package reader

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec"
	"github.com/ugparu/gomedia/codec/h264"
)

// newVideoPacket creates a heap-backed h264 video packet with the given timestamp.
// Release is a no-op for heap-backed packets.
func newVideoPacket(ts time.Duration) gomedia.VideoPacket {
	par := &h264.CodecParameters{
		BaseParameters: codec.BaseParameters{
			CodecType: gomedia.H264,
		},
	}
	return h264.NewPacket(true, ts, time.Now(), []byte{0x00}, "test", par)
}

func TestOffsetHandler_FirstPacket_IsCached(t *testing.T) {
	t.Parallel()
	oh := &offsetHandler{}
	pkt := newVideoPacket(500 * time.Millisecond)

	cached := oh.CheckEmptyPacket(pkt)

	require.True(t, cached, "first packet should be cached (one-behind)")
	assert.Equal(t, pkt, oh.lastPacket, "lastPacket should be the first packet")
	assert.Equal(t, 500*time.Millisecond, oh.offsetDown, "offsetDown should be set to first packet timestamp")
}

func TestOffsetHandler_FirstPacket_TimestampNormalized(t *testing.T) {
	t.Parallel()
	oh := &offsetHandler{}
	pkt := newVideoPacket(500 * time.Millisecond)

	oh.CheckEmptyPacket(pkt)

	// With offsetUp=0 and offsetDown=500ms, timestamp should become 500ms + 0 - 500ms = 0
	assert.Equal(t, time.Duration(0), pkt.Timestamp(), "first packet timestamp should be normalized to 0")
}

func TestOffsetHandler_SecondPacket_NotCached(t *testing.T) {
	t.Parallel()
	oh := &offsetHandler{}
	pkt1 := newVideoPacket(100 * time.Millisecond)
	oh.CheckEmptyPacket(pkt1)

	pkt2 := newVideoPacket(200 * time.Millisecond)
	cached := oh.CheckEmptyPacket(pkt2)

	assert.False(t, cached, "second packet should not be cached")
}

func TestOffsetHandler_ApplyToPkt_AdjustsTimestamp(t *testing.T) {
	t.Parallel()
	oh := &offsetHandler{}
	pkt1 := newVideoPacket(1 * time.Second)
	oh.CheckEmptyPacket(pkt1)

	pkt2 := newVideoPacket(2 * time.Second)
	ok := oh.applyToPkt(pkt2)

	require.True(t, ok, "monotonically increasing timestamp should be accepted")
	// Adjusted: 2s + 0 - 1s = 1s
	assert.Equal(t, 1*time.Second, pkt2.Timestamp())
}

func TestOffsetHandler_ApplyToPkt_RejectsNonMonotonic(t *testing.T) {
	t.Parallel()
	oh := &offsetHandler{}
	pkt1 := newVideoPacket(1 * time.Second)
	oh.CheckEmptyPacket(pkt1)

	// Same timestamp as first after normalization → not strictly greater → rejected
	pkt2 := newVideoPacket(1 * time.Second)
	ok := oh.applyToPkt(pkt2)

	assert.False(t, ok, "packet with equal timestamp should be rejected")
}

func TestOffsetHandler_ApplyToPkt_RejectsBackwardsTimestamp(t *testing.T) {
	t.Parallel()
	oh := &offsetHandler{}
	// First packet at 2s, normalized to 0
	pkt1 := newVideoPacket(2 * time.Second)
	oh.CheckEmptyPacket(pkt1)

	// Second packet at 1.5s, normalized to -0.5s → not greater than 0 → rejected
	pkt2 := newVideoPacket(1500 * time.Millisecond)
	ok := oh.applyToPkt(pkt2)

	assert.False(t, ok, "packet with earlier timestamp should be rejected")
}

func TestOffsetHandler_ApplyToPkt_MultiplePackets_MonotonicTimestamps(t *testing.T) {
	t.Parallel()
	oh := &offsetHandler{}
	// Simulate a stream starting at 10s
	pkt1 := newVideoPacket(10 * time.Second)
	oh.CheckEmptyPacket(pkt1)

	timestamps := []time.Duration{
		10*time.Second + 33*time.Millisecond,
		10*time.Second + 66*time.Millisecond,
		10*time.Second + 100*time.Millisecond,
	}

	var prevTS time.Duration
	for i, ts := range timestamps {
		pkt := newVideoPacket(ts)
		ok := oh.applyToPkt(pkt)
		require.True(t, ok, "packet %d should be accepted", i)
		assert.Greater(t, pkt.Timestamp(), prevTS, "packet %d timestamp should be greater than previous", i)
		prevTS = pkt.Timestamp()
		oh.lastPacket = pkt
	}
}

func TestOffsetHandler_ReleaseLastPacket(t *testing.T) {
	t.Parallel()
	oh := &offsetHandler{}
	pkt := newVideoPacket(100 * time.Millisecond)
	oh.lastPacket = pkt

	oh.releaseLastPacket()

	assert.Nil(t, oh.lastPacket, "lastPacket should be nil after release")
}

func TestOffsetHandler_ReleaseLastPacket_NilSafe(t *testing.T) {
	t.Parallel()
	oh := &offsetHandler{}

	// Should not panic
	oh.releaseLastPacket()

	assert.Nil(t, oh.lastPacket)
}

// setFlowing seeds a handler as if it has been emitting: a one-behind packet at
// the given emitted timestamp that arrived `arrivedAgo` ago.
func setFlowing(oh *offsetHandler, emit, dur, arrivedAgo time.Duration) {
	pkt := newVideoPacket(emit)
	pkt.SetStartTime(time.Now().Add(-arrivedAgo))
	oh.lastPacket = pkt
	oh.lastDuration = dur
}

func TestOffsetHandler_GapResumeTarget_ContinuesTimeline(t *testing.T) {
	t.Parallel()
	oh := &offsetHandler{}
	setFlowing(oh, 30*time.Second, 40*time.Millisecond, 10*time.Second)

	target, ok := oh.gapResumeTarget()

	require.True(t, ok)
	// 30s (last emit) + ~10s elapsed = ~40s.
	assert.InDelta(t, 40.0, target.Seconds(), 0.1, "target continues from last emit plus elapsed")
}

func TestOffsetHandler_GapResumeTarget_NoAnchor(t *testing.T) {
	t.Parallel()
	oh := &offsetHandler{}
	_, ok := oh.gapResumeTarget()
	assert.False(t, ok, "no packet to anchor on")
}

func TestOffsetHandler_ResumeAt_NextPacketLandsAtTarget(t *testing.T) {
	t.Parallel()
	oh := &offsetHandler{}
	setFlowing(oh, 5*time.Second, 40*time.Millisecond, time.Second)

	oh.resumeAt(42 * time.Second)
	assert.Nil(t, oh.lastPacket, "stale one-behind dropped")

	// The reconnected stream starts at an unrelated RTP base; its first packet
	// must land exactly at the resume target.
	pkt := newVideoPacket(999 * time.Second)
	require.True(t, oh.CheckEmptyPacket(pkt))
	assert.Equal(t, 42*time.Second, pkt.Timestamp(),
		"first post-reconnect packet continues the timeline at the shared target")
}

// TestReader_BridgeGap_KeepsAVInLockstep is the core of the accumulation fix:
// on reconnect both tracks resume at ONE shared target (video's), so even when
// audio is behind or stale it snaps to video's timeline and cannot drift.
func TestReader_BridgeGap_KeepsAVInLockstep(t *testing.T) {
	t.Parallel()
	rdr := &reader{}

	video := &offsetHandler{}
	setFlowing(video, 30*time.Second, 40*time.Millisecond, 5*time.Second)
	audio := &offsetHandler{}
	setFlowing(audio, 30*time.Second, 21*time.Millisecond, 5*time.Second)

	rdr.bridgeGap(video, audio)

	assert.Equal(t, video.offsetUp, audio.offsetUp,
		"both tracks re-anchor to the exact same resume target")

	// Both reconnected streams start at unrelated RTP bases; their first packets
	// must land at the same point.
	vpkt := newVideoPacket(700 * time.Second)
	apkt := newVideoPacket(120 * time.Second)
	require.True(t, video.CheckEmptyPacket(vpkt))
	require.True(t, audio.CheckEmptyPacket(apkt))
	assert.Equal(t, vpkt.Timestamp(), apkt.Timestamp(),
		"audio and video resume aligned after the reconnect")
}

// TestReader_BridgeGap_SnapsStaleAudioToVideo reproduces the desync: audio went
// silent, so its one-behind was already dropped (nil) at reconnect. Its offsetUp
// is stale. bridgeGap must still snap it to video's target instead of leaving it
// behind (the accumulation bug).
func TestReader_BridgeGap_SnapsStaleAudioToVideo(t *testing.T) {
	t.Parallel()
	rdr := &reader{}

	video := &offsetHandler{}
	setFlowing(video, 200*time.Second, 40*time.Millisecond, 3*time.Second)

	// Audio silent: no one-behind, and a stale offsetUp from long ago.
	audio := &offsetHandler{offsetUp: 80 * time.Second}

	rdr.bridgeGap(video, audio)

	assert.Equal(t, video.offsetUp, audio.offsetUp,
		"stale audio snaps to video's resume target, not its old offset")
	assert.Greater(t, audio.offsetUp, 80*time.Second, "old stale offset is overwritten")
}

func TestReader_BridgeGap_NoAnchor_NoOp(t *testing.T) {
	t.Parallel()
	rdr := &reader{}
	video := &offsetHandler{offsetUp: 3 * time.Second}
	audio := &offsetHandler{offsetUp: 4 * time.Second}

	// Neither track has a packet to anchor on — leave offsets untouched.
	rdr.bridgeGap(video, audio)

	assert.Equal(t, 3*time.Second, video.offsetUp)
	assert.Equal(t, 4*time.Second, audio.offsetUp)
}

func TestOffsetHandler_CheckTSWrap_NoWrap(t *testing.T) {
	t.Parallel()
	oh := &offsetHandler{}
	pkt1 := newVideoPacket(1 * time.Second)
	oh.CheckEmptyPacket(pkt1)

	// Normal next packet — no wrap
	pkt2 := newVideoPacket(1*time.Second + 33*time.Millisecond)
	oh.CheckTSWrap(pkt2)

	// offsetDown should remain unchanged
	assert.Equal(t, 1*time.Second, oh.offsetDown,
		"offsetDown should not change when there is no timestamp wrap")
}

func TestOffsetHandler_CheckTSWrap_DetectsWrap(t *testing.T) {
	t.Parallel()
	oh := &offsetHandler{}
	// Simulate packet near max RTP timestamp (large value)
	highTS := 100 * time.Minute
	pkt1 := newVideoPacket(highTS)
	oh.CheckEmptyPacket(pkt1)
	oh.lastDuration = 33 * time.Millisecond

	// Simulate wrapped packet (goes back to small value)
	pkt2 := newVideoPacket(1 * time.Second)
	oh.CheckTSWrap(pkt2)

	// offsetDown should be recalculated for the wrap
	assert.NotEqual(t, highTS, oh.offsetDown,
		"offsetDown should be recalculated after timestamp wrap")
}
