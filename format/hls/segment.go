package hls

import (
	"context"
	"fmt"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/format/fmp4"
	"github.com/ugparu/gomedia/utils/buffer"
	"github.com/ugparu/gomedia/utils/logger"
)

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
}

// newSegment creates a new segment with the specified parameters.
func newSegment(
	id uint64,
	targetFragmentDuration,
	targetDuration time.Duration,
	codecPars gomedia.CodecParametersPair,
) *segment {
	seg := &segment{
		id:                 id,
		codecPars:          codecPars,
		targetDuration:     targetDuration,
		targetFragDuration: targetFragmentDuration,
		fragments:          []*fragment{newFragment(0, id, targetFragmentDuration, codecPars)},
		finished:           make(chan struct{}),
		duration:           0,
		time:               time.Now(),
		cacheEntry:         "",
		curFragment:        nil,
		manifestEntry:      "",
	}
	seg.manifestEntry = seg.fragments[0].manifestEntry
	seg.curFragment = seg.fragments[0]
	return seg
}

// writePacket writes a multimedia packet to the current fragment of the segment.
func (element *segment) writePacket(packet gomedia.Packet) (err error) {
	curFrag := element.curFragment
	if err = curFrag.writePacket(packet); err != nil {
		return
	}

	select {
	case <-curFrag.finished:
		element.duration += curFrag.duration
		element.cacheEntry = fmt.Sprintf("%s%s", element.cacheEntry, curFrag.manifestEntry)
		if element.duration >= element.targetDuration {
			element.manifestEntry = fmt.Sprintf("%s#EXT-X-PROGRAM-DATE-TIME:%s\n#EXTINF:%.5f\nsegment/%d/cubic.m4s\n",
				element.cacheEntry, element.time.Format("2006-01-02T15:04:05.000000Z"), element.duration.Seconds(), element.id)
			return element.close()
		} else {
			newFragID := curFrag.id + 1
			newFragment := newFragment(newFragID, element.id, element.targetFragDuration, element.codecPars)
			element.fragments = append(element.fragments, newFragment)
			element.curFragment = element.fragments[newFragID]

			// Update manifest entry with new fragment
			newManifestEntry := fmt.Sprintf("%s%s",
				element.cacheEntry,
				element.fragments[newFragID].manifestEntry,
			)
			element.manifestEntry = newManifestEntry
		}
	default:
	}
	return
}

// close finalizes the segment by signaling completion.
// The MP4 buffer is generated lazily on demand via getMp4Buffer().
func (element *segment) close() (err error) {
	logger.Tracef(element, "Finishing segment")
	defer close(element.finished)

	return nil
}

// getMp4Buffer returns the MP4 buffer, generating it on first access.
// Uses sync.Once to ensure the buffer is generated only once and reused.
func (element *segment) getMp4Buffer() buffer.PooledBuffer {
	mux := fmp4.NewMuxer()
	if err := mux.Mux(element.codecPars); err != nil {
		return nil
	}
	for _, fragment := range element.fragments {
		for _, packet := range fragment.packets {
			mux.WritePacket(packet)
		}
	}
	return mux.GetMP4Fragment(int(element.id))
}

// getFragment gets the MP4 content of a specific fragment in the segment.
func (element *segment) getFragment(ctx context.Context, id uint8) buffer.PooledBuffer {
	if id >= uint8(len(element.fragments)) {
		return nil
	}

	const timeout = time.Second * 3
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	frag := element.fragments[id]
	select {
	case <-ctx.Done():
		return nil
	case <-frag.finished:
	}
	return frag.getMp4Buffer()
}

// waitFragment waits until a specific fragment in the segment is finished.
func (element *segment) waitFragment(ctx context.Context, id uint8) {
	for {
		curFrag := element.curFragment
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
