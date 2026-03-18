package cpu_test

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
	"github.com/ugparu/gomedia/decoder/video/cpu"
	"github.com/ugparu/gomedia/mocks"
)

const (
	h264DataDir    = "../../../tests/data/h264/"
	h265DataDir    = "../../../tests/data/hevc/"
	h264AACDataDir = "../../../tests/data/h264_aac/"
)

// ---------------------------------------------------------------------------
// JSON fixtures
// ---------------------------------------------------------------------------

type videoParamsJSON struct {
	Video struct {
		StreamIndex uint8  `json:"stream_index"`
		Record      string `json:"record"`
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
	ts := time.Duration(p.TimestampNs)
	return codech264.NewPacket(p.IsKeyframe, ts, time.Time{}, data, "test", par)
}

func makeH265Packet(t *testing.T, p packetJSON, par *codech265.CodecParameters) *codech265.Packet {
	t.Helper()
	data := decodeBase64(t, p.Data)
	require.Equal(t, p.Size, len(data))
	ts := time.Duration(p.TimestampNs)
	return codech265.NewPacket(p.IsKeyframe, ts, time.Time{}, data, "test", par)
}

// decodeUntilImage initialises the decoder with par, then feeds video packets
// sequentially until at least one image is produced or the packet list is
// exhausted.  It returns the first decoded image (nil if none were produced).
func decodeUntilImage(
	t *testing.T,
	d interface {
		Init(interface{ Width() uint }) error
	},
) {
	t.Helper()
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

func TestNewFFmpegCPUDecoder_NotNil(t *testing.T) {
	t.Parallel()
	d := cpu.NewFFmpegCPUDecoder()
	require.NotNil(t, d)
}

// ---------------------------------------------------------------------------
// Close
// ---------------------------------------------------------------------------

func TestCPU_Close_WithoutInit(t *testing.T) {
	t.Parallel()
	d := cpu.NewFFmpegCPUDecoder()
	require.NotPanics(t, d.Close)
}

func TestCPU_Close_AfterInit(t *testing.T) {
	t.Parallel()
	par := loadH264Params(t, h264AACDataDir)
	d := cpu.NewFFmpegCPUDecoder()
	require.NoError(t, d.Init(par))
	require.NotPanics(t, d.Close)
}

func TestCPU_Close_Double(t *testing.T) {
	t.Parallel()
	par := loadH264Params(t, h264AACDataDir)
	d := cpu.NewFFmpegCPUDecoder()
	require.NoError(t, d.Init(par))
	d.Close()
	require.NotPanics(t, d.Close)
}

// ---------------------------------------------------------------------------
// Init
// ---------------------------------------------------------------------------

func TestCPU_Init_H264_Valid(t *testing.T) {
	t.Parallel()
	par := loadH264Params(t, h264DataDir)
	d := cpu.NewFFmpegCPUDecoder()
	defer d.Close()
	require.NoError(t, d.Init(par))
}

func TestCPU_Init_H265_Valid(t *testing.T) {
	t.Parallel()
	par := loadH265Params(t)
	d := cpu.NewFFmpegCPUDecoder()
	defer d.Close()
	require.NoError(t, d.Init(par))
}

func TestCPU_Init_UnsupportedCodecType_Error(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	par := mocks.NewMockVideoCodecParameters(ctrl)
	par.EXPECT().Width().Return(uint(640)).AnyTimes()
	par.EXPECT().Height().Return(uint(480)).AnyTimes()

	d := cpu.NewFFmpegCPUDecoder()
	defer d.Close()
	err := d.Init(par)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported codec type")
}

func TestCPU_Init_Reinit_H264(t *testing.T) {
	t.Parallel()
	par := loadH264Params(t, h264DataDir)
	d := cpu.NewFFmpegCPUDecoder()
	defer d.Close()
	require.NoError(t, d.Init(par))
	// Reinitialising must not crash or leak resources.
	require.NoError(t, d.Init(par))
}

func TestCPU_Init_ReinitWithDifferentCodec(t *testing.T) {
	t.Parallel()
	h264Par := loadH264Params(t, h264DataDir)
	h265Par := loadH265Params(t)

	d := cpu.NewFFmpegCPUDecoder()
	defer d.Close()
	require.NoError(t, d.Init(h264Par))
	require.NoError(t, d.Init(h265Par))
}

// ---------------------------------------------------------------------------
// Decode — H264
// ---------------------------------------------------------------------------

// TestCPU_Decode_H264_ProducesImage feeds packets from h264_aac (first video
// packet is an IDR) and asserts that at least one non-nil image is returned.
func TestCPU_Decode_H264_ProducesImage(t *testing.T) {
	t.Parallel()
	par := loadH264Params(t, h264AACDataDir)
	pkts := loadPackets(t, h264AACDataDir)

	d := cpu.NewFFmpegCPUDecoder()
	defer d.Close()
	require.NoError(t, d.Init(par))

	// h264_aac first video packet is an IDR; decode until we get an image.
	const maxFrames = 30
	var got image.Image
	for i, p := range pkts {
		if p.Codec != "H264" {
			continue
		}
		if i >= maxFrames {
			break
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
	require.NotNil(t, got, "expected at least one decoded image from H264 stream")
	require.True(t, got.Bounds().Dx() > 0 && got.Bounds().Dy() > 0,
		"decoded image must have non-zero dimensions")
}

// TestCPU_Decode_H264_ErrNeedMoreData feeds non-IDR packets before any IDR and
// verifies the decoder returns ErrNeedMoreData (not a fatal error).
func TestCPU_Decode_H264_ErrNeedMoreData(t *testing.T) {
	t.Parallel()
	par := loadH264Params(t, h264DataDir)
	pkts := loadPackets(t, h264DataDir)

	d := cpu.NewFFmpegCPUDecoder()
	defer d.Close()
	require.NoError(t, d.Init(par))

	// h264 dataset: first 10 packets are non-IDR; they must return
	// ErrNeedMoreData or nil (never a hard error).
	const maxNonIDR = 10
	for i, p := range pkts {
		if i >= maxNonIDR {
			break
		}
		if p.IsKeyframe {
			break
		}
		pkt := makeH264Packet(t, p, par)
		img, err := d.Decode(pkt)
		if err != nil {
			require.EqualError(t, err, "need more data to decode frame",
				"frame %d: unexpected error", i)
		}
		// img may be nil before any IDR has been decoded
		_ = img
	}
}

// TestCPU_Decode_H264_AllPackets decodes the first N packets (stopping at
// the second keyframe) and verifies no unexpected errors are returned.
func TestCPU_Decode_H264_AllPackets(t *testing.T) {
	t.Parallel()
	par := loadH264Params(t, h264DataDir)
	pkts := loadPackets(t, h264DataDir)

	d := cpu.NewFFmpegCPUDecoder()
	defer d.Close()
	require.NoError(t, d.Init(par))

	keyFramesSeen := 0
	for i, p := range pkts {
		pkt := makeH264Packet(t, p, par)
		_, err := d.Decode(pkt)
		if err != nil {
			require.EqualError(t, err, "need more data to decode frame",
				"frame %d: unexpected error", i)
		}
		if p.IsKeyframe {
			keyFramesSeen++
			if keyFramesSeen >= 2 { //nolint:mnd // stop after second GOP
				break
			}
		}
	}
	require.GreaterOrEqual(t, keyFramesSeen, 1, "expected at least one keyframe")
}

// ---------------------------------------------------------------------------
// Feed — H264
// ---------------------------------------------------------------------------

// TestCPU_Feed_H264_KeyFrame verifies that Feed on an IDR packet does not
// return an error.
func TestCPU_Feed_H264_KeyFrame(t *testing.T) {
	t.Parallel()
	par := loadH264Params(t, h264AACDataDir)
	pkts := loadPackets(t, h264AACDataDir)

	d := cpu.NewFFmpegCPUDecoder()
	defer d.Close()
	require.NoError(t, d.Init(par))

	// Find first H264 keyframe and Feed it.
	for _, p := range pkts {
		if p.Codec != "H264" || !p.IsKeyframe {
			continue
		}
		pkt := makeH264Packet(t, p, par)
		err := d.Feed(pkt)
		require.NoError(t, err)
		break
	}
}

// TestCPU_Feed_H264_AfterDecode feeds packets after a full decode sequence
// to exercise the "throttle" path where the decoder buffers but does not output.
func TestCPU_Feed_H264_AfterDecode(t *testing.T) {
	t.Parallel()
	par := loadH264Params(t, h264AACDataDir)
	pkts := loadPackets(t, h264AACDataDir)

	d := cpu.NewFFmpegCPUDecoder()
	defer d.Close()
	require.NoError(t, d.Init(par))

	// Decode first IDR to populate the reference frame.
	var decoded bool
	for _, p := range pkts {
		if p.Codec != "H264" {
			continue
		}
		pkt := makeH264Packet(t, p, par)
		img, err := d.Decode(pkt)
		if err != nil && err.Error() != "need more data to decode frame" {
			require.NoError(t, err)
		}
		if img != nil {
			decoded = true
			break
		}
	}
	require.True(t, decoded, "expected to produce at least one image before Feed test")

	// Now Feed subsequent packets — none should return an error.
	fed := 0
	for _, p := range pkts {
		if p.Codec != "H264" || p.IsKeyframe {
			continue
		}
		if fed >= 3 { //nolint:mnd // test a few P-frames
			break
		}
		pkt := makeH264Packet(t, p, par)
		err := d.Feed(pkt)
		require.NoError(t, err, "Feed returned unexpected error")
		fed++
	}
}

// ---------------------------------------------------------------------------
// Decode — H265
// ---------------------------------------------------------------------------

// TestCPU_Decode_H265_ProducesImage feeds H265 packets (first is IDR) and
// asserts that at least one non-nil image is returned.
func TestCPU_Decode_H265_ProducesImage(t *testing.T) {
	t.Parallel()
	par := loadH265Params(t)
	pkts := loadPackets(t, h265DataDir)

	d := cpu.NewFFmpegCPUDecoder()
	defer d.Close()
	require.NoError(t, d.Init(par))

	// hevc dataset: first packet is an IDR.
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
	require.NotNil(t, got, "expected at least one decoded image from H265 stream")
	require.True(t, got.Bounds().Dx() > 0 && got.Bounds().Dy() > 0,
		"decoded image must have non-zero dimensions")
}

// TestCPU_Decode_H265_ImageDimensions verifies the decoded image dimensions
// match those advertised by the codec parameters.
func TestCPU_Decode_H265_ImageDimensions(t *testing.T) {
	t.Parallel()
	par := loadH265Params(t)
	pkts := loadPackets(t, h265DataDir)

	d := cpu.NewFFmpegCPUDecoder()
	defer d.Close()
	require.NoError(t, d.Init(par))

	const maxFrames = 5
	for i, p := range pkts {
		if i >= maxFrames {
			break
		}
		pkt := makeH265Packet(t, p, par)
		img, err := d.Decode(pkt)
		if err != nil && err.Error() != "need more data to decode frame" {
			require.NoError(t, err, "frame %d", i)
		}
		if img != nil {
			b := img.Bounds()
			require.Equal(t, int(par.Width()), b.Dx(),
				"image width must match codec parameters")
			require.Equal(t, int(par.Height()), b.Dy(),
				"image height must match codec parameters")
			return
		}
	}
	t.Fatal("no image produced within first maxFrames packets")
}

// ---------------------------------------------------------------------------
// Feed — H265
// ---------------------------------------------------------------------------

func TestCPU_Feed_H265_KeyFrame(t *testing.T) {
	t.Parallel()
	par := loadH265Params(t)
	pkts := loadPackets(t, h265DataDir)

	d := cpu.NewFFmpegCPUDecoder()
	defer d.Close()
	require.NoError(t, d.Init(par))

	// Feed first packet (IDR).
	pkt := makeH265Packet(t, pkts[0], par)
	err := d.Feed(pkt)
	require.NoError(t, err)
}
