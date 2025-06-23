package codec

import (
	"fmt"
	"math"

	"github.com/ugparu/gomedia"
)

type BaseParameters struct {
	Index uint8
	BRate uint
	gomedia.CodecType
}

func (par *BaseParameters) SetStreamIndex(idx uint8) {
	par.Index = idx
}

func (par *BaseParameters) StreamIndex() uint8 {
	if par == nil {
		return math.MaxUint8
	}
	return par.Index
}

func (par *BaseParameters) Type() gomedia.CodecType {
	if par == nil {
		return math.MaxUint32
	}
	return par.CodecType
}

func (par *BaseParameters) SetBitrate(br uint) {
	par.BRate = br
}

func (par *BaseParameters) Bitrate() uint {
	if par == nil {
		return 0
	}
	return par.BRate
}

func (par *BaseParameters) String() string {
	if par == nil {
		return "EMPTY_CODEC_PARAMETERS"
	}
	return fmt.Sprintf("CODEC_PARAMETERS codec=%v", par.CodecType)
}
