//nolint:mnd // Test file uses many literal values for expected results
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

func makeVideoFactory(inner decoder.InnerVideoDecoder) map[gomedia.CodecType]func() decoder.InnerVideoDecoder {
	fn := func() decoder.InnerVideoDecoder { return inner }
	return map[gomedia.CodecType]func() decoder.InnerVideoDecoder{
		gomedia.H264: fn,
		gomedia.H265: fn,
	}
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
// Lifecycle
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

func TestDurationFromFPS_1(t *testing.T) {
	t.Parallel()
	// 1 FPS → 1 second per frame
	d := decoder.DurationFromFPS(1)
	require.Equal(t, 1000*time.Millisecond, d)
}

func TestDurationFromFPS_30(t *testing.T) {
	t.Parallel()
	// 30 FPS → 33.33ms per frame. Integer division gives 33ms.
	// This is a known precision loss in the implementation (1000/30 = 33).
	// The test documents the actual behavior.
	d := decoder.DurationFromFPS(30)
	require.Equal(t, 33*time.Millisecond, d,
		"DurationFromFPS uses integer division: 1000/30 = 33ms (not 33.33ms)")
}

func TestDurationFromFPS_60(t *testing.T) {
	t.Parallel()
	// 60 FPS → 16.66ms. Integer division: 1000/60 = 16ms.
	d := decoder.DurationFromFPS(60)
	require.Equal(t, 16*time.Millisecond, d,
		"DurationFromFPS uses integer division: 1000/60 = 16ms")
}

func TestDurationFromFPS_Negative(t *testing.T) {
	t.Parallel()
	// Negative FPS should return 0 (same as fps <= 0)
	require.Equal(t, time.Duration(0), decoder.DurationFromFPS(-1))
}

// ---------------------------------------------------------------------------
// Options
// ---------------------------------------------------------------------------

func TestVideoWithName(t *testing.T) {
	t.Parallel()
	d := decoder.NewVideo(1, 30, nil, decoder.VideoWithName("my-decoder"))
	require.NotNil(t, d)
	d.Close()
}

// ---------------------------------------------------------------------------
// Step — keyframe produces image
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
		// Image bounds must match what the inner decoder returned
		require.Equal(t, img.Bounds(), got.Bounds())
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for image")
	}

	d.Close()
}

// ---------------------------------------------------------------------------
// Step — non-keyframe before first keyframe is skipped
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
	// Init called twice: once for initial start, once for restart after error
	inner.EXPECT().Init(par).Return(nil).Times(2)
	inner.EXPECT().Decode(gomock.Any()).Return(nil, errors.New("decode error"))
	// Close called twice: once on error, once on restart
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
// Step — unknown codec type: no image, handled gracefully
// ---------------------------------------------------------------------------

func TestStep_Video_UnknownCodecType_NoImage(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

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
// Step — FPS throttle: Feed called instead of Decode within rate window
// ---------------------------------------------------------------------------

func TestStep_Video_FPSThrottle_FeedCalled(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	par := mockVideoCodecParams(ctrl, gomedia.H264)
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))

	inner := mocks.NewMockInnerVideoDecoder(ctrl)
	inner.EXPECT().Init(par).Return(nil)
	// First packet: Decode produces an image
	inner.EXPECT().Decode(gomock.Any()).Return(img, nil)
	// Second packet arrives immediately — within throttle window → Feed is called
	inner.EXPECT().Feed(gomock.Any()).Return(nil)
	inner.EXPECT().Close()

	// fps=1 → frameDuration=1000ms; second packet arrives within 990ms → Feed
	d := decoder.NewVideo(2, 1, makeVideoFactory(inner))
	d.Decode()

	d.Packets() <- mockKeyPkt(ctrl, par)

	select {
	case _, ok := <-d.Images():
		require.True(t, ok)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for first image")
	}

	// Second packet immediately — must be throttled (Feed, not Decode)
	d.Packets() <- mockKeyPkt(ctrl, par)

	select {
	case <-time.After(50 * time.Millisecond):
		// Expected: no image because Feed was called, not Decode
	case img2 := <-d.Images():
		t.Fatalf("unexpected image on throttled frame: %v", img2)
	}

	d.Close()
	require.Empty(t, drainImages(d))
}

// ---------------------------------------------------------------------------
// FPS channel: same value is no-op
// ---------------------------------------------------------------------------

func TestStep_Video_FPS_SameValue_NoOp(t *testing.T) {
	t.Parallel()
	d := decoder.NewVideo(1, 30, nil)
	d.Decode()

	d.FPS() <- 30

	select {
	case <-time.After(50 * time.Millisecond):
	case img := <-d.Images():
		t.Fatalf("unexpected image on no-op FPS change: %v", img)
	}

	d.Close()
	require.Empty(t, drainImages(d))
}

// ---------------------------------------------------------------------------
// FPS=0 stops decoder
// ---------------------------------------------------------------------------

func TestStep_Video_FPS_ZeroStopsDecoder(t *testing.T) {
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
	case _, ok := <-d.Images():
		require.True(t, ok)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for image")
	}

	// FPS=0 must stop the inner decoder
	d.FPS() <- 0
	time.Sleep(50 * time.Millisecond)

	d.Close()
}

// ---------------------------------------------------------------------------
// Multiple keyframes: all produce images (when within FPS budget)
// ---------------------------------------------------------------------------

func TestStep_Video_MultipleKeyframes(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	par := mockVideoCodecParams(ctrl, gomedia.H264)
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))

	inner := mocks.NewMockInnerVideoDecoder(ctrl)
	inner.EXPECT().Init(par).Return(nil)
	// All packets decoded (fps=0 means no limit... wait, fps=0 disables.
	// Use high FPS so throttle window is tiny)
	inner.EXPECT().Decode(gomock.Any()).Return(img, nil).Times(3)
	inner.EXPECT().Close()

	// fps=1000 → frameDuration=1ms, so no throttling
	d := decoder.NewVideo(3, 1000, makeVideoFactory(inner))
	d.Decode()

	for range 3 {
		d.Packets() <- mockKeyPkt(ctrl, par)
		// Small delay to ensure lastFrameTime advances past frameDuration
		time.Sleep(2 * time.Millisecond)
	}

	received := 0
	for range 3 {
		select {
		case <-d.Images():
			received++
		case <-time.After(time.Second):
			t.Fatalf("timeout waiting for image %d", received+1)
		}
	}
	require.Equal(t, 3, received)

	d.Close()
}
