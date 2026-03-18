package codec

import (
	"fmt"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/utils/buffer"
)

type BasePacket[T gomedia.CodecParameters] struct {
	Idx          uint8
	RelativeTime time.Duration
	Dur          time.Duration
	InpSourceID  string
	Buf          []byte // Returns the packet data.
	AbsoluteTime time.Time
	CodecPar     T
	// Slot is non-nil for ring-backed packets. Shared across Clone(false) copies;
	// each owner must call Release() exactly once.
	Slot *buffer.SlotHandle
}

// NewBasePacket creates a new heap-backed BasePacket (Slot is nil; Release is a no-op).
func NewBasePacket[T gomedia.CodecParameters](
	idx uint8,
	relativeTime time.Duration,
	dur time.Duration,
	sourceID string,
	buf []byte,
	absTime time.Time,
	codecPar T,
) BasePacket[T] {
	return BasePacket[T]{
		Idx:          idx,
		RelativeTime: relativeTime,
		Dur:          dur,
		InpSourceID:  sourceID,
		Buf:          buf,
		AbsoluteTime: absTime,
		CodecPar:     codecPar,
	}
}

// Clone copies the packet metadata. When copyData is false the clone shares the
// same Buf and Slot (Retain is called to add an owner). When copyData is true a
// new heap buffer is allocated and the clone is independent (Slot is nil).
func (pkt *BasePacket[T]) Clone(copyData bool) BasePacket[T] {
	newPkt := BasePacket[T]{
		Idx:          pkt.Idx,
		RelativeTime: pkt.RelativeTime,
		Dur:          pkt.Dur,
		InpSourceID:  pkt.InpSourceID,
		Buf:          pkt.Buf,
		AbsoluteTime: pkt.AbsoluteTime,
		CodecPar:     pkt.CodecPar,
	}
	if copyData {
		newPkt.Buf = make([]byte, len(pkt.Buf))
		copy(newPkt.Buf, pkt.Buf)
		// Slot stays nil — independent heap copy, Release is a no-op.
	} else {
		newPkt.Slot = pkt.Slot
		pkt.Slot.Retain() // register the clone as an additional owner
	}
	return newPkt
}

// Release decrements the reference count of the backing ring slot. When it
// reaches zero the slab region is recycled. Safe to call on heap-backed packets
// (Slot == nil) — it is a no-op.
func (pkt *BasePacket[T]) Release() {
	pkt.Slot.Release()
}

func (pkt *BasePacket[T]) Len() int {
	return len(pkt.Buf)
}

func (pkt *BasePacket[T]) Data() []byte {
	return pkt.Buf
}

func (pkt *BasePacket[T]) SourceID() string {
	return pkt.InpSourceID
}

func (pkt *BasePacket[T]) SetSourceID(sourceID string) {
	pkt.InpSourceID = sourceID
}

func (pkt *BasePacket[T]) StreamIndex() uint8 {
	return pkt.Idx
}

func (pkt *BasePacket[T]) SetStreamIndex(idx uint8) {
	pkt.Idx = idx
}

func (pkt *BasePacket[T]) StartTime() time.Time {
	return pkt.AbsoluteTime
}

func (pkt *BasePacket[T]) SetStartTime(t time.Time) {
	pkt.AbsoluteTime = t
}

func (pkt *BasePacket[T]) Timestamp() time.Duration {
	return pkt.RelativeTime
}

func (pkt *BasePacket[T]) SetDuration(dur time.Duration) {
	pkt.Dur = dur
}

func (pkt *BasePacket[T]) SetTimestamp(ts time.Duration) {
	pkt.RelativeTime = ts
}

func (pkt *BasePacket[T]) Duration() time.Duration {
	return pkt.Dur
}

func (pkt *BasePacket[T]) String() string {
	if pkt == nil || pkt.Buf == nil {
		return "EMPTY_PACKET"
	}
	return fmt.Sprintf("PACKET sz=%d", len(pkt.Buf))
}

type VideoPacket[T gomedia.VideoCodecParameters] struct {
	BasePacket[T]
	IsKeyFrm bool
}

func (pkt *VideoPacket[T]) Clone(copyData bool) VideoPacket[T] {
	return VideoPacket[T]{
		BasePacket: pkt.BasePacket.Clone(copyData),
		IsKeyFrm:   pkt.IsKeyFrm,
	}
}

func (pkt *VideoPacket[T]) CodecParameters() gomedia.VideoCodecParameters {
	return pkt.CodecPar
}

func (pkt *VideoPacket[T]) IsKeyFrame() bool {
	return pkt.IsKeyFrm
}

type AudioPacket[T gomedia.AudioCodecParameters] struct {
	BasePacket[T]
}

func (pkt *AudioPacket[T]) Clone(copyData bool) AudioPacket[T] {
	return AudioPacket[T]{
		BasePacket: pkt.BasePacket.Clone(copyData),
	}
}

func (pkt *AudioPacket[T]) CodecParameters() gomedia.AudioCodecParameters {
	return pkt.CodecPar
}
