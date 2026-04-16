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

// segment represents a segment of an HLS video stream.
type segment struct {
	id                 uint64                      // Identifier for the segment.
	targetFragDuration time.Duration               // Target duration for each fragment in the segment.
	targetDuration     time.Duration               // Target duration for the entire segment.
	duration           time.Duration               // Actual duration of the segment.
	finished           chan struct{}               // Channel to signal completion of the segment.
	curFragment        *fragment                   // Atomic value to store the current fragment.
	manifestEntry      string                      // HLS manifest entry for the segment.
	codecPars          gomedia.CodecParametersPair // Codec parameters for the segment.
	cacheEntry         string                      // Cache entry for manifest generation.
	fragments          []*fragment                 // List of fragments in the segment.
	time               time.Time                   // Time when the segment was created.
	cachedMp4          []byte                      // Lazily generated full-segment MP4.
	mu                 sync.RWMutex                // Protects fragments slice, lazy MP4 generation, and packet release.
	released           bool                        // True after packets have been released.
	discontinuity      bool                        // True if this segment starts after a codec change.
	initVersion        int                         // Init segment version this segment belongs to.
	mediaName          string                      // Base filename used in manifest URIs (e.g. "media").
	blockingTimeout    time.Duration               // Timeout for blocking fragment requests.
	log                logger.Logger
}

// newSegment creates a new segment with the specified parameters.
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

// writePacket writes a multimedia packet to the current fragment of the segment.
// Segment closure is NOT handled here — it is driven by the muxer based on
// target duration (and optionally deferred to a keyframe when configured).
// Returns true if a fragment was closed and the manifest needs rebuilding.
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

// closeSeg finalizes the segment when the muxer triggers a rotation. Any
// in-progress fragment is closed: if it contains video data it becomes the
// last part of this segment; otherwise it is silently closed (audio-only
// data remains in the segment's fMP4 but is not listed as a part).
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

// close finalizes the segment metadata and signals completion.
// MP4 data is NOT generated here — it is produced lazily on first HTTP request.
// Packets are NOT released here — they stay alive for lazy generation and are
// released in release() when the segment is evicted from the playlist.
func (element *segment) close() (err error) {
	element.log.Tracef(element, "Finishing segment")
	close(element.finished)
	return nil
}

// release frees all retained ring-buffer packet slots.
// Called when the segment is evicted from the playlist or the muxer is closed.
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

// getMp4Buffer lazily generates and returns the full-segment MP4 data.
// Returns nil if the segment's packets have already been released.
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

// getFragment lazily generates and returns the MP4 content of a specific fragment.
// Returns nil if the segment's packets have already been released.
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

// waitFragment waits until a specific fragment in the segment is finished.
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
			// Explicitly return when context is done to prevent deadlocks
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

// String returns a string representation of the segment for debugging purposes.
func (element *segment) String() string {
	return fmt.Sprintf("SEGMENT id=%d frgs=%d", element.id, len(element.fragments))
}
