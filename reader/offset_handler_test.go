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

	cached := oh.CheckEmptyPacket(pkt, time.Time{})

	require.True(t, cached, "first packet should be cached (one-behind)")
	assert.Equal(t, pkt, oh.lastPacket, "lastPacket should be the first packet")
	assert.Equal(t, 500*time.Millisecond, oh.offsetDown, "offsetDown should be set to first packet timestamp")
}

func TestOffsetHandler_FirstPacket_TimestampNormalized(t *testing.T) {
	t.Parallel()
	oh := &offsetHandler{}
	pkt := newVideoPacket(500 * time.Millisecond)

	oh.CheckEmptyPacket(pkt, time.Time{})

	// With offsetUp=0 and offsetDown=500ms, timestamp should become 500ms + 0 - 500ms = 0
	assert.Equal(t, time.Duration(0), pkt.Timestamp(), "first packet timestamp should be normalized to 0")
}

func TestOffsetHandler_SecondPacket_NotCached(t *testing.T) {
	t.Parallel()
	oh := &offsetHandler{}
	pkt1 := newVideoPacket(100 * time.Millisecond)
	oh.CheckEmptyPacket(pkt1, time.Time{})

	pkt2 := newVideoPacket(200 * time.Millisecond)
	cached := oh.CheckEmptyPacket(pkt2, time.Time{})

	assert.False(t, cached, "second packet should not be cached")
}

func TestOffsetHandler_ApplyToPkt_AdjustsTimestamp(t *testing.T) {
	t.Parallel()
	oh := &offsetHandler{}
	pkt1 := newVideoPacket(1 * time.Second)
	oh.CheckEmptyPacket(pkt1, time.Time{})

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
	oh.CheckEmptyPacket(pkt1, time.Time{})

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
	oh.CheckEmptyPacket(pkt1, time.Time{})

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
	oh.CheckEmptyPacket(pkt1, time.Time{})

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

func TestOffsetHandler_RecalcForGap_ResetsAndReleases(t *testing.T) {
	t.Parallel()
	oh := &offsetHandler{}
	pkt := newVideoPacket(1 * time.Second)
	pkt.SetStartTime(time.Now().Add(-100 * time.Millisecond))
	oh.CheckEmptyPacket(pkt, time.Time{})
	oh.lastDuration = 33 * time.Millisecond

	oh.RecalcForGap()

	assert.Nil(t, oh.lastPacket, "lastPacket should be nil after RecalcForGap")
	assert.NotZero(t, oh.offsetUp, "offsetUp should be recalculated")
}

func TestOffsetHandler_RecalcForGap_NilSafe(t *testing.T) {
	t.Parallel()
	oh := &offsetHandler{}

	// Should not panic with no lastPacket
	oh.RecalcForGap()
}

func TestOffsetHandler_RecalcForGap_PreservesContinuity(t *testing.T) {
	t.Parallel()
	oh := &offsetHandler{}

	// First packet at 1s
	pkt1 := newVideoPacket(1 * time.Second)
	pkt1.SetStartTime(time.Now())
	oh.CheckEmptyPacket(pkt1, time.Time{})
	oh.lastDuration = 33 * time.Millisecond

	// Simulate second packet arriving
	pkt2 := newVideoPacket(1*time.Second + 33*time.Millisecond)
	oh.applyToPkt(pkt2)
	oh.lastPacket.SetDuration(pkt2.Timestamp() - oh.lastPacket.Timestamp())
	oh.lastDuration = oh.lastPacket.Duration()
	oh.lastPacket = pkt2

	// Simulate a gap/reconnect after some time
	time.Sleep(10 * time.Millisecond)
	oh.RecalcForGap()

	// After gap, a new packet from the reconnected stream should get normalized
	// to continue from where we left off (approximately)
	pkt3 := newVideoPacket(5 * time.Second) // new stream starts at 5s
	cached := oh.CheckEmptyPacket(pkt3, time.Time{})
	require.True(t, cached)

	// The new packet's timestamp should be > 0 (continuing from previous stream)
	assert.Greater(t, pkt3.Timestamp(), time.Duration(0),
		"after RecalcForGap, new stream packets should have positive timestamps continuing from before")
}

// TestOffsetHandler_CheckEmptyPacket_AlignsLateTrackToEpoch reproduces the A/V
// desync fix: a track whose first packet arrives after the shared epoch is
// placed at its wall-clock distance from the epoch, not at 0, so audio that
// comes up later than video lands on the same timeline instead of trailing by
// its start delay.
func TestOffsetHandler_CheckEmptyPacket_AlignsLateTrackToEpoch(t *testing.T) {
	t.Parallel()
	epoch := time.Now()

	// "Video": first packet at the epoch, arbitrary RTP base 1000s.
	vh := &offsetHandler{}
	vpkt := newVideoPacket(1000 * time.Second)
	vpkt.SetStartTime(epoch)
	vh.CheckEmptyPacket(vpkt, epoch)
	assert.Equal(t, time.Duration(0), vh.offsetUp, "first track anchors at 0")
	assert.Equal(t, time.Duration(0), vpkt.Timestamp(), "first track normalized to 0")

	// "Audio": first packet 142s after the epoch, unrelated RTP base 500s.
	ah := &offsetHandler{}
	apkt := newVideoPacket(500 * time.Second)
	apkt.SetStartTime(epoch.Add(142 * time.Second))
	ah.CheckEmptyPacket(apkt, epoch)
	assert.Equal(t, 142*time.Second, ah.offsetUp, "late track anchors at its start delay")
	assert.Equal(t, 142*time.Second, apkt.Timestamp(),
		"late track placed on the shared timeline, not at 0")
}

// TestOffsetHandler_CheckEmptyPacket_ReanchorKeepsTimeline ensures epoch
// alignment applies only to the very first packet: after a reconnect
// (RecalcForGap sets offsetUp to continue the timeline) the next first packet
// must not be re-shifted back to the epoch.
func TestOffsetHandler_CheckEmptyPacket_ReanchorKeepsTimeline(t *testing.T) {
	t.Parallel()
	epoch := time.Now()

	// Already anchored with a non-zero offsetUp, as RecalcForGap leaves it after
	// a reconnect. The next first packet — arriving an hour after the epoch —
	// must keep that offset, not jump to (StartTime - epoch).
	oh := &offsetHandler{anchored: true, offsetUp: 7 * time.Second}
	pkt := newVideoPacket(5 * time.Second)
	pkt.SetStartTime(epoch.Add(time.Hour))

	oh.CheckEmptyPacket(pkt, epoch)

	assert.Equal(t, 7*time.Second, oh.offsetUp,
		"already-anchored track keeps its offset, not re-anchored to epoch")
	// Normalized against the preserved offset: 5s + 7s - 5s = 7s.
	assert.Equal(t, 7*time.Second, pkt.Timestamp())
}

// TestOffsetHandler_Reconnect_PreservesAVAlignment verifies that after a
// reconnect (RecalcForGap on both tracks) audio and video that were aligned
// resume aligned — the epoch anchoring does not interfere, and both continue
// their timeline by the same real elapsed.
func TestOffsetHandler_Reconnect_PreservesAVAlignment(t *testing.T) {
	t.Parallel()
	gapStart := time.Now().Add(-10 * time.Second) // last packets arrived 10s ago

	vh := &offsetHandler{anchored: true}
	vpkt := newVideoPacket(30 * time.Second)
	vpkt.SetStartTime(gapStart)
	vh.lastPacket = vpkt
	vh.lastDuration = 40 * time.Millisecond

	ah := &offsetHandler{anchored: true}
	apkt := newVideoPacket(30 * time.Second)
	apkt.SetStartTime(gapStart)
	ah.lastPacket = apkt
	ah.lastDuration = 40 * time.Millisecond

	vh.RecalcForGap()
	ah.RecalcForGap()

	assert.Greater(t, vh.offsetUp, 30*time.Second, "timeline continued across the gap")
	assert.InDelta(t, ah.offsetUp.Seconds(), vh.offsetUp.Seconds(), 0.1,
		"after reconnect both tracks resume aligned")
}

func TestOffsetHandler_CheckTSWrap_NoWrap(t *testing.T) {
	t.Parallel()
	oh := &offsetHandler{}
	pkt1 := newVideoPacket(1 * time.Second)
	oh.CheckEmptyPacket(pkt1, time.Time{})

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
	oh.CheckEmptyPacket(pkt1, time.Time{})
	oh.lastDuration = 33 * time.Millisecond

	// Simulate wrapped packet (goes back to small value)
	pkt2 := newVideoPacket(1 * time.Second)
	oh.CheckTSWrap(pkt2)

	// offsetDown should be recalculated for the wrap
	assert.NotEqual(t, highTS, oh.offsetDown,
		"offsetDown should be recalculated after timestamp wrap")
}
