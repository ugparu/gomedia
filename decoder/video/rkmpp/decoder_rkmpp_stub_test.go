//go:build linux && !arm64

package rkmpp_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ugparu/gomedia/decoder/video/rkmpp"
)

// The stub keeps the package importable off Rockchip: consumers select the
// backend at runtime via CheckRKMPP, so it must report false and its decoder
// must fail loudly instead of pretending to decode.

func TestStub_CheckRKMPP_False(t *testing.T) {
	t.Parallel()
	require.False(t, rkmpp.CheckRKMPP())
}

func TestStub_Decoder_MethodsFail(t *testing.T) {
	t.Parallel()

	d := rkmpp.NewFFmpegRKMPPDecoder()
	require.NotNil(t, d)

	require.Error(t, d.Init(nil))
	require.Error(t, d.Feed(nil))

	img, err := d.Decode(nil)
	require.Error(t, err)
	require.Nil(t, img)

	require.NotPanics(t, d.Close)
}
