package opus

import (
	"errors"
	"time"
)

// Opus TOC bit constants
const (
	configShift      = 3    // Number of bits to shift to get the config
	stereoMask       = 0x4  // Bitmask to check for stereo
	framesTocMask    = 0x3  // Bitmask for the frame count code
	multiFrameMask   = 0x3F // Bitmask for number of frames in mode 3
	minPacketSize    = 1    // Minimum size of an Opus packet
	minMultiPackSize = 2    // Minimum size for a multi-frame packet
	singleFrameCode  = 0    // TOC code for a single frame
	twoFramesCode    = 1    // TOC code for two frames (same as 2)
	// TOC code 3 is for multiple frames with count in second byte
)

func PacketDuration(pkt []byte) (time.Duration, error) {
	if len(pkt) < minPacketSize {
		return 0, errors.New("empty opus packet")
	}
	toc := pkt[0]
	config := toc >> configShift
	// stereo := (toc & stereoMask) != 0
	code := toc & framesTocMask
	numFr := 0
	switch code {
	case singleFrameCode:
		// one frame
		if len(pkt) > minPacketSize {
			numFr = 1
		}
	case twoFramesCode, twoFramesCode + 1: // Cases 1 and 2 both indicate two frames
		// two frames
		if len(pkt) > minMultiPackSize {
			numFr = 2
		}
	case framesTocMask: // Case 3 - multiple frames
		// N frames
		if len(pkt) < minMultiPackSize {
			return 0, errors.New("invalid opus packet")
		}
		numFr = int(pkt[1] & multiFrameMask)
	}
	return time.Duration(numFr) * opusFrameTimes[config], nil
}

var opusFrameTimes = []time.Duration{
	// SILK NB
	10 * time.Millisecond,
	20 * time.Millisecond,
	40 * time.Millisecond,
	60 * time.Millisecond,
	// SILK MB
	10 * time.Millisecond,
	20 * time.Millisecond,
	40 * time.Millisecond,
	60 * time.Millisecond,
	// SILK WB
	10 * time.Millisecond,
	20 * time.Millisecond,
	40 * time.Millisecond,
	60 * time.Millisecond,
	// Hybrid SWB
	10 * time.Millisecond,
	20 * time.Millisecond,
	// Hybrid FB
	10 * time.Millisecond,
	20 * time.Millisecond,
	// CELT NB
	2500 * time.Microsecond,
	5 * time.Millisecond,
	10 * time.Millisecond,
	20 * time.Millisecond,
	// CELT WB
	2500 * time.Microsecond,
	5 * time.Millisecond,
	10 * time.Millisecond,
	20 * time.Millisecond,
	// CELT SWB
	2500 * time.Microsecond,
	5 * time.Millisecond,
	10 * time.Millisecond,
	20 * time.Millisecond,
	// CELT FB
	2500 * time.Microsecond,
	5 * time.Millisecond,
	10 * time.Millisecond,
	20 * time.Millisecond,
}
