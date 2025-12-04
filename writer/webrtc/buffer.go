package webrtc

import (
	"time"

	"github.com/ugparu/gomedia"
)

type GoP struct {
	packets  []gomedia.Packet
	duration time.Duration
}

type Buffer struct {
	gops           []GoP
	duration       time.Duration
	targetDuration time.Duration
}

func (b *Buffer) AddPacket(packet gomedia.Packet) {
	vPkt, ok := packet.(gomedia.VideoPacket)
	if ok && vPkt.IsKeyFrame() {
		b.gops = append(b.gops, GoP{})
		defer b.AdjustSize()
	}

	if len(b.gops) == 0 {
		return
	}

	b.gops[len(b.gops)-1].packets = append(b.gops[len(b.gops)-1].packets, packet)
	if ok {
		b.gops[len(b.gops)-1].duration += packet.Duration()
		b.duration += packet.Duration()
	}
}

func (b *Buffer) AdjustSize() {
	if b.duration < b.targetDuration {
		return
	}

	if b.duration-b.gops[0].duration < b.targetDuration {
		return
	}

	b.duration -= b.gops[0].duration
	gop := b.gops[0]
	b.gops = b.gops[1:]
	for _, packet := range gop.packets {
		packet.Close()
	}
}

func (b *Buffer) GetBuffer(ts time.Time) ([]gomedia.VideoPacket, []gomedia.Packet) {
	gopsID := len(b.gops)
	for i := range b.gops {
		if b.gops[i].packets[0].StartTime().After(ts) {
			gopsID = i
			break
		}
	}
	gopsID--
	if gopsID < 0 {
		var response []gomedia.Packet
		for _, gop := range b.gops {
			response = append(response, gop.packets...)
		}
		return nil, response
	}

	var restBuf []gomedia.Packet
	var seedBuf []gomedia.VideoPacket
	for i := range b.gops[gopsID].packets {
		if b.gops[gopsID].packets[i].StartTime().Before(ts) {
			if vPkt, ok := b.gops[gopsID].packets[i].(gomedia.VideoPacket); ok {
				seedBuf = append(seedBuf, vPkt)
			}
		} else {
			restBuf = append(restBuf, b.gops[gopsID].packets[i])
		}
	}

	for i := gopsID + 1; i < len(b.gops); i++ {
		restBuf = append(restBuf, b.gops[i].packets...)
	}

	return seedBuf, restBuf
}

func (b *Buffer) Reset() {
	b.gops = []GoP{}
	b.duration = 0
}
