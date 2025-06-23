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
	oh.lastPacket = nil
}

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
