package decoder_test

import (
	"errors"
	"image"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/ugparu/gomedia"
	decoder "github.com/ugparu/gomedia/decoder"
	"github.com/ugparu/gomedia/mocks"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func makeVideoFactory(inner decoder.InnerVideoDecoder) func() decoder.InnerVideoDecoder {
	return func() decoder.InnerVideoDecoder { return inner }
}

func mockVideoCodecParams(ctrl *gomock.Controller, ct gomedia.CodecType) *mocks.MockVideoCodecParameters {
	par := mocks.NewMockVideoCodecParameters(ctrl)
	par.EXPECT().Type().Return(ct).AnyTimes()
	return par
}

func mockKeyPkt(ctrl *gomock.Controller, par gomedia.VideoCodecParameters) *mocks.MockVideoPacket {
	pkt := mocks.NewMockVideoPacket(ctrl)
	pkt.EXPECT().CodecParameters().Return(par).AnyTimes()
	pkt.EXPECT().IsKeyFrame().Return(true).AnyTimes()
	pkt.EXPECT().Release().AnyTimes()
	return pkt
}

func mockNonKeyPkt(ctrl *gomock.Controller, par gomedia.VideoCodecParameters) *mocks.MockVideoPacket {
	pkt := mocks.NewMockVideoPacket(ctrl)
	pkt.EXPECT().CodecParameters().Return(par).AnyTimes()
	pkt.EXPECT().IsKeyFrame().Return(false).AnyTimes()
	pkt.EXPECT().Release().AnyTimes()
	return pkt
}

func drainImages(d gomedia.VideoDecoder) []image.Image {
	var out []image.Image
	for img := range d.Images() {
		out = append(out, img)
	}
	return out
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

func TestNewVideo_NotNil(t *testing.T) {
	t.Parallel()
	d := decoder.NewVideo(1, 30, nil)
	require.NotNil(t, d)
}

func TestNewVideo_ChannelsNotNil(t *testing.T) {
	t.Parallel()
	d := decoder.NewVideo(1, 30, nil)
	require.NotNil(t, d.Packets())
	require.NotNil(t, d.Images())
	require.NotNil(t, d.FPS())
	require.NotNil(t, d.Done())
}

// ---------------------------------------------------------------------------
// Close without Decode (start never called)
// ---------------------------------------------------------------------------

func TestVideoClose_WithoutDecode(t *testing.T) {
	t.Parallel()
	d := decoder.NewVideo(1, 30, nil)
	require.NotPanics(t, d.Close)
}

func TestVideoClose_WithoutDecode_ImagesClosedAfterClose(t *testing.T) {
	t.Parallel()
	d := decoder.NewVideo(1, 30, nil)
	d.Close()
	require.Empty(t, drainImages(d))
}

// ---------------------------------------------------------------------------
// Close after Decode (started but no packets)
// ---------------------------------------------------------------------------

func TestVideoClose_AfterDecode(t *testing.T) {
	t.Parallel()
	d := decoder.NewVideo(1, 30, nil)
	d.Decode()
	require.NotPanics(t, d.Close)
}

func TestVideoClose_Double(t *testing.T) {
	t.Parallel()
	d := decoder.NewVideo(1, 30, nil)
	d.Decode()
	d.Close()
	require.NotPanics(t, d.Close)
}

func TestVideoDone_ClosedAfterClose(t *testing.T) {
	t.Parallel()
	d := decoder.NewVideo(1, 30, nil)
	d.Decode()
	d.Close()
	select {
	case <-d.Done():
	case <-time.After(time.Second):
		t.Fatal("Done() not closed after Close()")
	}
}

// ---------------------------------------------------------------------------
// DurationFromFPS
// ---------------------------------------------------------------------------

func TestDurationFromFPS_Zero(t *testing.T) {
	t.Parallel()
	require.Equal(t, time.Duration(0), decoder.DurationFromFPS(0))
}

func TestDurationFromFPS_30(t *testing.T) {
	t.Parallel()
	require.Equal(t, 33*time.Millisecond, decoder.DurationFromFPS(30))
}

func TestDurationFromFPS_1(t *testing.T) {
	t.Parallel()
	require.Equal(t, 1000*time.Millisecond, decoder.DurationFromFPS(1))
}

// ---------------------------------------------------------------------------
// VideoWithName option
// ---------------------------------------------------------------------------

func TestVideoWithName(t *testing.T) {
	t.Parallel()
	d := decoder.NewVideo(1, 30, nil, decoder.VideoWithName("my-decoder"))
	require.NotNil(t, d)
	d.Close()
}

// ---------------------------------------------------------------------------
// Step — key frame produces image
// ---------------------------------------------------------------------------

func TestStep_Video_KeyFrame_ProducesImage(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	par := mockVideoCodecParams(ctrl, gomedia.H264)
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))

	inner := mocks.NewMockInnerVideoDecoder(ctrl)
	inner.EXPECT().Init(par).Return(nil)
	inner.EXPECT().Decode(gomock.Any()).Return(img, nil)
	inner.EXPECT().Close()

	d := decoder.NewVideo(1, 30, makeVideoFactory(inner))
	d.Decode()

	d.Packets() <- mockKeyPkt(ctrl, par)

	select {
	case got, ok := <-d.Images():
		require.True(t, ok)
		require.NotNil(t, got)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for image")
	}

	d.Close()
}

// ---------------------------------------------------------------------------
// Step — non-key frame before first key frame is skipped
// ---------------------------------------------------------------------------

func TestStep_Video_NonKeyFrame_BeforeKey_Skipped(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	par := mockVideoCodecParams(ctrl, gomedia.H264)

	inner := mocks.NewMockInnerVideoDecoder(ctrl)
	inner.EXPECT().Init(par).Return(nil)
	inner.EXPECT().Close()

	d := decoder.NewVideo(1, 30, makeVideoFactory(inner))
	d.Decode()

	d.Packets() <- mockNonKeyPkt(ctrl, par)

	select {
	case <-time.After(50 * time.Millisecond):
	case img := <-d.Images():
		t.Fatalf("unexpected image from non-key frame: %v", img)
	}

	d.Close()
	require.Empty(t, drainImages(d))
}

// ---------------------------------------------------------------------------
// Step — ErrNeedMoreData: no image, error is suppressed
// ---------------------------------------------------------------------------

func TestStep_Video_ErrNeedMoreData_NoImage(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	par := mockVideoCodecParams(ctrl, gomedia.H264)

	inner := mocks.NewMockInnerVideoDecoder(ctrl)
	inner.EXPECT().Init(par).Return(nil)
	inner.EXPECT().Decode(gomock.Any()).Return(nil, decoder.ErrNeedMoreData)
	inner.EXPECT().Close()

	d := decoder.NewVideo(1, 30, makeVideoFactory(inner))
	d.Decode()

	d.Packets() <- mockKeyPkt(ctrl, par)

	select {
	case <-time.After(50 * time.Millisecond):
	case img := <-d.Images():
		t.Fatalf("unexpected image on ErrNeedMoreData: %v", img)
	}

	d.Close()
	require.Empty(t, drainImages(d))
}

// ---------------------------------------------------------------------------
// Step — decode error: inner decoder is restarted (Init called twice)
// ---------------------------------------------------------------------------

func TestStep_Video_DecodeError_DecoderRestarted(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	par := mockVideoCodecParams(ctrl, gomedia.H264)

	inner := mocks.NewMockInnerVideoDecoder(ctrl)
	inner.EXPECT().Init(par).Return(nil).Times(2)
	inner.EXPECT().Decode(gomock.Any()).Return(nil, errors.New("decode error"))
	inner.EXPECT().Close().Times(2)

	d := decoder.NewVideo(1, 30, makeVideoFactory(inner))
	d.Decode()

	d.Packets() <- mockKeyPkt(ctrl, par)

	select {
	case <-time.After(50 * time.Millisecond):
	case img := <-d.Images():
		t.Fatalf("unexpected image on decode error: %v", img)
	}

	d.Close()
	require.Empty(t, drainImages(d))
}

// ---------------------------------------------------------------------------
// Step — Init error: no image forwarded
// ---------------------------------------------------------------------------

func TestStep_Video_InitError_NoImage(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	par := mockVideoCodecParams(ctrl, gomedia.H264)

	inner := mocks.NewMockInnerVideoDecoder(ctrl)
	inner.EXPECT().Init(par).Return(errors.New("init failed")).AnyTimes()
	inner.EXPECT().Close().AnyTimes()

	d := decoder.NewVideo(1, 30, makeVideoFactory(inner))
	d.Decode()

	d.Packets() <- mockKeyPkt(ctrl, par)
	d.Close()

	require.Empty(t, drainImages(d))
}

// ---------------------------------------------------------------------------
// Step — unknown codec type: no image, error handled gracefully
// ---------------------------------------------------------------------------

func TestStep_Video_UnknownCodecType_NoImage(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	// CodecType(0) has no String() case → returns "UNKNOWN"
	par := mocks.NewMockVideoCodecParameters(ctrl)
	par.EXPECT().Type().Return(gomedia.CodecType(0)).AnyTimes()

	pkt := mocks.NewMockVideoPacket(ctrl)
	pkt.EXPECT().CodecParameters().Return(par).AnyTimes()
	pkt.EXPECT().IsKeyFrame().Return(true).AnyTimes()
	pkt.EXPECT().Release().AnyTimes()

	d := decoder.NewVideo(1, 30, nil)
	d.Decode()

	d.Packets() <- pkt
	d.Close()

	require.Empty(t, drainImages(d))
}

// ---------------------------------------------------------------------------
// Step — FPS throttle: Feed is called when frame arrives within rate window
// ---------------------------------------------------------------------------

func TestStep_Video_FPSThrottle_FeedCalled(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	par := mockVideoCodecParams(ctrl, gomedia.H264)
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))

	inner := mocks.NewMockInnerVideoDecoder(ctrl)
	inner.EXPECT().Init(par).Return(nil)
	// First packet decoded normally (lastFrameTime is zero)
	inner.EXPECT().Decode(gomock.Any()).Return(img, nil)
	// Second packet arrives immediately — within the 990ms window for fps=1
	inner.EXPECT().Feed(gomock.Any()).Return(nil)
	inner.EXPECT().Close()

	// fps=1 → frameDuration=1000ms; second packet within 990ms triggers Feed
	d := decoder.NewVideo(2, 1, makeVideoFactory(inner))
	d.Decode()

	d.Packets() <- mockKeyPkt(ctrl, par)

	select {
	case _, ok := <-d.Images():
		require.True(t, ok)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for first image")
	}

	// Send second packet immediately — must be throttled
	d.Packets() <- mockKeyPkt(ctrl, par)

	select {
	case <-time.After(50 * time.Millisecond):
	case img2 := <-d.Images():
		t.Fatalf("unexpected image on throttled frame: %v", img2)
	}

	d.Close()
	require.Empty(t, drainImages(d))
}

// ---------------------------------------------------------------------------
// Step — FPS channel: same value sent is a no-op
// ---------------------------------------------------------------------------

func TestStep_Video_FPS_SameValue_NoOp(t *testing.T) {
	t.Parallel()
	d := decoder.NewVideo(1, 30, nil)
	d.Decode()

	// Sending the same FPS value should not trigger any inner decoder calls.
	d.FPS() <- 30

	// Give the goroutine time to process the fps message.
	select {
	case <-time.After(50 * time.Millisecond):
	case img := <-d.Images():
		t.Fatalf("unexpected image on no-op FPS change: %v", img)
	}

	d.Close()
	require.Empty(t, drainImages(d))
}

// ---------------------------------------------------------------------------
// Step — FPS change to zero: stops the running inner decoder
// ---------------------------------------------------------------------------

func TestStep_Video_FPS_ZeroStopsDecoder(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	par := mockVideoCodecParams(ctrl, gomedia.H264)
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))

	inner := mocks.NewMockInnerVideoDecoder(ctrl)
	inner.EXPECT().Init(par).Return(nil)
	inner.EXPECT().Decode(gomock.Any()).Return(img, nil)
	// Close is called once when fps=0 triggers stopDecoder.
	// Release calls stopDecoder again but InnerVideoDecoder is nil by then.
	inner.EXPECT().Close()

	d := decoder.NewVideo(1, 30, makeVideoFactory(inner))
	d.Decode()

	// Start the inner decoder by sending a key frame.
	d.Packets() <- mockKeyPkt(ctrl, par)
	select {
	case _, ok := <-d.Images():
		require.True(t, ok)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for image")
	}

	// Send fps=0 — must stop the inner decoder.
	d.FPS() <- 0

	// Give the goroutine time to process the fps change.
	time.Sleep(50 * time.Millisecond)

	d.Close()
}
