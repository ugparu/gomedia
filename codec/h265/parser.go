//nolint:mnd // This file contains many magic numbers that are part of the H.265 specification
package h265

import (
	"bytes"
	"errors"

	"github.com/ugparu/gomedia/utils/bits"
)

var (
	ErrH265IncorectUnitSize = errors.New("invorect Unit Size")
	ErrH265IncorectUnitType = errors.New("incorect Unit Type")
)

//nolint:gocyclo,cyclop // This function is complex due to the H.265 specification requirements
func ParseSPS(sps []byte) (ctx SPSInfo, err error) {
	if len(sps) < 2 { //nolint:mnd // 2 is the minimum size for a valid SPS
		err = ErrH265IncorectUnitSize
		return
	}
	rbsp := nal2rbsp(sps[2:])
	br := &bits.GolombBitReader{R: bytes.NewReader(rbsp)}
	if _, err = br.ReadBits(4); err != nil { //nolint:mnd // 4 bits for sps_video_parameter_set_id
		return
	}
	spsMaxSubLayersMinus1, err := br.ReadBits(3) //nolint:mnd // 3 bits for sps_max_sub_layers_minus1
	if err != nil {
		return
	}

	if spsMaxSubLayersMinus1+1 > ctx.NumTemporalLayers {
		ctx.NumTemporalLayers = spsMaxSubLayersMinus1 + 1
	}
	if ctx.TemporalIDNested, err = br.ReadBit(); err != nil {
		return
	}
	if err = parsePTL(br, &ctx, spsMaxSubLayersMinus1); err != nil {
		return
	}
	if _, err = br.ReadExponentialGolombCode(); err != nil {
		return
	}
	var cf uint
	if cf, err = br.ReadExponentialGolombCode(); err != nil {
		return
	}
	ctx.ChromaFormat = cf
	// 3 is the value for chroma_format_idc that requires separate_colour_plane_flag
	if ctx.ChromaFormat == 3 {
		if _, err = br.ReadBit(); err != nil {
			return
		}
	}
	if ctx.PicWidthInLumaSamples, err = br.ReadExponentialGolombCode(); err != nil {
		return
	}
	ctx.Width = ctx.PicWidthInLumaSamples
	if ctx.PicHeightInLumaSamples, err = br.ReadExponentialGolombCode(); err != nil {
		return
	}
	ctx.Height = ctx.PicHeightInLumaSamples
	conformanceWindowFlag, err := br.ReadBit()
	if err != nil {
		return
	}
	if conformanceWindowFlag != 0 {
		if _, err = br.ReadExponentialGolombCode(); err != nil {
			return
		}
		if _, err = br.ReadExponentialGolombCode(); err != nil {
			return
		}
		if _, err = br.ReadExponentialGolombCode(); err != nil {
			return
		}
		if _, err = br.ReadExponentialGolombCode(); err != nil {
			return
		}
	}

	var bdlm8 uint
	if bdlm8, err = br.ReadExponentialGolombCode(); err != nil {
		return
	}
	ctx.bitDepthChromaMinus8 = bdlm8
	var bdcm8 uint
	if bdcm8, err = br.ReadExponentialGolombCode(); err != nil {
		return
	}
	ctx.bitDepthChromaMinus8 = bdcm8

	_, err = br.ReadExponentialGolombCode()
	if err != nil {
		return
	}
	spsSubLayerOrderingInfoPresentFlag, err := br.ReadBit()
	if err != nil {
		return
	}
	var i uint
	if spsSubLayerOrderingInfoPresentFlag != 0 {
		i = 0
	} else {
		i = spsMaxSubLayersMinus1
	}
	for ; i <= spsMaxSubLayersMinus1; i++ {
		if _, err = br.ReadExponentialGolombCode(); err != nil {
			return
		}
		if _, err = br.ReadExponentialGolombCode(); err != nil {
			return
		}
		if _, err = br.ReadExponentialGolombCode(); err != nil {
			return
		}
	}

	if _, err = br.ReadExponentialGolombCode(); err != nil {
		return
	}
	if _, err = br.ReadExponentialGolombCode(); err != nil {
		return
	}
	if _, err = br.ReadExponentialGolombCode(); err != nil {
		return
	}
	if _, err = br.ReadExponentialGolombCode(); err != nil {
		return
	}
	if _, err = br.ReadExponentialGolombCode(); err != nil {
		return
	}
	if _, err = br.ReadExponentialGolombCode(); err != nil {
		return
	}
	return
}

func parsePTL(br *bits.GolombBitReader, ctx *SPSInfo, maxSubLayersMinus1 uint) error {
	var err error
	var ptl SPSInfo
	if ptl.GeneralProfileSpace, err = br.ReadBits(2); err != nil { //nolint:mnd // 2 bits for general_profile_space
		return err
	}
	if ptl.GeneralTierFlag, err = br.ReadBit(); err != nil {
		return err
	}
	if ptl.GeneralProfileIDC, err = br.ReadBits(5); err != nil { //nolint:mnd // 5 bits for general_profile_idc
		return err
	}
	// 32 bits for general_profile_compatibility_flag
	if ptl.GeneralProfileCompatibilityFlags, err = br.ReadBits32(32); err != nil {
		return err
	}
	// 48 bits for general_constraint_indicator_flags
	if ptl.GeneralConstraintIndicatorFlags, err = br.ReadBits64(48); err != nil {
		return err
	}
	if ptl.GeneralLevelIDC, err = br.ReadBits(8); err != nil { //nolint:mnd // 8 bits for general_level_idc
		return err
	}
	updatePTL(ctx, &ptl)
	if maxSubLayersMinus1 == 0 {
		return nil
	}
	subLayerProfilePresentFlag := make([]uint, maxSubLayersMinus1)
	subLayerLevelPresentFlag := make([]uint, maxSubLayersMinus1)
	for i := range maxSubLayersMinus1 {
		if subLayerProfilePresentFlag[i], err = br.ReadBit(); err != nil {
			return err
		}
		if subLayerLevelPresentFlag[i], err = br.ReadBit(); err != nil {
			return err
		}
	}
	if maxSubLayersMinus1 > 0 {
		for i := maxSubLayersMinus1; i < 8; i++ {
			if _, err = br.ReadBits(2); err != nil { //nolint:mnd // 2 bits for reserved_zero_2bits
				return err
			}
		}
	}
	for i := range maxSubLayersMinus1 {
		if subLayerProfilePresentFlag[i] != 0 {
			// 32 bits for sub_layer_profile_space, sub_layer_tier_flag, sub_layer_profile_idc,
			// and sub_layer_profile_compatibility_flag
			if _, err = br.ReadBits32(32); err != nil {
				return err
			}
			if _, err = br.ReadBits32(32); err != nil { //nolint:mnd // 32 bits for sub_layer_constraint_indicator_flags
				return err
			}
			if _, err = br.ReadBits32(24); err != nil { //nolint:mnd // 24 bits for sub_layer_constraint_indicator_flags
				return err
			}
		}

		if subLayerLevelPresentFlag[i] != 0 {
			if _, err = br.ReadBits(8); err != nil { //nolint:mnd // 8 bits for sub_layer_level_idc
				return err
			}
		}
	}
	return nil
}

func updatePTL(ctx, ptl *SPSInfo) {
	ctx.GeneralProfileSpace = ptl.GeneralProfileSpace

	if ptl.GeneralTierFlag > ctx.GeneralTierFlag {
		ctx.GeneralLevelIDC = ptl.GeneralLevelIDC
		ctx.GeneralTierFlag = ptl.GeneralTierFlag
	} else if ptl.GeneralLevelIDC > ctx.GeneralLevelIDC {
		ctx.GeneralLevelIDC = ptl.GeneralLevelIDC
	}

	if ptl.GeneralProfileIDC > ctx.GeneralProfileIDC {
		ctx.GeneralProfileIDC = ptl.GeneralProfileIDC
	}

	ctx.GeneralProfileCompatibilityFlags &= ptl.GeneralProfileCompatibilityFlags

	ctx.GeneralConstraintIndicatorFlags &= ptl.GeneralConstraintIndicatorFlags
}
