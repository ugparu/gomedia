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
	cachedMp4      []byte                      // Lazily generated MP4 data; populated on first HTTP request.
	log            logger.Logger
}

// newFragment creates a new fragment with the specified parameters.
func newFragment(id uint8, segID uint64, targetDuration time.Duration, codecPars gomedia.CodecParametersPair, log logger.Logger) *fragment {
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
		log:            log,
	}
	// Initialize the manifest entry with a preload hint.
	frag.manifestEntry = fmt.Sprintf("#EXT-X-PRELOAD-HINT:TYPE=PART,URI=\"fragment/%d/%d/cubic.m4s\"\n", segID, id)
	return frag
}

// writePacket writes a multimedia packet to the fragment.
// Clone(false) retains the ring-buffer slot so the backing memory stays valid
// until segment.release() is called when the segment is evicted.
func (fr *fragment) writePacket(packet gomedia.Packet) error {
	fr.log.Tracef(fr, "Writing packet %v", packet)

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

// close finalizes the fragment metadata and signals completion.
// MP4 data is NOT generated here — it is produced lazily on first HTTP request
// via generateMp4(), called under the owning segment's mutex.
func (fr *fragment) close() error {
	fr.log.Tracef(fr, "Finishing fragment")

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

	close(fr.finished)
	return nil
}

// generateMp4 lazily produces the MP4 data from retained ring-backed packets.
// Must be called under the owning segment's mu while packets are still live.
func (fr *fragment) generateMp4() {
	if fr.cachedMp4 != nil || len(fr.packets) == 0 {
		return
	}
	mux := fmp4.NewMuxer(fr.log)
	if err := mux.Mux(fr.codecPars); err != nil {
		fr.log.Errorf(fr,"fragment cache: mux error: %v", err)
		return
	}
	for _, pkt := range fr.packets {
		if err := mux.WritePacket(pkt); err != nil {
			fr.log.Errorf(fr,"fragment cache: WritePacket error: %v", err)
		}
	}
	if buf := mux.GetMP4Fragment(int(fr.id)); buf != nil {
		fr.cachedMp4 = make([]byte, len(buf.Data()))
		copy(fr.cachedMp4, buf.Data())
		buf.Release()
	}
}

// getMp4Buffer returns the cached MP4 data for this fragment.
func (fr *fragment) getMp4Buffer() buffer.PooledBuffer {
	return &staticBuffer{fr.cachedMp4}
}

// String returns a string representation of the fragment.
func (fr *fragment) String() string {
	return fmt.Sprintf("FRAGMENT id=%d ind=%v", fr.id, fr.independent)
}
