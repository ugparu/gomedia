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

func TestOffsetHandler_RecalcForGap_ResetsAndReleases(t *testing.T) {
	t.Parallel()
	oh := &offsetHandler{}
	pkt := newVideoPacket(1 * time.Second)
	pkt.SetStartTime(time.Now().Add(-100 * time.Millisecond))
	oh.CheckEmptyPacket(pkt)
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
	oh.CheckEmptyPacket(pkt1)
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
	cached := oh.CheckEmptyPacket(pkt3)
	require.True(t, cached)

	// The new packet's timestamp should be > 0 (continuing from previous stream)
	assert.Greater(t, pkt3.Timestamp(), time.Duration(0),
		"after RecalcForGap, new stream packets should have positive timestamps continuing from before")
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
