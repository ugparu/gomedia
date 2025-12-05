package codec

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/utils/buffer"
)

// sharedBuffer holds the buffer and reference count, shared between packet clones.
// This ensures all clones see the same buffer when it's swapped (e.g., via SwitchToFile).
type sharedBuffer struct {
	buf buffer.PooledBuffer
	ref int32
	mu  *sync.RWMutex
}

type BasePacket[T gomedia.CodecParameters] struct {
	Idx          uint8
	RelativeTime time.Duration
	Dur          time.Duration
	InpURL       string
	shared       *sharedBuffer // shared between clones
	AbsoluteTime time.Time
	CodecPar     T
}

// NewBasePacket creates a new BasePacket with the given buffer properly initialized.
func NewBasePacket[T gomedia.CodecParameters](
	idx uint8,
	relativeTime time.Duration,
	dur time.Duration,
	url string,
	buf buffer.PooledBuffer,
	absTime time.Time,
	codecPar T,
) BasePacket[T] {
	return BasePacket[T]{
		Idx:          idx,
		RelativeTime: relativeTime,
		Dur:          dur,
		InpURL:       url,
		shared:       &sharedBuffer{buf: buf, ref: 1, mu: &sync.RWMutex{}},
		AbsoluteTime: absTime,
		CodecPar:     codecPar,
	}
}

func (pkt *BasePacket[T]) Clone(copyData bool) BasePacket[T] {
	newPkt := BasePacket[T]{
		Idx:          pkt.Idx,
		RelativeTime: pkt.RelativeTime,
		Dur:          pkt.Dur,
		InpURL:       pkt.InpURL,
		shared:       nil,
		AbsoluteTime: pkt.AbsoluteTime,
		CodecPar:     pkt.CodecPar,
	}
	if copyData {
		buf := buffer.Get(len(pkt.shared.buf.Data()))
		copy(buf.Data(), pkt.shared.buf.Data())
		newPkt.shared = &sharedBuffer{buf: buf, ref: 1, mu: &sync.RWMutex{}}
	} else {
		// Share the same buffer - all clones will see buffer changes (e.g., SwitchToFile)
		atomic.AddInt32(&pkt.shared.ref, 1)
		newPkt.shared = pkt.shared
	}
	return newPkt
}

func (pkt *BasePacket[T]) Len() int {
	return pkt.shared.buf.Len()
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

// View предоставляет безопасный доступ к данным буфера.
// Слайс b валиден ТОЛЬКО внутри функции fn.
// Не сохраняйте b и не выносите его за пределы fn.
func (pkt *BasePacket[T]) View(fn func(b buffer.PooledBuffer)) {
	// Блокируем чтение указателя pkt.shared.buf
	pkt.shared.mu.RLock()
	defer pkt.shared.mu.RUnlock()

	// Внутри лока данные гарантированно существуют и не будут зарелизины
	if pkt.shared.buf != nil {
		fn(pkt.shared.buf)
	} else {
		fn(nil)
	}
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
	if pkt == nil || pkt.shared == nil {
		return "EMPTY_PACKET"
	}
	return fmt.Sprintf("PACKET sz=%d", pkt.shared.buf.Len())
}

// Retain increases the reference count. Use this when you need to keep a reference
// to the packet beyond its original scope.
func (pkt *BasePacket[T]) Retain() {
	atomic.AddInt32(&pkt.shared.ref, 1)
}

func (pkt *BasePacket[T]) SwitchToFile(f *os.File, offset int64, size int64, closeFn func() error) (err error) {
	pkt.shared.mu.Lock()
	defer pkt.shared.mu.Unlock()

	buf, err := buffer.GetMmap(f, offset, int(size), closeFn)
	if err != nil {
		return err
	}

	// Replace the buffer in the shared structure.
	// All clones will see this change since they share the same sharedBuffer pointer.
	oldBuf := pkt.shared.buf
	pkt.shared.buf = buf
	oldBuf.Release()

	return nil
}

func (pkt *BasePacket[T]) Close() {
	if pkt.shared == nil {
		return
	}
	count := atomic.AddInt32(&pkt.shared.ref, -1)
	if count == 0 {
		pkt.shared.buf.Release()
	} else if count < 0 {
		panic("packet reference count is negative")
	}
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
