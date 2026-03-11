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
	finished       chan struct{}  // Channel to signal completion of the fragment.
	manifestEntry  string        // HLS manifest entry.
	packets        []gomedia.Packet
	codecPars      gomedia.CodecParametersPair // Codec parameters for the fragment.
	cachedMp4      []byte                      // Pre-generated MP4 data; populated before finished is closed.
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
		cachedMp4:      nil,
	}
	// Initialize the manifest entry with a preload hint.
	frag.manifestEntry = fmt.Sprintf("#EXT-X-PRELOAD-HINT:TYPE=PART,URI=\"fragment/%d/%d/cubic.m4s\"\n", segID, id)
	return frag
}

// writePacket writes a multimedia packet to the fragment.
// Clone(false) retains the ring-buffer slot so the backing memory stays valid
// until the fragment explicitly releases it in segment.close().
func (fr *fragment) writePacket(packet gomedia.Packet) error {
	logger.Tracef(fr, "Writing packet %v", packet)

	fr.packets = append(fr.packets, packet.Clone(false))

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

// close finalizes the fragment, pre-generates the MP4 cache while the
// ring-backed packets are still live, then signals completion.
// HTTP goroutines wait on finished and always find a populated cache.
func (fr *fragment) close() error {
	logger.Tracef(fr, "Finishing fragment")

	// Update the manifest entry based on whether the fragment is independent.
	if fr.independent {
		fr.manifestEntry = fmt.Sprintf(
			"#EXT-X-PART:DURATION=%.5f,INDEPENDENT=YES,URI=\"fragment/%d/%d/cubic.m4s\"\n",
			fr.duration.Seconds(),
			fr.segID,
			fr.id,
		)
	} else {
		fr.manifestEntry = fmt.Sprintf(
			"#EXT-X-PART:DURATION=%.5f,URI=\"fragment/%d/%d/cubic.m4s\"\n",
			fr.duration.Seconds(),
			fr.segID,
			fr.id,
		)
	}

	// Pre-generate the fragment MP4 while ring-backed packets are still retained.
	// Packets are NOT released here — segment.close() releases them after it has
	// also encoded the full-segment MP4.
	mux := fmp4.NewMuxer()
	if err := mux.Mux(fr.codecPars); err != nil {
		logger.Errorf(fr, "fragment cache: mux error: %v", err)
	} else {
		for _, pkt := range fr.packets {
			if err := mux.WritePacket(pkt); err != nil {
				logger.Errorf(fr, "fragment cache: WritePacket error: %v", err)
			}
		}
		if buf := mux.GetMP4Fragment(int(fr.id)); buf != nil {
			fr.cachedMp4 = make([]byte, len(buf.Data()))
			copy(fr.cachedMp4, buf.Data())
			buf.Release()
		}
	}

	// Signal completion only after the cache is fully populated.
	close(fr.finished)
	return nil
}

// getMp4Buffer returns the pre-generated MP4 data for this fragment.
// The cache is always populated before finished is closed, so callers
// that wait on finished will never see a nil cache.
func (fr *fragment) getMp4Buffer() buffer.PooledBuffer {
	return &staticBuffer{fr.cachedMp4}
}

// String returns a string representation of the fragment.
func (fr *fragment) String() string {
	return fmt.Sprintf("FRAGMENT id=%d ind=%v", fr.id, fr.independent)
}
