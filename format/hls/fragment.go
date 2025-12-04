package hls

import (
	"fmt"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/format/fmp4"
	"github.com/ugparu/gomedia/utils/buffer"
	"github.com/ugparu/gomedia/utils/logger"
)

// fragment represents a fragment of an HLS video stream.
type fragment struct {
	independent    bool          // Indicates if the fragment is independent.
	id             uint8         // Identifier for the fragment.
	segID          uint64        // Identifier for the segment to which the fragment belongs.
	targetDuration time.Duration // Target duration for the fragment.
	duration       time.Duration // Actual duration of the fragment.
	finished       chan struct{} // Channel to signal completion of the fragment.
	manifestEntry  string        // HLS manifest entry.
	packets        []gomedia.Packet
	codecPars      gomedia.CodecParametersPair // Codec parameters for the fragment.
}

// newFragment creates a new fragment with the specified parameters.
func newFragment(id uint8, segID uint64, targetDuration time.Duration, codecPars gomedia.CodecParametersPair) *fragment {
	frag := &fragment{
		id:             id,
		segID:          segID,
		targetDuration: targetDuration,
		independent:    false,
		duration:       0,
		finished:       make(chan struct{}),
		manifestEntry:  "",
		packets:        make([]gomedia.Packet, 0),
		codecPars:      codecPars,
	}
	// Initialize the manifest entry with a preload hint.
	frag.manifestEntry = fmt.Sprintf("#EXT-X-PRELOAD-HINT:TYPE=PART,URI=\"fragment/%d/%d/cubic.m4s\"\n", segID, id)
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

// close finalizes the fragment and updates the HLS manifest entry.
// The MP4 buffer is generated lazily on demand via getMp4Buffer().
func (fr *fragment) close() error {
	logger.Tracef(fr, "Finishing fragment")
	defer close(fr.finished)

	// Update the manifest entry based on whether the fragment is independent.
	if fr.independent {
		// Build manifest entry with INDEPENDENT=YES flag
		fr.manifestEntry = fmt.Sprintf(
			"#EXT-X-PART:DURATION=%.5f,INDEPENDENT=YES,URI=\"fragment/%d/%d/cubic.m4s\"\n",
			fr.duration.Seconds(),
			fr.segID,
			fr.id,
		)
	} else {
		// Build standard manifest entry
		fr.manifestEntry = fmt.Sprintf(
			"#EXT-X-PART:DURATION=%.5f,URI=\"fragment/%d/%d/cubic.m4s\"\n",
			fr.duration.Seconds(),
			fr.segID,
			fr.id,
		)
	}
	return nil
}

// getMp4Buffer returns the MP4 buffer, generating it on first access.
// Uses sync.Once to ensure the buffer is generated only once and reused.
func (fr *fragment) getMp4Buffer() buffer.PooledBuffer {
	mux := fmp4.NewMuxer()
	if err := mux.Mux(fr.codecPars); err != nil {
		return nil
	}
	for _, packet := range fr.packets {
		mux.WritePacket(packet)
	}
	return mux.GetMP4Fragment(int(fr.id))
}

// String returns a string representation of the fragment.
func (fr *fragment) String() string {
	return fmt.Sprintf("FRAGMENT id=%d ind=%v", fr.id, fr.independent)
}
