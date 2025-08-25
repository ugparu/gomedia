package nal

import (
	"github.com/ugparu/gomedia/utils/bits/pio"
)

// Constants for different NALU (Network Abstraction Layer Unit) formats.
const (
	naluRaw    = iota // Raw NALU format.
	naluAVCC          // AVCC NALU format.
	naluANNEXB        // ANNEXB NALU format.
)

// MinNaluSize is the minimum size of a Network Abstraction Layer Unit (NALU).
const MinNaluSize = 4

// isStartCode checks if there's a NALU start code (0x000001 or 0x00000001) at the given position
// and returns the type of start code found (3-byte or 4-byte) and whether a start code was found.
func isStartCode(b []byte, pos int) (startCodeLength int, found bool) {
	if pos+2 >= len(b) || b[pos] != 0 {
		return 0, false
	}

	val3 := pio.U24BE(b[pos:])
	if val3 == 1 {
		return 3, true //nolint:mnd
	}

	if val3 == 0 && pos+3 < len(b) && b[pos+3] == 1 {
		return 4, true //nolint:mnd
	}

	return 0, false
}

// parseANNEXB parses a byte slice in ANNEXB format and returns the NALUs.
func parseANNEXB(b []byte, val3, val4 uint32) [][]byte {
	var nalus [][]byte
	_val3 := val3
	_val4 := val4
	start := 0
	pos := 0
	for {
		if start != pos {
			nalus = append(nalus, b[start:pos])
		}
		if _val3 == 1 {
			pos += 3
		} else if _val4 == 1 {
			pos += 4
		}
		start = pos
		if start == len(b) {
			break
		}
		_val3 = 0
		_val4 = 0
		for pos < len(b) {
			startCodeLength, found := isStartCode(b, pos)
			if found {
				if startCodeLength == 3 { //nolint:mnd
					_val3 = 1
				} else {
					_val4 = 1
				}
				break
			}
			pos++
		}
	}
	return nalus
}

// SplitNALUs splits a byte slice into Network Abstraction Layer Units (NALUs)
// based on different formats (Raw, AVCC, or ANNEXB) and returns the NALUs and the format type.
func SplitNALUs(b []byte) (nalus [][]byte, typ int) {
	// If the byte slice is smaller than the minimum NALU size, consider it as a single raw NALU.
	if len(b) < MinNaluSize {
		return [][]byte{b}, naluRaw
	}

	// Check for AVCC format.
	val4 := pio.U32BE(b)
	if val4 <= uint32(len(b)) { //nolint:gosec
		_val4 := val4
		_b := b[MinNaluSize:]
		nalus = [][]byte{}
		for {
			if _val4 > uint32(len(_b)) { //nolint:gosec
				// For corrupted streams, try to salvage partial NALUs
				if len(_b) > 0 {
					nalus = append(nalus, _b)
				}
				break
			}
			if _val4 > 0 {
				nalus = append(nalus, _b[:_val4])
			}
			_b = _b[_val4:]
			if len(_b) < MinNaluSize {
				break
			}
			_val4 = pio.U32BE(_b)
			_b = _b[MinNaluSize:]
			if _val4 > uint32(len(_b)) { //nolint:gosec
				// For corrupted streams, try to salvage partial NALUs
				if len(_b) > 0 {
					nalus = append(nalus, _b)
				}
				break
			}
		}
		if len(_b) == 0 || len(nalus) > 0 {
			return nalus, naluAVCC
		}
	}

	// Check for ANNEXB format.
	val3 := pio.U24BE(b)
	if val3 == 1 || val4 == 1 {
		nalus = parseANNEXB(b, val3, val4)
		return nalus, naluANNEXB
	}

	// If none of the formats match, consider it as a single raw NALU.
	return [][]byte{b}, naluRaw
}
