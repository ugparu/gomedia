package codec

import (
	"fmt"
	"os"
	"time"

	"github.com/ugparu/gomedia"
)

type BasePacket[T gomedia.CodecParameters] struct {
	Idx          uint8
	RelativeTime time.Duration
	Dur          time.Duration
	InpURL       string
	Buffer       RefBuffer
	AbsoluteTime time.Time
	CodecPar     T
}

func (pkt *BasePacket[T]) Clone(copyData bool) BasePacket[T] {
	newPkt := BasePacket[T]{
		Idx:          pkt.Idx,
		RelativeTime: pkt.RelativeTime,
		Dur:          pkt.Dur,
		InpURL:       pkt.InpURL,
		Buffer:       nil,
		AbsoluteTime: pkt.AbsoluteTime,
		CodecPar:     pkt.CodecPar,
	}
	if copyData {
		newPkt.Buffer = GetMemBuffer()
		newPkt.Buffer.SetData(pkt.Buffer.Data())
	} else {
		newPkt.Buffer = pkt.Buffer
	}
	return newPkt
}

func (pkt *BasePacket[T]) URL() string {
	return pkt.InpURL
}

func (pkt *BasePacket[T]) SetURL(url string) {
	pkt.InpURL = url
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

func (pkt *BasePacket[T]) Data() []byte {
	return pkt.Buffer.Data()
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
	if pkt == nil {
		return "EMPTY_PACKET"
	}
	return fmt.Sprintf("PACKET sz=%d", pkt.Buffer.Len())
}

func (pkt *BasePacket[T]) SwitchToMmap(f *os.File, offset int64, size int64) (err error) {
	buf := GetFileBuffer(f, offset, int(size))
	if buf == nil {
		return fmt.Errorf("failed to mmap file at offset %d with size %d", offset, size)
	}
	pkt.Buffer = buf
	return nil
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
