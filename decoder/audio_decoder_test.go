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
	pkt := mocks.NewMockAudioPacket(ctrl)
	pkt.EXPECT().CodecParameters().Return(par).AnyTimes()
	pkt.EXPECT().Data().Return(data).AnyTimes()
	pkt.EXPECT().Len().Return(len(data)).AnyTimes()
	pkt.EXPECT().Timestamp().Return(time.Duration(0)).AnyTimes()
	pkt.EXPECT().SourceID().Return("src").AnyTimes()
	pkt.EXPECT().StartTime().Return(time.Time{}).AnyTimes()
	pkt.EXPECT().Duration().Return(20 * time.Millisecond).AnyTimes()
	pkt.EXPECT().Release().AnyTimes()
	return pkt
}

// drainSamples collects all packets emitted on the samples channel after it is
// closed (i.e. after Close returns).  Returns the collected slice.
func drainSamples(d gomedia.AudioDecoder) []gomedia.AudioPacket {
	var out []gomedia.AudioPacket
	for pkt := range d.Samples() {
		out = append(out, pkt)
	}
	return out
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
// Close without Decode (start never called)
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
	// Samples() channel must be closed; range should exit immediately.
	require.Empty(t, drainSamples(d))
}

// ---------------------------------------------------------------------------
// Close after Decode (started but no packets)
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
// Step — unsupported codec: no PCM output, no crash
// ---------------------------------------------------------------------------

func TestStep_UnsupportedCodec(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	par := mockCodecParams(ctrl, gomedia.AAC)
	pkt := mockPacket(ctrl, par, []byte{0x01})

	// Empty factory — no codec is supported.
	d := decoder.NewAudioDecoder(1, map[gomedia.CodecType]func() decoder.InnerAudioDecoder{})
	d.Decode()

	d.Packets() <- pkt
	d.Close()

	require.Empty(t, drainSamples(d))
}

// ---------------------------------------------------------------------------
// Step — inner decoder returns empty PCM: no PCM packet forwarded
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

	// Give the goroutine time to process the packet before closing.
	select {
	case <-time.After(50 * time.Millisecond):
	case out := <-d.Samples():
		t.Fatalf("unexpected PCM packet: %v", out)
	}

	d.Close()
	require.Empty(t, drainSamples(d))
}

// ---------------------------------------------------------------------------
// Step — inner decoder returns error: no PCM packet forwarded
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
// Step — Init error: no PCM packet forwarded
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
// Ring buffer option: propagated to inner decoder Decode call
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
	// Verify the ring is forwarded: Decode must be called with our ring instance.
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
// AudioWithName option: accepted without panic (smoke test)
// ---------------------------------------------------------------------------

func TestAudioWithName(t *testing.T) {
	t.Parallel()
	d := decoder.NewAudioDecoder(1, nil, decoder.AudioWithName("my-decoder"))
	require.NotNil(t, d)
	d.Close()
}

// ---------------------------------------------------------------------------
// Multiple packets: codec parameters cached after first packet
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
// Integration tests — real data, real inner decoders (no CGO)
// ---------------------------------------------------------------------------

// lawFactory builds a factory map for a single PCM-law codec type using a
// real inner decoder constructor (no CGO required).
func lawFactory(ct gomedia.CodecType, newInner func() decoder.InnerAudioDecoder) map[gomedia.CodecType]func() decoder.InnerAudioDecoder {
	return map[gomedia.CodecType]func() decoder.InnerAudioDecoder{
		ct: newInner,
	}
}

// runLawIntegration is shared by ALAW and MULAW integration tests.
// It feeds every frame from the fixture through a real audioDecoder pipeline
// and asserts that each output PCM packet is exactly 2× the input frame size.
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

	// Send all frames into the pipeline.
	for i, frame := range frames {
		pkt := mocks.NewMockAudioPacket(ctrl)
		pkt.EXPECT().CodecParameters().Return(par).AnyTimes()
		pkt.EXPECT().Data().Return(frame).AnyTimes()
		pkt.EXPECT().Len().Return(len(frame)).AnyTimes()
		pkt.EXPECT().Timestamp().Return(time.Duration(0)).AnyTimes()
		pkt.EXPECT().SourceID().Return("src").AnyTimes()
		pkt.EXPECT().StartTime().Return(time.Time{}).AnyTimes()
		pkt.EXPECT().Duration().Return(128 * time.Millisecond).AnyTimes()
		pkt.EXPECT().Release().AnyTimes()
		select {
		case d.Packets() <- pkt:
		case <-time.After(time.Second):
			t.Fatalf("timeout sending frame %d", i)
		}
	}

	// Collect PCM packets — one per input frame.
	for i, frame := range frames {
		select {
		case out, ok := <-d.Samples():
			require.True(t, ok, "frame %d: samples channel closed early", i)
			require.Equal(t, 2*len(frame), out.Len(),
				"frame %d: PCM size must be 2× input (8-bit law → 16-bit PCM)", i)
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

// TestIntegration_ALAW_WithRingBuffer feeds ALAW frames through the pipeline
// with a GrowingRingAlloc, verifying PCM output size is correct via ring.
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

	pkt := mocks.NewMockAudioPacket(ctrl)
	pkt.EXPECT().CodecParameters().Return(par).AnyTimes()
	pkt.EXPECT().Data().Return(frame).AnyTimes()
	pkt.EXPECT().Len().Return(len(frame)).AnyTimes()
	pkt.EXPECT().Timestamp().Return(time.Duration(0)).AnyTimes()
	pkt.EXPECT().SourceID().Return("src").AnyTimes()
	pkt.EXPECT().StartTime().Return(time.Time{}).AnyTimes()
	pkt.EXPECT().Duration().Return(128 * time.Millisecond).AnyTimes()
	pkt.EXPECT().Release().AnyTimes()

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
