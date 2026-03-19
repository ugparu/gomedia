package encoder

import (
	"fmt"
	"testing"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/pcm"
	"github.com/ugparu/gomedia/mocks"
	"go.uber.org/mock/gomock"
)

// newTestPCMPacket creates a heap-backed PCM packet for testing.
func newTestPCMPacket(data []byte, codecPar *pcm.CodecParameters) *pcm.Packet {
	return pcm.NewPacket(data, 0, "test-source", time.Now(), codecPar, 20*time.Millisecond) //nolint:mnd // 20ms test duration
}

// closeEncoder stops the async loop and waits for it to finish.
func closeEncoder(enc gomedia.AudioEncoder) {
	ae := enc.(*audioEncoder)
	ae.AsyncManager.Close()
	<-ae.AsyncManager.Done()
}

func TestNewAudioEncoder(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockInner := mocks.NewMockInnerAudioEncoder(ctrl)

	called := false
	factory := func() InnerAudioEncoder {
		if called {
			return mocks.NewMockInnerAudioEncoder(ctrl)
		}
		called = true
		return mockInner
	}

	enc := NewAudioEncoder(10, factory) //nolint:mnd // channel buffer size

	if enc == nil {
		t.Fatal("expected non-nil encoder")
	}
	if enc.Samples() == nil {
		t.Fatal("expected non-nil input channel")
	}
	if enc.Packets() == nil {
		t.Fatal("expected non-nil output channel")
	}
}

func TestEncode_StartsAndStops(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockInner := mocks.NewMockInnerAudioEncoder(ctrl)
	// Only Release() calls Close — no packets sent so no param-change Close
	mockInner.EXPECT().Close().Times(1)

	enc := NewAudioEncoder(10, func() InnerAudioEncoder { //nolint:mnd // channel buffer size
		return mockInner
	})

	enc.Encode()
	closeEncoder(enc)
}

func TestStep_EncodesPacketAndSetsTimestamp(t *testing.T) {
	ctrl := gomock.NewController(t)

	codecPar := pcm.NewCodecParameters(0, gomedia.PCM, 1, 16000) //nolint:mnd // mono 16kHz

	pktDuration := 20 * time.Millisecond //nolint:mnd // 20ms frame duration
	outData := []byte{0x01, 0x02, 0x03}
	outPkt := pcm.NewPacket(outData, 0, "test-source", time.Now(), codecPar, pktDuration)

	mockInner := mocks.NewMockInnerAudioEncoder(ctrl)
	mockInner.EXPECT().Init(codecPar).Return(nil).Times(1)
	mockInner.EXPECT().Encode(gomock.Any()).Return([]gomedia.AudioPacket{outPkt}, nil).Times(1)
	// Close called twice: once from Step param-change (inpCodecPar nil→non-nil), once from Release
	mockInner.EXPECT().Close().Times(2) //nolint:mnd

	enc := NewAudioEncoder(10, func() InnerAudioEncoder { //nolint:mnd // channel buffer size
		return mockInner
	})

	enc.Encode()

	inPkt := newTestPCMPacket(make([]byte, 320), codecPar) //nolint:mnd // 320 bytes = 10ms mono 16kHz S16LE
	enc.Samples() <- inPkt

	select {
	case pkt := <-enc.Packets():
		if pkt.Timestamp() != 0 {
			t.Errorf("first packet timestamp = %v, want 0", pkt.Timestamp())
		}
		if pkt.Duration() != pktDuration {
			t.Errorf("packet duration = %v, want %v", pkt.Duration(), pktDuration)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for output packet")
	}

	closeEncoder(enc)
}

func TestStep_TimestampAccumulation(t *testing.T) {
	ctrl := gomock.NewController(t)

	codecPar := pcm.NewCodecParameters(0, gomedia.PCM, 1, 16000) //nolint:mnd // mono 16kHz
	pktDuration := 20 * time.Millisecond                         //nolint:mnd // 20ms frame duration

	mockInner := mocks.NewMockInnerAudioEncoder(ctrl)
	mockInner.EXPECT().Init(codecPar).Return(nil).Times(1)

	// Each Encode call returns one output packet
	mockInner.EXPECT().Encode(gomock.Any()).DoAndReturn(func(_ *pcm.Packet) ([]gomedia.AudioPacket, error) {
		out := pcm.NewPacket([]byte{0x01}, 0, "test", time.Now(), codecPar, pktDuration)
		return []gomedia.AudioPacket{out}, nil
	}).Times(3) //nolint:mnd // 3 packets
	mockInner.EXPECT().Close().Times(2) //nolint:mnd // param-change + Release

	enc := NewAudioEncoder(10, func() InnerAudioEncoder { //nolint:mnd // channel buffer size
		return mockInner
	})

	enc.Encode()

	// Send 3 packets, verify timestamps accumulate: 0, 20ms, 40ms
	for i := range 3 { //nolint:mnd // 3 packets
		inPkt := newTestPCMPacket(make([]byte, 320), codecPar) //nolint:mnd // 320 bytes
		enc.Samples() <- inPkt

		select {
		case pkt := <-enc.Packets():
			expected := time.Duration(i) * pktDuration
			if pkt.Timestamp() != expected {
				t.Errorf("packet %d: timestamp = %v, want %v", i, pkt.Timestamp(), expected)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for packet %d", i)
		}
	}

	closeEncoder(enc)
}

func TestStep_MultipleOutputPacketsPerEncode(t *testing.T) {
	ctrl := gomock.NewController(t)

	codecPar := pcm.NewCodecParameters(0, gomedia.PCM, 1, 16000) //nolint:mnd // mono 16kHz
	pktDuration := 10 * time.Millisecond                         //nolint:mnd // 10ms frame duration

	mockInner := mocks.NewMockInnerAudioEncoder(ctrl)
	mockInner.EXPECT().Init(codecPar).Return(nil).Times(1)

	// Single Encode call returns 3 output packets
	mockInner.EXPECT().Encode(gomock.Any()).DoAndReturn(func(_ *pcm.Packet) ([]gomedia.AudioPacket, error) {
		var out []gomedia.AudioPacket
		for range 3 { //nolint:mnd // 3 output packets
			out = append(out, pcm.NewPacket([]byte{0x01}, 0, "test", time.Now(), codecPar, pktDuration))
		}
		return out, nil
	}).Times(1)
	mockInner.EXPECT().Close().Times(2) //nolint:mnd // param-change + Release

	enc := NewAudioEncoder(10, func() InnerAudioEncoder { //nolint:mnd // channel buffer size
		return mockInner
	})

	enc.Encode()

	inPkt := newTestPCMPacket(make([]byte, 320), codecPar) //nolint:mnd // 320 bytes
	enc.Samples() <- inPkt

	// All 3 packets should have sequential timestamps: 0, 10ms, 20ms
	for i := range 3 { //nolint:mnd // 3 packets
		select {
		case pkt := <-enc.Packets():
			expected := time.Duration(i) * pktDuration
			if pkt.Timestamp() != expected {
				t.Errorf("packet %d: timestamp = %v, want %v", i, pkt.Timestamp(), expected)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for packet %d", i)
		}
	}

	closeEncoder(enc)
}

func TestStep_EncodeReturnsNoPackets(t *testing.T) {
	ctrl := gomock.NewController(t)

	codecPar := pcm.NewCodecParameters(0, gomedia.PCM, 1, 16000) //nolint:mnd // mono 16kHz

	mockInner := mocks.NewMockInnerAudioEncoder(ctrl)
	mockInner.EXPECT().Init(codecPar).Return(nil).Times(1)
	mockInner.EXPECT().Encode(gomock.Any()).Return(nil, nil).Times(1)

	// Second call returns a packet so we can verify the encoder is still alive
	secondPkt := pcm.NewPacket([]byte{0x01}, 0, "test", time.Now(), codecPar, 20*time.Millisecond) //nolint:mnd
	mockInner.EXPECT().Encode(gomock.Any()).Return([]gomedia.AudioPacket{secondPkt}, nil).Times(1)
	mockInner.EXPECT().Close().Times(2) //nolint:mnd // param-change + Release

	enc := NewAudioEncoder(10, func() InnerAudioEncoder { //nolint:mnd
		return mockInner
	})

	enc.Encode()

	// First packet produces no output
	enc.Samples() <- newTestPCMPacket(make([]byte, 320), codecPar) //nolint:mnd

	// Second packet produces output — encoder still works
	enc.Samples() <- newTestPCMPacket(make([]byte, 320), codecPar) //nolint:mnd

	select {
	case <-enc.Packets():
		// success
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for output packet after empty encode")
	}

	closeEncoder(enc)
}

func TestStep_CodecParameterChange(t *testing.T) {
	ctrl := gomock.NewController(t)

	codecPar1 := pcm.NewCodecParameters(0, gomedia.PCM, 1, 16000) //nolint:mnd // mono 16kHz
	codecPar2 := pcm.NewCodecParameters(0, gomedia.PCM, 2, 48000) //nolint:mnd // stereo 48kHz
	pktDuration := 20 * time.Millisecond                           //nolint:mnd

	// Use separate mocks for each phase. The factory returns them in order:
	// call 1 (constructor) → constructorInner
	// call 2 (first packet reinit, inpCodecPar nil→codecPar1) → firstInner
	// call 3 (second packet reinit, codecPar1→codecPar2) → secondInner
	constructorInner := mocks.NewMockInnerAudioEncoder(ctrl)
	firstInner := mocks.NewMockInnerAudioEncoder(ctrl)
	secondInner := mocks.NewMockInnerAudioEncoder(ctrl)

	callCount := 0
	inners := []InnerAudioEncoder{constructorInner, firstInner, secondInner}
	factory := func() InnerAudioEncoder {
		idx := callCount
		callCount++
		return inners[idx]
	}

	// constructorInner: only Close'd when first packet triggers param-change
	constructorInner.EXPECT().Close().Times(1)

	// firstInner: Init with codecPar1, Encode one packet, Close'd when codecPar2 triggers reinit
	firstInner.EXPECT().Init(codecPar1).Return(nil).Times(1)
	firstInner.EXPECT().Encode(gomock.Any()).DoAndReturn(func(_ *pcm.Packet) ([]gomedia.AudioPacket, error) {
		out := pcm.NewPacket([]byte{0x01}, 0, "test", time.Now(), codecPar1, pktDuration)
		return []gomedia.AudioPacket{out}, nil
	}).Times(1)
	firstInner.EXPECT().Close().Times(1)

	// secondInner: Init with codecPar2, Encode one packet, Close'd by Release
	secondInner.EXPECT().Init(codecPar2).Return(nil).Times(1)
	secondInner.EXPECT().Encode(gomock.Any()).DoAndReturn(func(_ *pcm.Packet) ([]gomedia.AudioPacket, error) {
		out := pcm.NewPacket([]byte{0x02}, 0, "test", time.Now(), codecPar2, pktDuration)
		return []gomedia.AudioPacket{out}, nil
	}).Times(1)
	secondInner.EXPECT().Close().Times(1)

	enc := NewAudioEncoder(10, factory) //nolint:mnd
	enc.Encode()

	// Send packet with first codec params
	enc.Samples() <- newTestPCMPacket(make([]byte, 320), codecPar1) //nolint:mnd
	select {
	case pkt := <-enc.Packets():
		if pkt.Timestamp() != 0 {
			t.Errorf("first packet timestamp = %v, want 0", pkt.Timestamp())
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first packet")
	}

	// Send packet with different codec params — triggers reinit
	enc.Samples() <- newTestPCMPacket(make([]byte, 640), codecPar2) //nolint:mnd
	select {
	case pkt := <-enc.Packets():
		// Timestamp should continue from where it left off
		if pkt.Timestamp() != pktDuration {
			t.Errorf("second packet timestamp = %v, want %v", pkt.Timestamp(), pktDuration)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second packet after reinit")
	}

	closeEncoder(enc)
}

func TestStep_EncodeError(t *testing.T) {
	ctrl := gomock.NewController(t)

	codecPar := pcm.NewCodecParameters(0, gomedia.PCM, 1, 16000) //nolint:mnd
	encodeErr := fmt.Errorf("encode failed")

	mockInner := mocks.NewMockInnerAudioEncoder(ctrl)
	mockInner.EXPECT().Init(codecPar).Return(nil).Times(1)
	mockInner.EXPECT().Encode(gomock.Any()).Return(nil, encodeErr).Times(1)

	// After error, failsafe manager continues — next call succeeds
	outPkt := pcm.NewPacket([]byte{0x01}, 0, "test", time.Now(), codecPar, 20*time.Millisecond) //nolint:mnd
	mockInner.EXPECT().Encode(gomock.Any()).Return([]gomedia.AudioPacket{outPkt}, nil).Times(1)
	mockInner.EXPECT().Close().Times(2) //nolint:mnd // param-change + Release

	enc := NewAudioEncoder(10, func() InnerAudioEncoder { //nolint:mnd
		return mockInner
	})

	enc.Encode()

	// First packet triggers error — failsafe manager logs and continues
	enc.Samples() <- newTestPCMPacket(make([]byte, 320), codecPar) //nolint:mnd

	// Second packet succeeds
	enc.Samples() <- newTestPCMPacket(make([]byte, 320), codecPar) //nolint:mnd

	select {
	case <-enc.Packets():
		// success — encoder recovered from error
	case <-time.After(time.Second):
		t.Fatal("timed out — encoder did not recover from encode error")
	}

	closeEncoder(enc)
}

func TestStep_InitError(t *testing.T) {
	ctrl := gomock.NewController(t)

	codecPar := pcm.NewCodecParameters(0, gomedia.PCM, 1, 16000) //nolint:mnd
	codecPar2 := pcm.NewCodecParameters(0, gomedia.PCM, 2, 48000) //nolint:mnd // different params to trigger second init
	initErr := fmt.Errorf("init failed")
	pktDuration := 20 * time.Millisecond //nolint:mnd

	mockInner := mocks.NewMockInnerAudioEncoder(ctrl)
	// First init fails
	mockInner.EXPECT().Init(codecPar).Return(initErr).Times(1)
	// Second init (different params) succeeds — proves encoder recovered
	mockInner.EXPECT().Init(codecPar2).Return(nil).Times(1)
	mockInner.EXPECT().Encode(gomock.Any()).DoAndReturn(func(_ *pcm.Packet) ([]gomedia.AudioPacket, error) {
		out := pcm.NewPacket([]byte{0x01}, 0, "test", time.Now(), codecPar2, pktDuration)
		return []gomedia.AudioPacket{out}, nil
	}).Times(1)
	mockInner.EXPECT().Close().MinTimes(1)

	enc := NewAudioEncoder(10, func() InnerAudioEncoder { //nolint:mnd
		return mockInner
	})

	enc.Encode()

	// First packet — init fails, failsafe manager logs error and continues
	enc.Samples() <- newTestPCMPacket(make([]byte, 320), codecPar) //nolint:mnd

	// Second packet with different params — triggers new init that succeeds
	enc.Samples() <- newTestPCMPacket(make([]byte, 640), codecPar2) //nolint:mnd

	select {
	case <-enc.Packets():
		// success — encoder recovered from init error
	case <-time.After(time.Second):
		t.Fatal("timed out — encoder did not recover from init error")
	}

	closeEncoder(enc)
}

func TestRelease_ClosesInnerAndOutputChannel(t *testing.T) {
	ctrl := gomock.NewController(t)

	mockInner := mocks.NewMockInnerAudioEncoder(ctrl)
	mockInner.EXPECT().Close().Times(1)

	enc := NewAudioEncoder(10, func() InnerAudioEncoder { //nolint:mnd
		return mockInner
	})

	enc.Encode()
	closeEncoder(enc)

	// After Close + Done, output channel should be closed
	_, open := <-enc.Packets()
	if open {
		t.Error("output channel should be closed after Release")
	}
}

func TestPackets_ReturnsReadOnlyChannel(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockInner := mocks.NewMockInnerAudioEncoder(ctrl)
	mockInner.EXPECT().Close().Times(1)

	enc := NewAudioEncoder(10, func() InnerAudioEncoder { //nolint:mnd
		return mockInner
	})

	ch := enc.Packets()
	if ch == nil {
		t.Fatal("Packets() returned nil")
	}

	enc.Encode()
	closeEncoder(enc)
}

func TestSamples_ReturnsSendOnlyChannel(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockInner := mocks.NewMockInnerAudioEncoder(ctrl)
	mockInner.EXPECT().Close().Times(1)

	enc := NewAudioEncoder(10, func() InnerAudioEncoder { //nolint:mnd
		return mockInner
	})

	ch := enc.Samples()
	if ch == nil {
		t.Fatal("Samples() returned nil")
	}

	enc.Encode()
	closeEncoder(enc)
}

func TestStep_ProcessesPacketWithoutPanic(t *testing.T) {
	ctrl := gomock.NewController(t)

	codecPar := pcm.NewCodecParameters(0, gomedia.PCM, 1, 16000) //nolint:mnd

	mockInner := mocks.NewMockInnerAudioEncoder(ctrl)
	mockInner.EXPECT().Init(codecPar).Return(nil).Times(1)

	outPkt := pcm.NewPacket([]byte{0x01}, 0, "test", time.Now(), codecPar, 20*time.Millisecond) //nolint:mnd
	mockInner.EXPECT().Encode(gomock.Any()).Return([]gomedia.AudioPacket{outPkt}, nil).Times(1)
	mockInner.EXPECT().Close().Times(2) //nolint:mnd // param-change + Release

	enc := NewAudioEncoder(10, func() InnerAudioEncoder { //nolint:mnd
		return mockInner
	})

	enc.Encode()

	inPkt := newTestPCMPacket(make([]byte, 320), codecPar) //nolint:mnd
	enc.Samples() <- inPkt

	// Wait for output to confirm Step processed the packet
	select {
	case <-enc.Packets():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for output packet")
	}

	closeEncoder(enc)
}

func TestStep_SameCodecParamsNoReinit(t *testing.T) {
	ctrl := gomock.NewController(t)

	codecPar := pcm.NewCodecParameters(0, gomedia.PCM, 1, 16000) //nolint:mnd
	pktDuration := 20 * time.Millisecond                         //nolint:mnd

	mockInner := mocks.NewMockInnerAudioEncoder(ctrl)
	// Init called once — first packet triggers param-change (nil→codecPar),
	// subsequent packets with same pointer skip reinit
	mockInner.EXPECT().Init(codecPar).Return(nil).Times(1)
	mockInner.EXPECT().Encode(gomock.Any()).DoAndReturn(func(_ *pcm.Packet) ([]gomedia.AudioPacket, error) {
		out := pcm.NewPacket([]byte{0x01}, 0, "test", time.Now(), codecPar, pktDuration)
		return []gomedia.AudioPacket{out}, nil
	}).Times(3) //nolint:mnd
	mockInner.EXPECT().Close().Times(2) //nolint:mnd // param-change + Release

	enc := NewAudioEncoder(10, func() InnerAudioEncoder { //nolint:mnd
		return mockInner
	})

	enc.Encode()

	// Send 3 packets with the same CodecParameters pointer — should NOT reinit
	for i := range 3 { //nolint:mnd
		enc.Samples() <- newTestPCMPacket(make([]byte, 320), codecPar) //nolint:mnd
		select {
		case <-enc.Packets():
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for packet %d", i)
		}
	}

	closeEncoder(enc)
}

func TestChannelBufferSize(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockInner := mocks.NewMockInnerAudioEncoder(ctrl)
	mockInner.EXPECT().Close().Times(1)

	chanSize := 5 //nolint:mnd
	enc := NewAudioEncoder(chanSize, func() InnerAudioEncoder {
		return mockInner
	})

	// Verify channels have the specified buffer capacity
	ae := enc.(*audioEncoder)
	if cap(ae.inpSamples) != chanSize {
		t.Errorf("input channel capacity = %d, want %d", cap(ae.inpSamples), chanSize)
	}
	if cap(ae.outSamples) != chanSize {
		t.Errorf("output channel capacity = %d, want %d", cap(ae.outSamples), chanSize)
	}

	enc.Encode()
	closeEncoder(enc)
}

func TestEncode_DoubleStartIsSafe(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockInner := mocks.NewMockInnerAudioEncoder(ctrl)
	mockInner.EXPECT().Close().Times(1)

	enc := NewAudioEncoder(10, func() InnerAudioEncoder { //nolint:mnd
		return mockInner
	})

	// FailSafeAsyncManager uses sync.Once — double Start is safe
	enc.Encode()
	enc.Encode() // should not panic or error

	closeEncoder(enc)
}
