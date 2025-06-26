package mjpeg

import (
	"fmt"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec"
)

// CodecParameters represents MJPEG codec parameters
type CodecParameters struct {
	codec.BaseParameters
	width  uint
	height uint
	fps    uint
}

// NewCodecParameters creates new MJPEG codec parameters
func NewCodecParameters(width, height, fps uint) *CodecParameters {
	codecPar := &CodecParameters{
		width:  width,
		height: height,
		fps:    fps,
	}
	codecPar.CodecType = gomedia.MJPEG

	// Calculate bitrate estimation for MJPEG
	// MJPEG typically uses higher bitrates than compressed codecs
	codecPar.BRate = uint(float64(width) * float64(height) * float64(fps) * 0.5)

	return codecPar
}

// Width returns the video frame width in pixels
func (par *CodecParameters) Width() uint {
	return par.width
}

// Height returns the video frame height in pixels
func (par *CodecParameters) Height() uint {
	return par.height
}

// FPS returns the video frame rate
func (par *CodecParameters) FPS() uint {
	return par.fps
}

// Tag returns a string tag representing the codec information
func (par *CodecParameters) Tag() string {
	return fmt.Sprintf("mjpeg.%dx%d", par.width, par.height)
}
