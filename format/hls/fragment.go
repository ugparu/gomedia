package hls

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/format/fmp4"
	"github.com/ugparu/gomedia/utils/logger"
)

// fragment represents a fragment of an HLS video stream.
type fragment struct {
	independent    bool             // Indicates if the fragment is independent.
	id             uint8            // Identifier for the fragment.
	segID          uint64           // Identifier for the segment to which the fragment belongs.
	targetDuration time.Duration    // Target duration for the fragment.
	duration       time.Duration    // Actual duration of the fragment.
	finished       chan struct{}    // Channel to signal completion of the fragment.
	manifestEntry  *atomic.Value    // Atomic value for storing the HLS manifest entry.
	packets        []gomedia.Packet // List of multimedia packets in the fragment.
	mux            *fmp4.Muxer
	mp4Buff        []byte // Buffer for the finalized MP4 content.
}

// newFragment creates a new fragment with the specified parameters.
func newFragment(id uint8, segID uint64, targetDuration time.Duration, mux *fmp4.Muxer) *fragment {
	frag := &fragment{
		id:             id,
		segID:          segID,
		targetDuration: targetDuration,
		independent:    false,
		duration:       0,
		packets:        nil,
		finished:       make(chan struct{}),
		mp4Buff:        nil,
		manifestEntry:  &atomic.Value{},
		mux:            mux,
	}
	// Initialize the manifest entry with a preload hint.
	frag.manifestEntry.Store(fmt.Sprintf("#EXT-X-PRELOAD-HINT:TYPE=PART,URI=\"fragment/%d/%d/cubic.m4s\"\n", segID, id))
	return frag
}

// writePacket writes a multimedia packet to the fragment.
func (fr *fragment) writePacket(packet gomedia.Packet) error {
	logger.Tracef(fr, "Writing packet %v", packet)

	fr.packets = append(fr.packets, packet)

	vPacket, casted := packet.(gomedia.VideoPacket)

	// Check if the packet is a keyframe for video packets.
	if casted {
		if vPacket.IsKeyFrame() {
			fr.independent = true
		}
		fr.duration += packet.Duration()
		// Check if the fragment duration exceeds the target duration.
		if fr.duration >= fr.targetDuration {
			return fr.close()
		}
	}

	return nil
}

// close finalizes the fragment, muxes packets into an MP4 buffer, and updates the HLS manifest entry.
func (fr *fragment) close() error {
	logger.Tracef(fr, "Finishing fragment")
	defer close(fr.finished)

	for _, v := range fr.packets {
		if err := fr.mux.WritePacket(v); err != nil {
			return err
		}
	}
	// Finalize the MP4 buffer.
	fr.mp4Buff = fr.mux.GetMP4Fragment()
	// Update the manifest entry based on whether the fragment is independent.
	if fr.independent {
		// Build manifest entry with INDEPENDENT=YES flag
		fr.manifestEntry.Store(fmt.Sprintf(
			"#EXT-X-PART:DURATION=%.5f,INDEPENDENT=YES,URI=\"fragment/%d/%d/cubic.m4s\"\n",
			fr.duration.Seconds(),
			fr.segID,
			fr.id,
		))
	} else {
		// Build standard manifest entry
		fr.manifestEntry.Store(fmt.Sprintf(
			"#EXT-X-PART:DURATION=%.5f,URI=\"fragment/%d/%d/cubic.m4s\"\n",
			fr.duration.Seconds(),
			fr.segID,
			fr.id,
		))
	}
	return nil
}

// String returns a string representation of the fragment.
func (fr *fragment) String() string {
	return fmt.Sprintf("FRAGMENT id=%d ind=%v pkts=%d", fr.id, fr.independent, len(fr.packets))
}
