package codec

import (
	"bytes"
	"fmt"
	"os"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/utils/buffer"
)

type BasePacket[T gomedia.CodecParameters] struct {
	Idx          uint8
	RelativeTime time.Duration
	Dur          time.Duration
	InpURL       string
	Buffer       buffer.PooledBuffer
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
		newPkt.Buffer = buffer.Get(len(pkt.Buffer.Data()))
		copy(newPkt.Buffer.Data(), pkt.Buffer.Data())
	} else {
		newPkt.Buffer = pkt.Buffer
		newPkt.Buffer.Retain()
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

func BuffersEqual(a, b buffer.PooledBuffer) bool {
	if a == nil || b == nil {
		return a == b
	}
	da, db := a.Data(), b.Data()
	if len(da) != len(db) {
		return false
	}
	return bytes.Equal(da, db)
}

func (pkt *BasePacket[T]) SwitchToFile(f *os.File, offset int64, size int64, closeFn func() error) (err error) {
	// Sync file to ensure writes are flushed before mmap
	if err = f.Sync(); err != nil {
		return err
	}

	buf, err := buffer.GetMmap(f, offset, int(size), closeFn)
	if err != nil {
		return err
	}
	println(BuffersEqual(pkt.Buffer, buf))

	// pkt.Buffer.Release()
	// pkt.Buffer = buf

	return nil
}

func (pkt *BasePacket[T]) Close() {
	pkt.Buffer.Release()
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
