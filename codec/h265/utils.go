package h265

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/ugparu/gomedia/utils/bits"
)

const (
	NalUnitCodedSliceTrailR    = 1
	NalUnitCodedSliceTsaN      = 2
	NalUnitCodedSliceTsaR      = 3
	NalUnitCodedSliceStsaN     = 4
	NalUnitCodedSliceStsaR     = 5
	NalUnitCodedSliceRadlN     = 6
	NalUnitCodedSliceRadlR     = 7
	NalUnitCodedSliceRaslN     = 8
	NalUnitCodedSliceRaslR     = 9
	NalUnitReservedVclN10      = 10
	NalUnitReservedVclR11      = 11
	NalUnitReservedVclN12      = 12
	NalUnitReservedVclR13      = 13
	NalUnitReservedVclN14      = 14
	NalUnitReservedVclR15      = 15
	NalUnitCodedSliceBlaWLp    = 16
	NalUnitCodedSliceBlaWRadl  = 17
	NalUnitCodedSliceBlaNLp    = 18
	NalUnitCodedSliceIdrWRadl  = 19
	NalUnitCodedSliceIdrNLp    = 20
	NalUnitCodedSliceCra       = 21
	NalUnitReservedIrapVcl22   = 22
	NalUnitReservedIrapVcl23   = 23
	NalUnitReservedVcl24       = 24
	NalUnitReservedVcl25       = 25
	NalUnitReservedVcl26       = 26
	NalUnitReservedVcl27       = 27
	NalUnitReservedVcl28       = 28
	NalUnitReservedVcl29       = 29
	NalUnitReservedVcl30       = 30
	NalUnitReservedVcl31       = 31
	NalUnitVps                 = 32
	NalUnitSps                 = 33
	NalUnitPps                 = 34
	NalUnitAccessUnitDelimiter = 35
	NalUnitEos                 = 36
	NalUnitEob                 = 37
	NalUnitFillerData          = 38
	NalUnitPrefixSei           = 39
	NalUnitSuffixSei           = 40
	NalUnitReservedNvcl41      = 41
	NalUnitReservedNvcl42      = 42
	NalUnitReservedNvcl43      = 43
	NalUnitReservedNvcl44      = 44
	NalUnitReservedNvcl45      = 45
	NalUnitReservedNvcl46      = 46
	NalUnitReservedNvcl47      = 47
	NalUnitUnspecified48       = 48
	NalFU                      = 49
	NalUnitUnspecified50       = 50
	NalUnitUnspecified51       = 51
	NalUnitUnspecified52       = 52
	NalUnitUnspecified53       = 53
	NalUnitUnspecified54       = 54
	NalUnitUnspecified55       = 55
	NalUnitUnspecified56       = 56
	NalUnitUnspecified57       = 57
	NalUnitUnspecified58       = 58
	NalUnitUnspecified59       = 59
	NalUnitUnspecified60       = 60
	NalUnitUnspecified61       = 61
	NalUnitUnspecified62       = 62
	NalUnitUnspecified63       = 63
	NalUnitInvalid             = 64

	MaxVPSCount  = 16
	MaxSubLayers = 7
	MaxSPSCount  = 32

	bitrateEstimationFactor = 1.71 // Estimation factor for H265 encoding
	referenceFrameRate      = 30.0 // Reference frame rate in FPS
	kbpsToBpsMultiplier     = 1000 // Conversion from kbps to bps
)

type SPSInfo struct {
	ProfileIdc                       uint
	LevelIdc                         uint
	MbWidth                          uint
	MbHeight                         uint
	CropLeft                         uint
	CropRight                        uint
	CropTop                          uint
	CropBottom                       uint
	Width                            uint
	Height                           uint
	NumTemporalLayers                uint
	TemporalIDNested                 uint
	ChromaFormat                     uint
	PicWidthInLumaSamples            uint
	PicHeightInLumaSamples           uint
	bitDepthChromaMinus8             uint
	GeneralProfileSpace              uint
	GeneralTierFlag                  uint
	GeneralProfileIDC                uint
	GeneralProfileCompatibilityFlags uint32
	GeneralConstraintIndicatorFlags  uint64
	GeneralLevelIDC                  uint
	FPS                              uint
}

type SliceType uint

func (st SliceType) String() string {
	switch st {
	case SliceP:
		return "P"
	case SliceB:
		return "B"
	case SliceI:
		return "I"
	}
	return ""
}

const (
	SliceP = iota + 1
	SliceB
	SliceI
)

func nal2rbsp(nal []byte) []byte {
	return bytes.ReplaceAll(nal, []byte{0x0, 0x0, 0x3}, []byte{0x0, 0x0})
}

func ParseSliceHeaderFromNALU(packet []byte) (sliceType SliceType, err error) {
	if len(packet) <= 1 {
		err = errors.New("h265parser: packet too short to parse slice header")
		return
	}
	nalUnitTypy := packet[0] & 0x1f //nolint:mnd // 0x1f is a mask for the NAL unit type in H.265
	switch nalUnitTypy {
	case 1, 2, 5, 19: //nolint:mnd // These are NAL unit types that contain slice headers
	default:
		err = fmt.Errorf("h265parser: nal_unit_type=%d has no slice header", nalUnitTypy)
		return
	}

	r := &bits.GolombBitReader{R: bytes.NewReader(packet[1:])}
	if _, err = r.ReadExponentialGolombCode(); err != nil {
		return
	}
	var u uint
	if u, err = r.ReadExponentialGolombCode(); err != nil {
		return
	}

	switch u {
	case 0, 3, 5, 8: //nolint:mnd // These values correspond to P slice types
		sliceType = SliceP
	case 1, 6: //nolint:mnd // These values correspond to B slice types
		sliceType = SliceB
	case 2, 4, 7, 9: //nolint:mnd // These values correspond to I slice types
		sliceType = SliceI
	default:
		err = fmt.Errorf("h265parser: slice_type=%d invalid", u)
		return
	}
	return
}

func IsKey(naluType byte) bool {
	return naluType >= NalUnitCodedSliceBlaWLp && naluType <= NalUnitCodedSliceCra
}
