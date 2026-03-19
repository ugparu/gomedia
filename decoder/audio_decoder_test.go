//nolint:mnd // Test file uses many literal values for expected results
package decoder_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/ugparu/gomedia"
	decoder "github.com/ugparu/gomedia/decoder"
	decPcm "github.com/ugparu/gomedia/decoder/pcm"
	"github.com/ugparu/gomedia/mocks"
	"github.com/ugparu/gomedia/utils/buffer"
)

const (
	alawDataPath  = "../tests/data/alaw/packets.json"
	mulawDataPath = "../tests/data/mulaw/packets.json"
)

type fixturePacketsJSON struct {
	Packets []struct {
		Size int    `json:"size"`
		Data string `json:"data"`
	} `json:"packets"`
}

func loadFixtureFrames(t *testing.T, path string) [][]byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var f fixturePacketsJSON
	require.NoError(t, json.Unmarshal(raw, &f))
	require.NotEmpty(t, f.Packets)
	frames := make([][]byte, len(f.Packets))
	for i, p := range f.Packets {
		data, decErr := base64.StdEncoding.DecodeString(p.Data)
		require.NoError(t, decErr, "packet %d base64 decode", i)
		require.Equal(t, p.Size, len(data), "packet %d size mismatch", i)
		frames[i] = data
	}
	return frames
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func makeFactory(codecType gomedia.CodecType, inner decoder.InnerAudioDecoder) map[gomedia.CodecType]func() decoder.InnerAudioDecoder {
	return map[gomedia.CodecType]func() decoder.InnerAudioDecoder{
		codecType: func() decoder.InnerAudioDecoder { return inner },
	}
}

func mockCodecParams(ctrl *gomock.Controller, ct gomedia.CodecType) *mocks.MockAudioCodecParameters {
	par := mocks.NewMockAudioCodecParameters(ctrl)
	par.EXPECT().Type().Return(ct).AnyTimes()
	par.EXPECT().StreamIndex().Return(uint8(0)).AnyTimes()
	par.EXPECT().Channels().Return(uint8(1)).AnyTimes()
	par.EXPECT().SampleRate().Return(uint64(16000)).AnyTimes()
	return par
}

func mockPacket(ctrl *gomock.Controller, par gomedia.AudioCodecParameters, data []byte) *mocks.MockAudioPacket {
	return mockPacketFull(ctrl, par, data, time.Duration(0), "src", time.Time{}, 20*time.Millisecond)
}

func mockPacketFull(
	ctrl *gomock.Controller,
	par gomedia.AudioCodecParameters,
	data []byte,
	ts time.Duration,
	sourceID string,
	startTime time.Time,
	dur time.Duration,
) *mocks.MockAudioPacket {
	pkt := mocks.NewMockAudioPacket(ctrl)
	pkt.EXPECT().CodecParameters().Return(par).AnyTimes()
	pkt.EXPECT().Data().Return(data).AnyTimes()
	pkt.EXPECT().Len().Return(len(data)).AnyTimes()
	pkt.EXPECT().Timestamp().Return(ts).AnyTimes()
	pkt.EXPECT().SourceID().Return(sourceID).AnyTimes()
	pkt.EXPECT().StartTime().Return(startTime).AnyTimes()
	pkt.EXPECT().Duration().Return(dur).AnyTimes()
	pkt.EXPECT().Release().AnyTimes()
	return pkt
}

func drainSamples(d gomedia.AudioDecoder) []gomedia.AudioPacket {
	var out []gomedia.AudioPacket
	for pkt := range d.Samples() {
		out = append(out, pkt)
	}
	return out
}

func lawFactory(ct gomedia.CodecType, newInner func() decoder.InnerAudioDecoder) map[gomedia.CodecType]func() decoder.InnerAudioDecoder {
	return map[gomedia.CodecType]func() decoder.InnerAudioDecoder{
		ct: newInner,
	}
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

func TestNewAudioDecoder_NotNil(t *testing.T) {
	t.Parallel()
	d := decoder.NewAudioDecoder(1, nil)
	require.NotNil(t, d)
}

func TestNewAudioDecoder_ChannelsNotNil(t *testing.T) {
	t.Parallel()
	d := decoder.NewAudioDecoder(1, nil)
	require.NotNil(t, d.Packets())
	require.NotNil(t, d.Samples())
	require.NotNil(t, d.Done())
}

// ---------------------------------------------------------------------------
// Lifecycle: Close without Decode
// ---------------------------------------------------------------------------

func TestClose_WithoutDecode(t *testing.T) {
	t.Parallel()
	d := decoder.NewAudioDecoder(1, nil)
	require.NotPanics(t, d.Close)
}

func TestClose_WithoutDecode_SamplesClosedAfterClose(t *testing.T) {
	t.Parallel()
	d := decoder.NewAudioDecoder(1, nil)
	d.Close()
	require.Empty(t, drainSamples(d))
}

// ---------------------------------------------------------------------------
// Lifecycle: Close after Decode
// ---------------------------------------------------------------------------

func TestClose_AfterDecode(t *testing.T) {
	t.Parallel()
	d := decoder.NewAudioDecoder(1, nil)
	d.Decode()
	require.NotPanics(t, d.Close)
}

func TestClose_Double(t *testing.T) {
	t.Parallel()
	d := decoder.NewAudioDecoder(1, nil)
	d.Decode()
	d.Close()
	require.NotPanics(t, d.Close)
}

func TestDone_ClosedAfterClose(t *testing.T) {
	t.Parallel()
	d := decoder.NewAudioDecoder(1, nil)
	d.Decode()
	d.Close()
	select {
	case <-d.Done():
	case <-time.After(time.Second):
		t.Fatal("Done() not closed after Close()")
	}
}

// ---------------------------------------------------------------------------
// Step — happy path: produce PCM
// ---------------------------------------------------------------------------

func TestStep_ProducePCM(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	ct := gomedia.AAC
	inner := mocks.NewMockInnerAudioDecoder(ctrl)
	par := mockCodecParams(ctrl, ct)
	pkt := mockPacket(ctrl, par, []byte{0x01, 0x02})
	pcmData := []byte{0xAA, 0xBB, 0xCC, 0xDD}

	inner.EXPECT().Init(par).Return(nil)
	inner.EXPECT().Decode(gomock.Any(), gomock.Nil()).Return(pcmData, nil, nil)
	inner.EXPECT().Close()

	d := decoder.NewAudioDecoder(1, makeFactory(ct, inner))
	d.Decode()

	d.Packets() <- pkt

	select {
	case out, ok := <-d.Samples():
		require.True(t, ok)
		require.NotNil(t, out)
		require.Equal(t, len(pcmData), out.Len())
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for PCM packet")
	}

	d.Close()
}

// ---------------------------------------------------------------------------
// Timestamp, SourceID, Duration propagation
// ---------------------------------------------------------------------------

func TestStep_PropagatesTimestamp(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	ct := gomedia.AAC
	inner := mocks.NewMockInnerAudioDecoder(ctrl)
	par := mockCodecParams(ctrl, ct)
	pcmData := []byte{0xAA, 0xBB}

	expectedTS := 500 * time.Millisecond
	expectedSourceID := "rtsp://camera/stream"
	expectedStartTime := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	expectedDur := 64 * time.Millisecond

	pkt := mockPacketFull(ctrl, par, []byte{0x01}, expectedTS, expectedSourceID, expectedStartTime, expectedDur)

	inner.EXPECT().Init(par).Return(nil)
	inner.EXPECT().Decode(gomock.Any(), gomock.Nil()).Return(pcmData, nil, nil)
	inner.EXPECT().Close()

	d := decoder.NewAudioDecoder(1, makeFactory(ct, inner))
	d.Decode()

	d.Packets() <- pkt

	select {
	case out, ok := <-d.Samples():
		require.True(t, ok)
		require.Equal(t, expectedTS, out.Timestamp(),
			"output timestamp must match input")
		require.Equal(t, expectedSourceID, out.SourceID(),
			"output sourceID must match input")
		require.Equal(t, expectedStartTime, out.StartTime(),
			"output startTime must match input")
		require.Equal(t, expectedDur, out.Duration(),
			"output duration must match input")
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for PCM packet")
	}

	d.Close()
}

// ---------------------------------------------------------------------------
// Output PCM codec parameters
// ---------------------------------------------------------------------------

func TestStep_OutputHasPCMCodecType(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	ct := gomedia.AAC
	inner := mocks.NewMockInnerAudioDecoder(ctrl)
	par := mockCodecParams(ctrl, ct)
	pkt := mockPacket(ctrl, par, []byte{0x01})
	pcmData := []byte{0xAA, 0xBB}

	inner.EXPECT().Init(par).Return(nil)
	inner.EXPECT().Decode(gomock.Any(), gomock.Nil()).Return(pcmData, nil, nil)
	inner.EXPECT().Close()

	d := decoder.NewAudioDecoder(1, makeFactory(ct, inner))
	d.Decode()

	d.Packets() <- pkt

	select {
	case out, ok := <-d.Samples():
		require.True(t, ok)
		outPar := out.CodecParameters()
		require.Equal(t, gomedia.PCM, outPar.Type(),
			"output codec type must be PCM")
		require.Equal(t, uint8(0), outPar.StreamIndex(),
			"output stream index must match input")
		require.Equal(t, uint64(16000), outPar.SampleRate(),
			"output sample rate must match input")
		require.Equal(t, uint8(1), outPar.Channels(),
			"output channels must match input")
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}

	d.Close()
}

// ---------------------------------------------------------------------------
// Unsupported codec
// ---------------------------------------------------------------------------

func TestStep_UnsupportedCodec(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	par := mockCodecParams(ctrl, gomedia.AAC)
	pkt := mockPacket(ctrl, par, []byte{0x01})

	d := decoder.NewAudioDecoder(1, map[gomedia.CodecType]func() decoder.InnerAudioDecoder{})
	d.Decode()

	d.Packets() <- pkt

	// Must not produce output for unsupported codec
	select {
	case <-time.After(50 * time.Millisecond):
	case out := <-d.Samples():
		t.Fatalf("unexpected PCM packet for unsupported codec: %v", out)
	}

	d.Close()
	require.Empty(t, drainSamples(d))
}

// ---------------------------------------------------------------------------
// Empty decode result
// ---------------------------------------------------------------------------

func TestStep_EmptyDecode(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	ct := gomedia.AAC
	inner := mocks.NewMockInnerAudioDecoder(ctrl)
	par := mockCodecParams(ctrl, ct)
	pkt := mockPacket(ctrl, par, []byte{0x01})

	inner.EXPECT().Init(par).Return(nil)
	inner.EXPECT().Decode(gomock.Any(), gomock.Nil()).Return(nil, nil, nil)
	inner.EXPECT().Close()

	d := decoder.NewAudioDecoder(1, makeFactory(ct, inner))
	d.Decode()

	d.Packets() <- pkt

	select {
	case <-time.After(50 * time.Millisecond):
	case out := <-d.Samples():
		t.Fatalf("unexpected PCM packet on empty decode: %v", out)
	}

	d.Close()
	require.Empty(t, drainSamples(d))
}

// ---------------------------------------------------------------------------
// Decode error
// ---------------------------------------------------------------------------

func TestStep_DecodeError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	ct := gomedia.AAC
	inner := mocks.NewMockInnerAudioDecoder(ctrl)
	par := mockCodecParams(ctrl, ct)
	pkt := mockPacket(ctrl, par, []byte{0x01})

	inner.EXPECT().Init(par).Return(nil)
	inner.EXPECT().Decode(gomock.Any(), gomock.Nil()).Return(nil, nil, errors.New("decode failed"))
	inner.EXPECT().Close()

	d := decoder.NewAudioDecoder(1, makeFactory(ct, inner))
	d.Decode()

	d.Packets() <- pkt

	select {
	case <-time.After(50 * time.Millisecond):
	case out := <-d.Samples():
		t.Fatalf("unexpected PCM packet on decode error: %v", out)
	}

	d.Close()
	require.Empty(t, drainSamples(d))
}

// ---------------------------------------------------------------------------
// Init error
// ---------------------------------------------------------------------------

func TestStep_InitError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	ct := gomedia.AAC
	inner := mocks.NewMockInnerAudioDecoder(ctrl)
	par := mockCodecParams(ctrl, ct)
	pkt := mockPacket(ctrl, par, []byte{0x01})

	inner.EXPECT().Init(par).Return(errors.New("init failed")).AnyTimes()
	inner.EXPECT().Close().AnyTimes()

	d := decoder.NewAudioDecoder(1, makeFactory(ct, inner))
	d.Decode()

	d.Packets() <- pkt
	d.Close()

	require.Empty(t, drainSamples(d))
}

// ---------------------------------------------------------------------------
// Ring buffer propagation
// ---------------------------------------------------------------------------

func TestAudioWithRingBuffer_PropagatedToDecode(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	ct := gomedia.AAC
	inner := mocks.NewMockInnerAudioDecoder(ctrl)
	par := mockCodecParams(ctrl, ct)
	pkt := mockPacket(ctrl, par, []byte{0x01, 0x02})
	pcmData := []byte{0xAA, 0xBB}

	ring := buffer.NewGrowingRingAlloc(64 * 1024)

	inner.EXPECT().Init(par).Return(nil)
	inner.EXPECT().Decode(gomock.Any(), ring).Return(pcmData, nil, nil)
	inner.EXPECT().Close()

	d := decoder.NewAudioDecoder(1, makeFactory(ct, inner), decoder.AudioWithRingBuffer(ring))
	d.Decode()

	d.Packets() <- pkt

	select {
	case out, ok := <-d.Samples():
		require.True(t, ok)
		require.Equal(t, len(pcmData), out.Len())
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for PCM packet via ring")
	}

	d.Close()
}

// ---------------------------------------------------------------------------
// Options smoke test
// ---------------------------------------------------------------------------

func TestAudioWithName(t *testing.T) {
	t.Parallel()
	d := decoder.NewAudioDecoder(1, nil, decoder.AudioWithName("my-decoder"))
	require.NotNil(t, d)
	d.Close()
}

// ---------------------------------------------------------------------------
// Multiple packets: Init called once, Decode called N times
// ---------------------------------------------------------------------------

func TestStep_MultiplePackets_SameCodec(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	ct := gomedia.AAC
	inner := mocks.NewMockInnerAudioDecoder(ctrl)
	par := mockCodecParams(ctrl, ct)

	pcmData := []byte{0xAA, 0xBB}
	const n = 3

	inner.EXPECT().Init(par).Return(nil).Times(1)
	inner.EXPECT().Decode(gomock.Any(), gomock.Nil()).Return(pcmData, nil, nil).Times(n)
	inner.EXPECT().Close()

	d := decoder.NewAudioDecoder(n, makeFactory(ct, inner))
	d.Decode()

	for range n {
		pkt := mockPacket(ctrl, par, []byte{0x01, 0x02})
		d.Packets() <- pkt
	}

	received := 0
	for range n {
		select {
		case <-d.Samples():
			received++
		case <-time.After(time.Second):
			t.Fatalf("timeout waiting for packet %d", received+1)
		}
	}
	require.Equal(t, n, received)

	d.Close()
}

// ---------------------------------------------------------------------------
// Codec switch: new codec params → new inner decoder created
// ---------------------------------------------------------------------------

func TestStep_CodecSwitch_ReinitInner(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	ct := gomedia.AAC
	inner1 := mocks.NewMockInnerAudioDecoder(ctrl)
	inner2 := mocks.NewMockInnerAudioDecoder(ctrl)
	par1 := mockCodecParams(ctrl, ct)
	par2 := mockCodecParams(ctrl, ct) // different pointer → triggers codec switch

	pcmData := []byte{0xAA, 0xBB}
	callCount := 0

	factory := map[gomedia.CodecType]func() decoder.InnerAudioDecoder{
		ct: func() decoder.InnerAudioDecoder {
			callCount++
			if callCount == 1 {
				return inner1
			}
			return inner2
		},
	}

	inner1.EXPECT().Init(par1).Return(nil)
	inner1.EXPECT().Decode(gomock.Any(), gomock.Nil()).Return(pcmData, nil, nil)
	inner1.EXPECT().Close().AnyTimes()

	inner2.EXPECT().Init(par2).Return(nil)
	inner2.EXPECT().Decode(gomock.Any(), gomock.Nil()).Return(pcmData, nil, nil)
	inner2.EXPECT().Close().AnyTimes()

	d := decoder.NewAudioDecoder(2, factory)
	d.Decode()

	d.Packets() <- mockPacket(ctrl, par1, []byte{0x01})
	select {
	case <-d.Samples():
	case <-time.After(time.Second):
		t.Fatal("timeout on first packet")
	}

	d.Packets() <- mockPacket(ctrl, par2, []byte{0x02})
	select {
	case <-d.Samples():
	case <-time.After(time.Second):
		t.Fatal("timeout on second packet (codec switch)")
	}

	require.Equal(t, 2, callCount, "factory must be called twice for codec switch")

	d.Close()
}

// ---------------------------------------------------------------------------
// Integration tests — real data, real inner decoders (no CGO)
// ---------------------------------------------------------------------------

func runLawIntegration(t *testing.T, ct gomedia.CodecType, newInner func() decoder.InnerAudioDecoder, dataPath string) {
	t.Helper()
	ctrl := gomock.NewController(t)

	frames := loadFixtureFrames(t, dataPath)

	par := mocks.NewMockAudioCodecParameters(ctrl)
	par.EXPECT().Type().Return(ct).AnyTimes()
	par.EXPECT().StreamIndex().Return(uint8(1)).AnyTimes()
	par.EXPECT().Channels().Return(uint8(1)).AnyTimes()
	par.EXPECT().SampleRate().Return(uint64(8000)).AnyTimes()

	d := decoder.NewAudioDecoder(len(frames), lawFactory(ct, newInner))
	d.Decode()

	for i, frame := range frames {
		pkt := mockPacketFull(ctrl, par, frame,
			time.Duration(i)*128*time.Millisecond, "src",
			time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			128*time.Millisecond,
		)
		select {
		case d.Packets() <- pkt:
		case <-time.After(time.Second):
			t.Fatalf("timeout sending frame %d", i)
		}
	}

	for i, frame := range frames {
		select {
		case out, ok := <-d.Samples():
			require.True(t, ok, "frame %d: samples channel closed early", i)
			// A-law/Mu-law: 8-bit input → 16-bit PCM output
			require.Equal(t, 2*len(frame), out.Len(),
				"frame %d: PCM size must be 2× input (8-bit law → 16-bit PCM)", i)
			// Output must be PCM codec type
			require.Equal(t, gomedia.PCM, out.CodecParameters().Type(),
				"frame %d: output must be PCM", i)
			// Timestamp must be propagated from input
			require.Equal(t, time.Duration(i)*128*time.Millisecond, out.Timestamp(),
				"frame %d: timestamp must be propagated", i)
		case <-time.After(time.Second):
			t.Fatalf("timeout receiving PCM for frame %d", i)
		}
	}

	d.Close()
}

func TestIntegration_ALAW_Pipeline(t *testing.T) {
	t.Parallel()
	runLawIntegration(t, gomedia.PCMAlaw, decPcm.NewALAWDecoder, alawDataPath)
}

func TestIntegration_MULAW_Pipeline(t *testing.T) {
	t.Parallel()
	runLawIntegration(t, gomedia.PCMUlaw, decPcm.NewULAWDecoder, mulawDataPath)
}

func TestIntegration_ALAW_WithRingBuffer(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	frames := loadFixtureFrames(t, alawDataPath)
	frame := frames[0]

	par := mocks.NewMockAudioCodecParameters(ctrl)
	par.EXPECT().Type().Return(gomedia.PCMAlaw).AnyTimes()
	par.EXPECT().StreamIndex().Return(uint8(1)).AnyTimes()
	par.EXPECT().Channels().Return(uint8(1)).AnyTimes()
	par.EXPECT().SampleRate().Return(uint64(8000)).AnyTimes()

	pkt := mockPacket(ctrl, par, frame)

	ring := buffer.NewGrowingRingAlloc(256 * 1024)
	d := decoder.NewAudioDecoder(1, lawFactory(gomedia.PCMAlaw, decPcm.NewALAWDecoder),
		decoder.AudioWithRingBuffer(ring))
	d.Decode()

	d.Packets() <- pkt

	select {
	case out, ok := <-d.Samples():
		require.True(t, ok)
		require.Equal(t, 2*len(frame), out.Len(),
			"PCM size must be 2× input when decoded via ring")
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for PCM packet")
	}

	d.Close()
}
