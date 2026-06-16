//go:build !cuda

package cuda

import (
	"errors"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/decoder"
	"github.com/ugparu/gomedia/frame/rgb"
)

// errCUDAUnavailable is returned by every method of the stub decoder built
// without the "cuda" build tag. The package stays importable so consumers
// compile cleanly; CUDA decoding only works in binaries built with -tags cuda.
var errCUDAUnavailable = errors.New("cuda decoder unavailable: build with -tags cuda")

type unavailableCUDADecoder struct{}

// NewFFmpegCUDADecoder returns a decoder whose methods all fail with
// errCUDAUnavailable. Build with -tags cuda to get the real implementation.
func NewFFmpegCUDADecoder() decoder.InnerVideoDecoder {
	return unavailableCUDADecoder{}
}

// CheckCuda always reports false in builds without the "cuda" tag.
func CheckCuda() bool {
	return false
}

// InitCuda is a no-op in builds without the "cuda" tag.
func InitCuda(maxMats int) {}

// CloseCuda is a no-op in builds without the "cuda" tag.
func CloseCuda() {}

func (unavailableCUDADecoder) Init(gomedia.VideoCodecParameters) error {
	return errCUDAUnavailable
}

func (unavailableCUDADecoder) Feed(gomedia.VideoPacket) error {
	return errCUDAUnavailable
}

func (unavailableCUDADecoder) Decode(gomedia.VideoPacket) (rgb.ReleasableImage, error) {
	return nil, errCUDAUnavailable
}

func (unavailableCUDADecoder) Close() {}
