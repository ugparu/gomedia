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
	// anchored becomes true after the very first packet of the track is placed
	// on the shared epoch. Only that first packet is epoch-aligned; every later
	// re-anchoring (RecalcForGap/CheckTSWrap on reconnect or wrap) must keep the
	// timeline it already built, so it must not be re-shifted to the epoch.
	anchored bool
}

// releaseLastPacket releases the one-behind cached packet if present.
func (oh *offsetHandler) releaseLastPacket() {
	if oh.lastPacket != nil {
		oh.lastPacket.Release()
		oh.lastPacket = nil
	}
}

func (oh *offsetHandler) RecalcForGap() {
	if oh.lastPacket == nil {
		return
	}

	oh.offsetUp = oh.lastPacket.Timestamp() + time.Since(oh.lastPacket.StartTime())
	if oh.lastDuration != 0 {
		oh.offsetUp /= oh.lastDuration
		oh.offsetUp *= oh.lastDuration
	}
	oh.offsetUp = time.Duration(oh.offsetUp.Milliseconds()/10*10) * time.Millisecond //nolint: mnd
	oh.lastPacket.Release()
	oh.lastPacket = nil
}

// CheckEmptyPacket caches the first packet of the track (one-behind) and
// normalizes its timestamp. epoch is the wall-clock StartTime of the first
// packet seen across ALL tracks of the source; the first packet of THIS track
// is placed at its wall-clock distance from that epoch instead of at 0, so a
// track that starts later than the other (e.g. audio that comes up seconds or
// minutes after video) lands aligned on the shared timeline rather than
// diverging by its start delay. Later cache refills (after RecalcForGap on a
// reconnect) keep the offsetUp those handlers computed.
func (oh *offsetHandler) CheckEmptyPacket(pkt gomedia.Packet, epoch time.Time) bool {
	if oh.lastPacket == nil {
		oh.offsetDown = pkt.Timestamp()
		if !oh.anchored {
			if d := pkt.StartTime().Sub(epoch); d > 0 && !epoch.IsZero() {
				oh.offsetUp = time.Duration(d.Milliseconds()/10*10) * time.Millisecond //nolint: mnd
			}
			oh.anchored = true
		}
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
