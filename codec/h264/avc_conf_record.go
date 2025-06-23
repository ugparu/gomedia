package h264

import "github.com/ugparu/gomedia/utils/bits/pio"

// AVCDecoderConfRecord represents the HEVC (High-Efficiency Video Coding) decoder configuration record.
type AVCDecoderConfRecord struct {
	AVCProfileIndication uint8    // Profile indication for the AVC stream.
	ProfileCompatibility uint8    // Profile compatibility for the AVC stream.
	AVCLevelIndication   uint8    // Level indication for the AVC stream.
	LengthSizeMinusOne   uint8    // Length size (in bytes) minus one for the AVC stream.
	SPS                  [][]byte // Sequence Parameter Sets (SPS) containing the SPS NALUs.
	PPS                  [][]byte // Picture Parameter Sets (PPS) containing the PPS NALUs.
}

// Unmarshal decodes the binary representation of AVCDecoderConfRecord from the given byte slice.
// It returns the number of bytes read and any decoding error encountered.
func (avc *AVCDecoderConfRecord) Unmarshal(b []byte) (n int, err error) {
	const minLength = 7
	if len(b) < minLength {
		err = ErrDecconfInvalid
		return
	}

	avc.AVCProfileIndication = b[1]
	avc.ProfileCompatibility = b[2]
	avc.AVCLevelIndication = b[3]
	avc.LengthSizeMinusOne = b[4] & maskLengthSizeMinusOne
	spscount := int(b[5] & maskSPSCount)
	n += 6

	for range spscount {
		if len(b) < n+2 {
			err = ErrDecconfInvalid
			return
		}
		spslen := int(pio.U16BE(b[n:]))
		n += 2

		if len(b) < n+spslen {
			err = ErrDecconfInvalid
			return
		}
		avc.SPS = append(avc.SPS, b[n:n+spslen])
		n += spslen
	}

	if len(b) < n+1 {
		err = ErrDecconfInvalid
		return
	}
	ppscount := int(b[n])
	n++

	for range ppscount {
		if len(b) < n+2 {
			err = ErrDecconfInvalid
			return
		}
		ppslen := int(pio.U16BE(b[n:]))
		n += 2

		if len(b) < n+ppslen {
			err = ErrDecconfInvalid
			return
		}
		avc.PPS = append(avc.PPS, b[n:n+ppslen])
		n += ppslen
	}

	return
}

// Len calculates and returns the length of the binary representation of AVCDecoderConfRecord.
// It includes the length of the fixed-size fields and the lengths of SPS and PPS data.
func (avc *AVCDecoderConfRecord) Len() (n int) {
	n = 7
	for _, sps := range avc.SPS {
		n += lengthFieldSize + len(sps)
	}
	for _, pps := range avc.PPS {
		n += lengthFieldSize + len(pps)
	}
	return
}

// Marshal serializes the AVCDecoderConfRecord to a binary representation.
// It writes the serialized data to the provided byte slice and returns the number of bytes written.
func (avc *AVCDecoderConfRecord) Marshal(b []byte) (n int) {
	b[0] = 1
	b[1] = avc.AVCProfileIndication
	b[2] = avc.ProfileCompatibility
	b[3] = avc.AVCLevelIndication
	b[4] = avc.LengthSizeMinusOne | maskLengthSizeMinusOneInv
	b[5] = uint8(len(avc.SPS)) | maskSPSCountInv //nolint:gosec // integer overflow for sps count is not possible
	n += 6

	for _, sps := range avc.SPS {
		pio.PutU16BE(b[n:], uint16(len(sps))) //nolint:gosec // integer overflow for sps length is not possible
		n += 2
		copy(b[n:], sps)
		n += len(sps)
	}

	b[n] = uint8(len(avc.PPS)) //nolint:gosec // integer overflow for pps count is not possible
	n++

	for _, pps := range avc.PPS {
		pio.PutU16BE(b[n:], uint16(len(pps))) //nolint:gosec // integer overflow for pps length is not possible
		n += 2
		copy(b[n:], pps)
		n += len(pps)
	}

	return
}
