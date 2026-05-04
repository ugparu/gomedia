package mjpeg

import (
	"fmt"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec"
)

type CodecParameters struct {
	codec.BaseParameters
	width  uint
	height uint
	fps    uint
}

func NewCodecParameters(width, height, fps uint) *CodecParameters {
	codecPar := &CodecParameters{
		width:  width,
		height: height,
		fps:    fps,
	}
	codecPar.CodecType = gomedia.MJPEG

	// MJPEG has no inter-frame prediction, so per-pixel cost is roughly constant;
	// 0.5 bits/px/frame is a rough average for typical camera JPEG quality.
	codecPar.BRate = uint(float64(width) * float64(height) * float64(fps) * 0.5) //nolint:mnd

	return codecPar
}

func (par *CodecParameters) Width() uint {
	return par.width
}

func (par *CodecParameters) Height() uint {
	return par.height
}

func (par *CodecParameters) FPS() uint {
	return par.fps
}

func (par *CodecParameters) Tag() string {
	return fmt.Sprintf("mjpeg.%dx%d", par.width, par.height)
}
