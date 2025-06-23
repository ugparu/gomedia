package h265

import (
	"errors"

	"github.com/ugparu/gomedia/utils/bits/pio"
)

func IsDataNALU(b []byte) bool {
	typ := b[0] & 0x1f //nolint:mnd // 0x1f is a mask for the NAL unit type in H.265
	return typ >= 1 && typ <= 5
}

var StartCodeBytes = []byte{0, 0, 1}
var AUDBytes = []byte{0, 0, 0, 1, 0x9, 0xf0, 0, 0, 0, 1} // AUD

type HEVCDecoderConfRecord struct {
	AVCProfileIndication uint8
	ProfileCompatibility uint8
	AVCLevelIndication   uint8
	LengthSizeMinusOne   uint8
	VPS                  [][]byte
	SPS                  [][]byte
	PPS                  [][]byte
}

var ErrDecconfInvalid = errors.New("h265parser: AVCDecoderConfRecord invalid")

func (avc *HEVCDecoderConfRecord) Unmarshal(b []byte) (n int, err error) {
	if len(b) < 30 { //nolint:mnd // 30 is the minimum size for a valid AVCDecoderConfRecord
		err = ErrDecconfInvalid
		return
	}
	avc.AVCProfileIndication = b[1]
	avc.ProfileCompatibility = b[2]
	avc.AVCLevelIndication = b[3]
	avc.LengthSizeMinusOne = b[4] & 0x03 //nolint:mnd // 0x03 is a mask for the length size minus one field

	vpscount := int(b[25] & 0x1f) //nolint:mnd // 0x1f is a mask for the VPS count
	n += 26
	for range vpscount {
		if len(b) < n+2 {
			err = ErrDecconfInvalid
			return
		}
		vpslen := int(pio.U16BE(b[n:]))
		n += 2

		if len(b) < n+vpslen {
			err = ErrDecconfInvalid
			return
		}
		avc.VPS = append(avc.VPS, b[n:n+vpslen])
		n += vpslen
	}

	if len(b) < n+1 {
		err = ErrDecconfInvalid
		return
	}

	n++
	n++

	spscount := int(b[n])
	n++

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

	n++
	n++

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

func (avc *HEVCDecoderConfRecord) Len() (n int) {
	n = 23
	for _, sps := range avc.SPS {
		n += 5 + len(sps) //nolint:mnd // 5 is the size of the header for each SPS
	}
	for _, pps := range avc.PPS {
		n += 5 + len(pps) //nolint:mnd // 5 is the size of the header for each PPS
	}
	for _, vps := range avc.VPS {
		n += 5 + len(vps) //nolint:mnd // 5 is the size of the header for each VPS
	}
	return
}

func (avc *HEVCDecoderConfRecord) Marshal(b []byte) (n int) {
	b[0] = 1
	b[1] = avc.AVCProfileIndication
	b[2] = avc.ProfileCompatibility
	b[3] = avc.AVCLevelIndication
	b[21] = 3
	b[22] = 3
	n += 23
	if len(avc.VPS[0]) > 0 {
		b[n] = (avc.VPS[0][0] >> 1) & 0x3f //nolint:mnd // 0x3f is a mask for the VPS NAL unit type
		n++
		b[n] = byte(len(avc.VPS) >> 8) //nolint:mnd // 8 bits for the high byte of the VPS count
		n++
		b[n] = byte(len(avc.VPS))
		n++
		for _, vps := range avc.VPS {
			// Use a safe length value to avoid overflow
			vpsLen := len(vps)
			if vpsLen > 65535 { //nolint:mnd // 65535 is the maximum value for uint16
				vpsLen = 65535
			}
			pio.PutU16BE(b[n:], uint16(vpsLen)) //nolint:gosec // We've already checked that vpsLen <= 65535
			n += 2
			copy(b[n:], vps)
			n += len(vps)
		}
	}
	b[n] = (avc.SPS[0][0] >> 1) & 0x3f //nolint:mnd // 0x3f is a mask for the SPS NAL unit type
	n++
	b[n] = byte(len(avc.SPS) >> 8) //nolint:mnd // 8 bits for the high byte of the SPS count
	n++
	b[n] = byte(len(avc.SPS))
	n++
	for _, sps := range avc.SPS {
		// Use a safe length value to avoid overflow
		spsLen := len(sps)
		if spsLen > 65535 { //nolint:mnd // 65535 is the maximum value for uint16
			spsLen = 65535
		}
		pio.PutU16BE(b[n:], uint16(spsLen)) //nolint:gosec // We've already checked that spsLen <= 65535
		n += 2
		copy(b[n:], sps)
		n += len(sps)
	}
	b[n] = (avc.PPS[0][0] >> 1) & 0x3f //nolint:mnd // 0x3f is a mask for the PPS NAL unit type
	n++
	b[n] = byte(len(avc.PPS) >> 8) //nolint:mnd // 8 bits for the high byte of the PPS count
	n++
	b[n] = byte(len(avc.PPS))
	n++
	for _, pps := range avc.PPS {
		// Use a safe length value to avoid overflow
		ppsLen := min(len(pps), 65535)      //nolint:mnd // 65535 is the maximum value for uint16
		pio.PutU16BE(b[n:], uint16(ppsLen)) //nolint:gosec // We've already checked that ppsLen <= 65535
		n += 2
		copy(b[n:], pps)
		n += len(pps)
	}
	return
}
