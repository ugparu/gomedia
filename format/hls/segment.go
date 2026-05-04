package hls

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/format/fmp4"
	"github.com/ugparu/gomedia/utils/buffer"
	"github.com/ugparu/gomedia/utils/logger"
)

// staticBuffer wraps a pre-generated []byte as a read-only PooledBuffer.
// Release is a no-op: the backing slice is owned by the fragment/segment struct
// and its lifetime is managed explicitly via ring-slot reference counting.
type staticBuffer struct{ data []byte }

func (b *staticBuffer) Data() []byte { return b.data }
func (b *staticBuffer) Len() int     { return len(b.data) }
func (b *staticBuffer) Cap() int     { return cap(b.data) }
func (b *staticBuffer) Release()     {}
func (b *staticBuffer) Resize(int)   {}

// segment is one .m4s file in the HLS playlist, composed of one or more fragments
// (LL-HLS parts). Packets are retained by their ring-buffer slots until release().
type segment struct {
	id                 uint64
	targetFragDuration time.Duration
	targetDuration     time.Duration
	duration           time.Duration
	finished           chan struct{}
	curFragment        *fragment
	manifestEntry      string
	codecPars          gomedia.CodecParametersPair
	cacheEntry         string
	fragments          []*fragment
	time               time.Time
	cachedMp4          []byte       // lazily generated full-segment MP4
	mu                 sync.RWMutex // guards fragments, lazy MP4 generation, and release
	released           bool
	discontinuity      bool // true if this segment starts after a codec change
	initVersion        int
	mediaName          string
	blockingTimeout    time.Duration
	log                logger.Logger
}

func newSegment(
	id uint64,
	targetFragmentDuration,
	targetDuration time.Duration,
	codecPars gomedia.CodecParametersPair,
	mediaName string,
	blockingTimeout time.Duration,
	log logger.Logger,
) *segment {
	seg := &segment{
		id:                 id,
		codecPars:          codecPars,
		targetDuration:     targetDuration,
		targetFragDuration: targetFragmentDuration,
		fragments:          []*fragment{newFragment(0, id, targetFragmentDuration, codecPars, mediaName, log)},
		finished:           make(chan struct{}),
		duration:           0,
		time:               time.Now().UTC(),
		cacheEntry:         "",
		curFragment:        nil,
		manifestEntry:      "",
		mediaName:          mediaName,
		blockingTimeout:    blockingTimeout,
		cachedMp4:          nil,
		log:                log,
	}
	seg.manifestEntry = seg.fragments[0].manifestEntry
	seg.curFragment = seg.fragments[0]
	return seg
}

// writePacket forwards the packet to the current fragment and rotates to a new
// fragment when the old one fills. Segment rotation is the muxer's job, not ours.
// Returns true when a fragment boundary was crossed (manifest needs a rebuild).
func (element *segment) writePacket(packet gomedia.Packet) (changed bool, err error) {
	curFrag := element.curFragment
	if err = curFrag.writePacket(packet); err != nil {
		return
	}

	select {
	case <-curFrag.finished:
		element.duration += curFrag.duration
		element.cacheEntry += curFrag.manifestEntry

		newFragID := curFrag.id + 1
		newFrag := newFragment(newFragID, element.id, element.targetFragDuration, element.codecPars, element.mediaName, element.log)

		element.mu.Lock()
		element.fragments = append(element.fragments, newFrag)
		element.curFragment = newFrag
		element.mu.Unlock()

		element.manifestEntry = element.cacheEntry + newFrag.manifestEntry
		changed = true
	default:
	}
	return
}

// closeSeg finishes the current fragment and writes the segment's manifest
// entry. A fragment with zero duration (audio-only tail) is closed silently —
// its samples are still in the fMP4, but not advertised as an LL-HLS part.
func (element *segment) closeSeg() {
	curFrag := element.curFragment
	select {
	case <-curFrag.finished:
		// Already closed and accounted for.
	default:
		if curFrag.duration > 0 {
			_ = curFrag.close()
			element.duration += curFrag.duration
			element.cacheEntry += curFrag.manifestEntry
		} else {
			// Audio-only or empty fragment — close without manifest entry.
			curFrag.duration = 0
			close(curFrag.finished)
		}
	}

	element.manifestEntry = fmt.Sprintf("%s#EXT-X-PROGRAM-DATE-TIME:%s\n#EXTINF:%.5f\nsegment/%d/%s.m4s\n",
		element.cacheEntry, element.time.Format("2006-01-02T15:04:05.000000Z"), element.duration.Seconds(), element.id, element.mediaName)
	_ = element.close()
}

// close signals completion. Packets are kept alive for lazy MP4 generation and
// only freed when release() is called (segment eviction).
func (element *segment) close() (err error) {
	element.log.Tracef(element, "Finishing segment")
	close(element.finished)
	return nil
}

// release frees all retained ring-buffer slots. Safe to call more than once.
// Called on playlist eviction or muxer close.
func (element *segment) release() {
	element.mu.Lock()
	defer element.mu.Unlock()

	if element.released {
		return
	}
	element.released = true

	for _, frag := range element.fragments {
		for _, pkt := range frag.packets {
			pkt.Release()
		}
		frag.packets = nil
	}
}

// getMp4Buffer returns the full-segment MP4, generating it on first call.
// Returns nil once release() has freed the underlying packets.
func (element *segment) getMp4Buffer() buffer.Buffer {
	element.mu.Lock()
	defer element.mu.Unlock()

	if element.cachedMp4 != nil {
		return &staticBuffer{element.cachedMp4}
	}
	if element.released {
		return nil
	}

	mux := fmp4.NewMuxer(element.log)
	if muxErr := mux.Mux(element.codecPars); muxErr != nil {
		element.log.Errorf(element, "segment cache: mux error: %v", muxErr)
		return nil
	}
	for _, frag := range element.fragments {
		for _, pkt := range frag.packets {
			if wErr := mux.WritePacket(pkt); wErr != nil {
				element.log.Errorf(element, "segment cache: WritePacket error: %v", wErr)
			}
		}
	}
	if buf := mux.GetMP4Fragment(int(element.id)); buf != nil {
		element.cachedMp4 = make([]byte, len(buf.Data()))
		copy(element.cachedMp4, buf.Data())
	}

	return &staticBuffer{element.cachedMp4}
}

// getFragment blocks until fragment id closes, then returns its MP4 bytes.
// Blocks up to blockingTimeout (LL-HLS block-GET). Returns nil if the packets
// have been released or the timeout elapses.
func (element *segment) getFragment(ctx context.Context, id uint8) buffer.Buffer {
	element.mu.RLock()
	if id >= uint8(len(element.fragments)) {
		element.mu.RUnlock()
		return nil
	}
	frag := element.fragments[id]
	element.mu.RUnlock()

	ctx, cancel := context.WithTimeout(ctx, element.blockingTimeout)
	defer cancel()

	select {
	case <-ctx.Done():
		return nil
	case <-frag.finished:
	}

	element.mu.Lock()
	defer element.mu.Unlock()

	if frag.cachedMp4 != nil {
		return frag.getMp4Buffer()
	}
	if element.released {
		return nil
	}

	frag.generateMp4()
	return frag.getMp4Buffer()
}

// waitFragment blocks until fragment id closes, the segment finishes, or ctx is cancelled.
func (element *segment) waitFragment(ctx context.Context, id uint8) {
	for {
		element.mu.RLock()
		curFrag := element.curFragment
		element.mu.RUnlock()

		if curFrag.id >= id {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-element.finished:
			return
		case <-curFrag.finished:
			if curFrag.id == id {
				return
			}
		}
	}
}

func (element *segment) String() string {
	return fmt.Sprintf("SEGMENT id=%d frgs=%d", element.id, len(element.fragments))
}
