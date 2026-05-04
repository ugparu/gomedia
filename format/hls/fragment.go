package hls

import (
	"fmt"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/format/fmp4"
	"github.com/ugparu/gomedia/utils/buffer"
	"github.com/ugparu/gomedia/utils/logger"
)

// fragment is a sub-segment (LL-HLS "part") within a segment.
// Lifetime: packets retain ring-buffer slots; slots are released only when the
// enclosing segment is evicted.
type fragment struct {
	independent    bool
	id             uint8
	segID          uint64
	targetDuration time.Duration
	duration       time.Duration
	finished       chan struct{}
	manifestEntry  string
	packets        []gomedia.Packet
	codecPars      gomedia.CodecParametersPair
	cachedMp4      []byte // lazily generated on first HTTP request
	mediaName      string // base filename used in manifest URIs
	log            logger.Logger
}

func newFragment(id uint8, segID uint64, targetDuration time.Duration, codecPars gomedia.CodecParametersPair, mediaName string, log logger.Logger) *fragment {
	frag := &fragment{
		id:             id,
		segID:          segID,
		targetDuration: targetDuration,
		finished:       make(chan struct{}),
		packets:        make([]gomedia.Packet, 0),
		codecPars:      codecPars,
		mediaName:      mediaName,
		log:            log,
	}
	// Until the fragment closes, advertise it via a preload hint so LL-HLS clients can block-GET.
	frag.manifestEntry = fmt.Sprintf("#EXT-X-PRELOAD-HINT:TYPE=PART,URI=\"fragment/%d/%d/%s.m4s\"\n", segID, id, mediaName)
	return frag
}

// writePacket retains the packet's ring-buffer slot via Clone(false); the slot
// is released when the enclosing segment is evicted.
func (fr *fragment) writePacket(packet gomedia.Packet) error {
	fr.log.Tracef(fr, "Writing packet %v", packet)

	fr.packets = append(fr.packets, packet.Clone(false))

	vPacket, casted := packet.(gomedia.VideoPacket)
	if casted {
		if vPacket.IsKeyFrame() {
			fr.independent = true
		}
		fr.duration += packet.Duration()
		if fr.duration >= fr.targetDuration {
			return fr.close()
		}
	}

	return nil
}

// close finalizes the manifest entry and unblocks waitFragment.
// MP4 bytes are NOT generated here — that happens lazily on first HTTP request
// in generateMp4, under the owning segment's mutex.
func (fr *fragment) close() error {
	fr.log.Tracef(fr, "Finishing fragment")

	if fr.independent {
		fr.manifestEntry = fmt.Sprintf(
			"#EXT-X-PART:DURATION=%.5f,INDEPENDENT=YES,URI=\"fragment/%d/%d/%s.m4s\"\n",
			fr.duration.Seconds(),
			fr.segID,
			fr.id,
			fr.mediaName,
		)
	} else {
		fr.manifestEntry = fmt.Sprintf(
			"#EXT-X-PART:DURATION=%.5f,URI=\"fragment/%d/%d/%s.m4s\"\n",
			fr.duration.Seconds(),
			fr.segID,
			fr.id,
			fr.mediaName,
		)
	}

	close(fr.finished)
	return nil
}

// generateMp4 encodes the retained packets into fragmented MP4 bytes.
// Idempotent. Must be called under the owning segment's mutex while packets
// are still live (i.e. before the segment is evicted and slots released).
func (fr *fragment) generateMp4() {
	if fr.cachedMp4 != nil || len(fr.packets) == 0 {
		return
	}
	mux := fmp4.NewMuxer(fr.log)
	if err := mux.Mux(fr.codecPars); err != nil {
		fr.log.Errorf(fr, "fragment cache: mux error: %v", err)
		return
	}
	for _, pkt := range fr.packets {
		if err := mux.WritePacket(pkt); err != nil {
			fr.log.Errorf(fr, "fragment cache: WritePacket error: %v", err)
		}
	}
	if buf := mux.GetMP4Fragment(int(fr.id)); buf != nil {
		fr.cachedMp4 = make([]byte, len(buf.Data()))
		copy(fr.cachedMp4, buf.Data())
	}
}

func (fr *fragment) getMp4Buffer() buffer.Buffer {
	return &staticBuffer{fr.cachedMp4}
}

func (fr *fragment) String() string {
	return fmt.Sprintf("FRAGMENT id=%d ind=%v", fr.id, fr.independent)
}
