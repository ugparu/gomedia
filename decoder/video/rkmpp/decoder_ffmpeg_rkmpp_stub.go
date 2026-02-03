//go:build linux && !arm64

package rkmpp

import (
	"errors"
	"image"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/decoder"
)

var errRKMPPNotSupported = errors.New("rkmpp decoder is only supported on linux/arm64")

type ffmpegRKMPPDecoder struct{}

// NewFFmpegRKMPPDecoder returns a dummy decoder implementation for non-arm64 builds.
// It satisfies decoder.InnerVideoDecoder so packages can compile on other architectures.
func NewFFmpegRKMPPDecoder() decoder.InnerVideoDecoder { //nolint:revive // exported API kept for compatibility
	return &ffmpegRKMPPDecoder{}
}

func (dcd *ffmpegRKMPPDecoder) Init(codecPar gomedia.VideoCodecParameters) error {
	return errRKMPPNotSupported
}

func (dcd *ffmpegRKMPPDecoder) Feed(pkt gomedia.VideoPacket) error {
	return errRKMPPNotSupported
}

func (dcd *ffmpegRKMPPDecoder) Decode(pkt gomedia.VideoPacket) (image.Image, error) {
	return nil, errRKMPPNotSupported
}

func (dcd *ffmpegRKMPPDecoder) Close() {}
