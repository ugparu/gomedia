package opus

import (
	"errors"
	"time"
)

// Opus TOC bit constants
const (
	configShift      = 3    // Number of bits to shift to get the config
	framesTocMask    = 0x3  // Bitmask for the frame count code
	multiFrameMask   = 0x3F // Bitmask for number of frames in mode 3 (RFC 6716 §3.2.5)
	minPacketSize    = 1    // Minimum size of an Opus packet
	minMultiPackSize = 2    // Minimum size for a multi-frame packet (TOC + frame-count byte)
	singleFrameCode  = 0    // TOC code for a single frame (RFC 6716 §3.2.1)
	twoFramesCode    = 1    // TOC code for two frames; code 2 also means two frames
	maxFrameCount    = 48   // Maximum number of frames per packet (RFC 6716 §3.2.5)
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
		// RFC 6716 §3.2.1: Code 0 always contains exactly one frame.
		// A TOC-only packet (0-byte frame body) is a valid DTX frame.
		numFr = 1
	case twoFramesCode, twoFramesCode + 1: // Cases 1 and 2 both indicate two frames
		// RFC 6716 §3.2.2/§3.2.3: Code 1 and Code 2 always contain exactly two frames.
		numFr = 2
	case framesTocMask: // Case 3 - multiple frames
		// RFC 6716 §3.2.5: second byte carries the frame count M.
		if len(pkt) < minMultiPackSize {
			return 0, errors.New("invalid opus packet")
		}
		numFr = int(pkt[1] & multiFrameMask)
		// RFC 6716 §3.2.5: M MUST be in the range 1 to 48.
		if numFr < 1 || numFr > maxFrameCount {
			return 0, errors.New("invalid opus frame count")
		}
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
