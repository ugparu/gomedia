package webrtc

import (
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/utils/logger"
)

type GoP struct {
	packets  []gomedia.Packet
	duration time.Duration
}

type Buffer struct {
	log             logger.Logger
	gops            []GoP
	duration        time.Duration
	targetDuration  time.Duration
	hardCapDuration time.Duration
}

// AddPacket stores packet in the current GOP buffer.
// Returns false when the packet is dropped (no GOP started yet — waiting for
// the first key frame). The caller must release the packet when false is returned.
func (b *Buffer) AddPacket(packet gomedia.Packet) bool {
	vPkt, ok := packet.(gomedia.VideoPacket)
	if ok && vPkt.IsKeyFrame() {
		b.gops = append(b.gops, GoP{})
		defer b.AdjustSize()
	}

	if len(b.gops) == 0 {
		return false
	}

	b.gops[len(b.gops)-1].packets = append(b.gops[len(b.gops)-1].packets, packet)
	if ok {
		b.gops[len(b.gops)-1].duration += packet.Duration()
		b.duration += packet.Duration()
	}

	b.adjustHardCap()
	return true
}

func (b *Buffer) AdjustSize() {
	if b.duration < b.targetDuration || b.duration-b.gops[0].duration < b.targetDuration {
		return
	}

	for _, pkt := range b.gops[0].packets {
		pkt.Release()
	}
	b.duration -= b.gops[0].duration
	b.gops = b.gops[1:]
}

func (b *Buffer) adjustHardCap() {
	if b.hardCapDuration == 0 {
		return
	}

	for len(b.gops) > 1 && b.duration > b.hardCapDuration && b.duration-b.gops[0].duration >= b.hardCapDuration {
		b.dropOldestGOP()
	}

	for len(b.gops) == 1 && b.duration > b.hardCapDuration {
		if !b.shiftSingleGOP() {
			return
		}
	}
}

func (b *Buffer) dropOldestGOP() {
	for _, pkt := range b.gops[0].packets {
		pkt.Release()
	}
	b.duration -= b.gops[0].duration
	b.gops = b.gops[1:]
}

func (b *Buffer) shiftSingleGOP() bool {
	gop := &b.gops[0]
	if len(gop.packets) < 2 {
		return false
	}

	dropped := gop.packets[1]
	var droppedDur time.Duration
	if _, ok := dropped.(gomedia.VideoPacket); ok {
		droppedDur = dropped.Duration()
	}
	dropped.Release()

	copy(gop.packets[1:], gop.packets[2:])
	gop.packets = gop.packets[:len(gop.packets)-1]

	gop.duration -= droppedDur
	b.duration -= droppedDur

	return true
}

func (b *Buffer) GetBuffer(ts time.Time) ([]gomedia.VideoPacket, []gomedia.Packet) {
	b.log.Debugf(b, "GetBuffer called with ts=%v, total_gops=%d", ts, len(b.gops))

	gopsID := len(b.gops)
	for i := range b.gops {
		if len(b.gops[i].packets) == 0 {
			continue
		}
		if b.gops[i].packets[0].StartTime().After(ts) {
			gopsID = i
			b.log.Debugf(b, "Found GOP at index %d with start_time=%v after ts=%v", i, b.gops[i].packets[0].StartTime(), ts)
			break
		}
	}
	gopsID--
	b.log.Debugf(b, "Selected gopsID=%d (after decrement)", gopsID)

	if gopsID < 0 {
		b.log.Debugf(b, "gopsID < 0, returning all packets from all GOPs")
		var response []gomedia.Packet
		totalPackets := 0
		for _, gop := range b.gops {
			response = append(response, gop.packets...)
			totalPackets += len(gop.packets)
		}
		b.log.Debugf(b, "Returning nil seedBuf and %d packets in response", totalPackets)
		return nil, response
	}

	b.log.Debugf(b, "Processing GOP at index %d with %d packets", gopsID, len(b.gops[gopsID].packets))
	var restBuf []gomedia.Packet
	var seedBuf []gomedia.VideoPacket
	for i := range b.gops[gopsID].packets {
		packetStartTime := b.gops[gopsID].packets[i].StartTime()
		if packetStartTime.Before(ts) {
			if vPkt, ok := b.gops[gopsID].packets[i].(gomedia.VideoPacket); ok {
				seedBuf = append(seedBuf, vPkt)
				b.log.Debugf(b, "Added packet %d to seedBuf (start_time=%v < ts=%v)", i, packetStartTime, ts)
			}
		} else {
			restBuf = append(restBuf, b.gops[gopsID].packets[i])
			b.log.Debugf(b, "Added packet %d to restBuf (start_time=%v >= ts=%v)", i, packetStartTime, ts)
		}
	}

	b.log.Debugf(b, "After processing GOP %d: seedBuf=%d packets, restBuf=%d packets", gopsID, len(seedBuf), len(restBuf))

	for i := gopsID + 1; i < len(b.gops); i++ {
		packetsAdded := len(b.gops[i].packets)
		restBuf = append(restBuf, b.gops[i].packets...)
		b.log.Debugf(b, "Added GOP %d (%d packets) to restBuf", i, packetsAdded)
	}

	b.log.Debugf(b, "GetBuffer returning: seedBuf=%d packets, restBuf=%d packets", len(seedBuf), len(restBuf))
	return seedBuf, restBuf
}

func (b *Buffer) Reset() {
	for _, gop := range b.gops {
		for _, pkt := range gop.packets {
			pkt.Release()
		}
	}
	b.gops = b.gops[:0]
	b.duration = 0
}

func (b *Buffer) Close() {
	for _, gop := range b.gops {
		for _, pkt := range gop.packets {
			pkt.Release()
		}
	}
	b.gops = nil
	b.duration = 0
}
