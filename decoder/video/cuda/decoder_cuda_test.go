package cuda_test

import (
	"encoding/base64"
	"encoding/json"
	"image"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	codech264 "github.com/ugparu/gomedia/codec/h264"
	codech265 "github.com/ugparu/gomedia/codec/h265"
	"github.com/ugparu/gomedia/decoder/video/cuda"
	"github.com/ugparu/gomedia/mocks"
)

const (
	h264DataDir    = "../../../tests/data/h264/"
	h265DataDir    = "../../../tests/data/hevc/"
	h264AACDataDir = "../../../tests/data/h264_aac/"

	cudaMaxMats = 4 //nolint:mnd // enough slots for parallel tests
)

// ---------------------------------------------------------------------------
// TestMain — initialize CUDA once for the entire test binary
// ---------------------------------------------------------------------------

func TestMain(m *testing.M) {
	if !cuda.CheckCuda() {
		os.Exit(0)
	}
	cuda.InitCuda(cudaMaxMats)
	defer cuda.CloseCuda()
	os.Exit(m.Run())
}

// ---------------------------------------------------------------------------
// JSON fixtures (shared structure with cpu tests)
// ---------------------------------------------------------------------------

type videoParamsJSON struct {
	Video struct {
		StreamIndex uint8  `json:"stream_index"`
		SPS         string `json:"sps"`
		PPS         string `json:"pps"`
		VPS         string `json:"vps"`
	} `json:"video"`
}

type packetJSON struct {
	Codec       string `json:"codec"`
	StreamIndex uint8  `json:"stream_index"`
	TimestampNs int64  `json:"timestamp_ns"`
	IsKeyframe  bool   `json:"is_keyframe"`
	Size        int    `json:"size"`
	Data        string `json:"data"`
}

type packetsFileJSON struct {
	Packets []packetJSON `json:"packets"`
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func decodeBase64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	require.NoError(t, err)
	return b
}

func loadH264Params(t *testing.T, dir string) *codech264.CodecParameters {
	t.Helper()
	raw, err := os.ReadFile(dir + "parameters.json")
	require.NoError(t, err)
	var f videoParamsJSON
	require.NoError(t, json.Unmarshal(raw, &f))

	sps := decodeBase64(t, f.Video.SPS)
	pps := decodeBase64(t, f.Video.PPS)
	cp, err := codech264.NewCodecDataFromSPSAndPPS(sps, pps)
	require.NoError(t, err)
	cp.SetStreamIndex(f.Video.StreamIndex)
	return &cp
}

func loadH265Params(t *testing.T) *codech265.CodecParameters {
	t.Helper()
	raw, err := os.ReadFile(h265DataDir + "parameters.json")
	require.NoError(t, err)
	var f videoParamsJSON
	require.NoError(t, json.Unmarshal(raw, &f))

	vps := decodeBase64(t, f.Video.VPS)
	sps := decodeBase64(t, f.Video.SPS)
	pps := decodeBase64(t, f.Video.PPS)
	cp, err := codech265.NewCodecDataFromVPSAndSPSAndPPS(vps, sps, pps)
	require.NoError(t, err)
	cp.SetStreamIndex(f.Video.StreamIndex)
	return &cp
}

func loadPackets(t *testing.T, dir string) []packetJSON {
	t.Helper()
	raw, err := os.ReadFile(dir + "packets.json")
	require.NoError(t, err)
	var f packetsFileJSON
	require.NoError(t, json.Unmarshal(raw, &f))
	require.NotEmpty(t, f.Packets)
	return f.Packets
}

func makeH264Packet(t *testing.T, p packetJSON, par *codech264.CodecParameters) *codech264.Packet {
	t.Helper()
	data := decodeBase64(t, p.Data)
	require.Equal(t, p.Size, len(data))
	return codech264.NewPacket(p.IsKeyframe, time.Duration(p.TimestampNs), time.Time{}, data, "test", par)
}

func makeH265Packet(t *testing.T, p packetJSON, par *codech265.CodecParameters) *codech265.Packet {
	t.Helper()
	data := decodeBase64(t, p.Data)
	require.Equal(t, p.Size, len(data))
	return codech265.NewPacket(p.IsKeyframe, time.Duration(p.TimestampNs), time.Time{}, data, "test", par)
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

func TestNewFFmpegCUDADecoder_NotNil(t *testing.T) {
	t.Parallel()
	d := cuda.NewFFmpegCUDADecoder()
	require.NotNil(t, d)
}

// ---------------------------------------------------------------------------
// Close
// ---------------------------------------------------------------------------

// TestCUDA_Close_Double verifies that calling Close twice is safe.
// The Go wrapper sets dcd.dcd = nil after the first Close, so the second call
// is a no-op regardless of CUDA availability.
func TestCUDA_Close_Double(t *testing.T) {
	t.Parallel()

	par := loadH264Params(t, h264AACDataDir)
	d := cuda.NewFFmpegCUDADecoder()
	require.NoError(t, d.Init(par))
	d.Close()
	require.NotPanics(t, d.Close)
}

// ---------------------------------------------------------------------------
// Init
// ---------------------------------------------------------------------------

func TestCUDA_Init_H264_Valid(t *testing.T) {
	t.Parallel()

	par := loadH264Params(t, h264DataDir)
	d := cuda.NewFFmpegCUDADecoder()
	defer d.Close()
	require.NoError(t, d.Init(par))
}

func TestCUDA_Init_H265_Valid(t *testing.T) {
	t.Parallel()

	par := loadH265Params(t)
	d := cuda.NewFFmpegCUDADecoder()
	defer d.Close()
	require.NoError(t, d.Init(par))
}

func TestCUDA_Init_UnsupportedCodecType_Error(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	par := mocks.NewMockVideoCodecParameters(ctrl)
	par.EXPECT().Width().Return(uint(640)).AnyTimes()
	par.EXPECT().Height().Return(uint(480)).AnyTimes()

	d := cuda.NewFFmpegCUDADecoder()
	defer d.Close()
	err := d.Init(par)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported codec type")
}

func TestCUDA_Init_Reinit_H264(t *testing.T) {
	t.Parallel()

	par := loadH264Params(t, h264DataDir)
	d := cuda.NewFFmpegCUDADecoder()
	defer d.Close()
	require.NoError(t, d.Init(par))
	require.NoError(t, d.Init(par))
}

// ---------------------------------------------------------------------------
// Decode — H264
// ---------------------------------------------------------------------------

// TestCUDA_Decode_H264_ProducesImage feeds H264 packets (first video packet is
// IDR in the h264_aac dataset) and asserts at least one image is returned.
func TestCUDA_Decode_H264_ProducesImage(t *testing.T) {
	t.Parallel()

	par := loadH264Params(t, h264AACDataDir)
	pkts := loadPackets(t, h264AACDataDir)

	d := cuda.NewFFmpegCUDADecoder()
	defer d.Close()
	require.NoError(t, d.Init(par))

	const maxFrames = 30
	var got image.Image
	for i, p := range pkts {
		if p.Codec != "H264" || i >= maxFrames {
			continue
		}
		pkt := makeH264Packet(t, p, par)
		img, err := d.Decode(pkt)
		if err != nil && err.Error() != "need more data to decode frame" {
			require.NoError(t, err, "frame %d returned unexpected error", i)
		}
		if img != nil {
			got = img
			break
		}
	}
	require.NotNil(t, got, "expected at least one decoded image from H264 CUDA stream")
	require.True(t, got.Bounds().Dx() > 0 && got.Bounds().Dy() > 0)
}

// TestCUDA_Decode_H264_ErrNeedMoreData feeds non-IDR packets before any IDR
// and verifies only ErrNeedMoreData (not fatal errors) are returned.
func TestCUDA_Decode_H264_ErrNeedMoreData(t *testing.T) {
	t.Parallel()

	par := loadH264Params(t, h264DataDir)
	pkts := loadPackets(t, h264DataDir)

	d := cuda.NewFFmpegCUDADecoder()
	defer d.Close()
	require.NoError(t, d.Init(par))

	const maxNonIDR = 10
	for i, p := range pkts {
		if i >= maxNonIDR || p.IsKeyframe {
			break
		}
		pkt := makeH264Packet(t, p, par)
		img, err := d.Decode(pkt)
		if err != nil {
			require.EqualError(t, err, "need more data to decode frame",
				"frame %d: unexpected error", i)
		}
		_ = img
	}
}

// ---------------------------------------------------------------------------
// Feed — H264
// ---------------------------------------------------------------------------

func TestCUDA_Feed_H264_KeyFrame(t *testing.T) {
	t.Parallel()

	par := loadH264Params(t, h264AACDataDir)
	pkts := loadPackets(t, h264AACDataDir)

	d := cuda.NewFFmpegCUDADecoder()
	defer d.Close()
	require.NoError(t, d.Init(par))

	for _, p := range pkts {
		if p.Codec != "H264" || !p.IsKeyframe {
			continue
		}
		pkt := makeH264Packet(t, p, par)
		require.NoError(t, d.Feed(pkt))
		break
	}
}

// ---------------------------------------------------------------------------
// Decode — H265
// ---------------------------------------------------------------------------

// TestCUDA_Decode_H265_ProducesImage feeds H265 packets (first packet is IDR)
// and asserts at least one image is returned.
func TestCUDA_Decode_H265_ProducesImage(t *testing.T) {
	t.Parallel()

	par := loadH265Params(t)
	pkts := loadPackets(t, h265DataDir)

	d := cuda.NewFFmpegCUDADecoder()
	defer d.Close()
	require.NoError(t, d.Init(par))

	const maxFrames = 5
	var got image.Image
	for i, p := range pkts {
		if i >= maxFrames {
			break
		}
		pkt := makeH265Packet(t, p, par)
		img, err := d.Decode(pkt)
		if err != nil && err.Error() != "need more data to decode frame" {
			require.NoError(t, err, "frame %d returned unexpected error", i)
		}
		if img != nil {
			got = img
			break
		}
	}
	require.NotNil(t, got, "expected at least one decoded image from H265 CUDA stream")
	require.True(t, got.Bounds().Dx() > 0 && got.Bounds().Dy() > 0)
}

// ---------------------------------------------------------------------------
// Feed — H265
// ---------------------------------------------------------------------------

func TestCUDA_Feed_H265_KeyFrame(t *testing.T) {
	t.Parallel()

	par := loadH265Params(t)
	pkts := loadPackets(t, h265DataDir)

	d := cuda.NewFFmpegCUDADecoder()
	defer d.Close()
	require.NoError(t, d.Init(par))

	pkt := makeH265Packet(t, pkts[0], par)
	require.NoError(t, d.Feed(pkt))
}
