package reader

import (
	"time"

	"github.com/ugparu/gomedia"
)

type offsetHandler struct {
	lastPacket   gomedia.Packet
	lastDuration time.Duration
	offsetUp     time.Duration
	offsetDown   time.Duration
}

// releaseLastPacket releases the one-behind cached packet if present.
func (oh *offsetHandler) releaseLastPacket() {
	if oh.lastPacket != nil {
		oh.lastPacket.Release()
		oh.lastPacket = nil
	}
}

// gapResumeTarget returns the timeline position playback should continue from
// after a reconnect gap: the last emitted timestamp plus the real wall time
// elapsed since it arrived, rounded to the last packet duration. ok is false
// when the track has no packet to anchor on (never started, or went silent and
// its one-behind was already dropped). The reader uses ONE target for both
// tracks so audio and video resume in lockstep — see reader.bridgeGap.
func (oh *offsetHandler) gapResumeTarget() (target time.Duration, ok bool) {
	if oh.lastPacket == nil {
		return 0, false
	}
	target = oh.lastPacket.Timestamp() + time.Since(oh.lastPacket.StartTime())
	if oh.lastDuration != 0 {
		target = target / oh.lastDuration * oh.lastDuration
	}
	target = time.Duration(target.Milliseconds()/10*10) * time.Millisecond //nolint: mnd
	return target, true
}

// resumeAt re-anchors the track so its next (first) packet continues the
// timeline exactly at target, then drops the stale one-behind packet. Applied
// with the SAME target to both tracks after a reconnect, so the gap is bridged
// identically for audio and video and they cannot drift apart — the reconnect
// stays invisible to consumers (continuous, monotonic timeline).
func (oh *offsetHandler) resumeAt(target time.Duration) {
	oh.offsetUp = target
	oh.releaseLastPacket()
}

// CheckEmptyPacket caches the first packet of the track (one-behind) and
// normalizes its timestamp against the current offsets. After resumeAt set
// offsetUp to a shared target, the first packet lands exactly at that target,
// so audio and video re-anchored together stay aligned.
func (oh *offsetHandler) CheckEmptyPacket(pkt gomedia.Packet) bool {
	if oh.lastPacket == nil {
		oh.offsetDown = pkt.Timestamp()
		pkt.SetTimestamp(pkt.Timestamp() + oh.offsetUp - oh.offsetDown)
		oh.lastPacket = pkt
		return true
	}
	return false
}

func (oh *offsetHandler) applyToPkt(pkt gomedia.Packet) bool {
	pkt.SetTimestamp(pkt.Timestamp() + oh.offsetUp - oh.offsetDown)
	return pkt.Timestamp() > oh.lastPacket.Timestamp()
}

func (oh *offsetHandler) CheckTSWrap(pkt gomedia.Packet) {
	if oh.lastPacket.Timestamp()-pkt.Timestamp()-oh.offsetUp+oh.offsetDown <= time.Minute {
		return
	}
	oh.offsetDown = pkt.Timestamp() - oh.lastDuration
	oh.offsetUp = oh.lastPacket.Timestamp()
	if oh.lastDuration != 0 {
		oh.offsetUp /= oh.lastDuration
		oh.offsetUp *= oh.lastDuration
	}
	oh.offsetUp = time.Duration(oh.offsetUp.Milliseconds()/10*10) * time.Millisecond //nolint: mnd
}
