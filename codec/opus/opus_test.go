package opus

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/ugparu/gomedia"
)

// buildTOC constructs an Opus TOC byte from its fields.
// config: 0-31 (5 bits), stereo: 0/1 (1 bit), code: 0-3 (2 bits).
func buildTOC(config, stereo, code uint8) byte {
	return (config << configShift) | (stereo << 2) | (code & framesTocMask)
}

// ---------------------------------------------------------------------------
// PacketDuration — Code 0 (single frame, RFC 6716 §3.2.1)
// ---------------------------------------------------------------------------

// TestPacketDuration_Code0_SingleFrame verifies that a normal Code 0 packet
// returns exactly one frame duration.
func TestPacketDuration_Code0_SingleFrame(t *testing.T) {
	t.Parallel()
	// config 3 = SILK NB 60 ms
	toc := buildTOC(3, 0, 0)
	pkt := []byte{toc, 0xAA, 0xBB}
	dur, err := PacketDuration(pkt)
	require.NoError(t, err)
	require.Equal(t, 60*time.Millisecond, dur)
}

// TestPacketDuration_Code0_DTX verifies that a 1-byte DTX packet (TOC only)
// is treated as one frame, not zero. RFC 6716 §3.2.1 allows a 0-byte frame body.
func TestPacketDuration_Code0_DTX(t *testing.T) {
	t.Parallel()
	// config 1 = SILK NB 20 ms
	toc := buildTOC(1, 0, 0)
	pkt := []byte{toc} // only TOC byte — valid DTX single frame
	dur, err := PacketDuration(pkt)
	require.NoError(t, err)
	require.Equal(t, 20*time.Millisecond, dur)
}

// TestPacketDuration_Code0_AllConfigs walks all 32 configs and checks the
// expected single-frame duration matches the RFC 6716 §2 table.
func TestPacketDuration_Code0_AllConfigs(t *testing.T) {
	t.Parallel()
	expected := []time.Duration{
		// SILK NB: configs 0-3
		10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond, 60 * time.Millisecond,
		// SILK MB: configs 4-7
		10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond, 60 * time.Millisecond,
		// SILK WB: configs 8-11
		10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond, 60 * time.Millisecond,
		// Hybrid SWB: configs 12-13
		10 * time.Millisecond, 20 * time.Millisecond,
		// Hybrid FB: configs 14-15
		10 * time.Millisecond, 20 * time.Millisecond,
		// CELT NB: configs 16-19
		2500 * time.Microsecond, 5 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond,
		// CELT WB: configs 20-23
		2500 * time.Microsecond, 5 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond,
		// CELT SWB: configs 24-27
		2500 * time.Microsecond, 5 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond,
		// CELT FB: configs 28-31
		2500 * time.Microsecond, 5 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond,
	}
	for cfg := uint8(0); cfg < 32; cfg++ {
		toc := buildTOC(cfg, 0, 0)
		pkt := []byte{toc} // DTX single frame
		dur, err := PacketDuration(pkt)
		require.NoError(t, err, "config %d", cfg)
		require.Equal(t, expected[cfg], dur, "config %d", cfg)
	}
}

// ---------------------------------------------------------------------------
// PacketDuration — Code 1 (two equal-sized frames, RFC 6716 §3.2.2)
// ---------------------------------------------------------------------------

// TestPacketDuration_Code1_TwoFrames verifies that a Code 1 packet returns 2x the single-frame duration.
func TestPacketDuration_Code1_TwoFrames(t *testing.T) {
	t.Parallel()
	// config 0 = SILK NB 10 ms; two frames → 20 ms total
	toc := buildTOC(0, 0, 1)
	pkt := []byte{toc, 0x01, 0x02} // TOC + 2 bytes (1 byte per frame)
	dur, err := PacketDuration(pkt)
	require.NoError(t, err)
	require.Equal(t, 20*time.Millisecond, dur)
}

// TestPacketDuration_Code1_DTX verifies that a 1-byte Code 1 packet (two 0-byte DTX frames)
// returns 2x duration. RFC 6716 §3.2.2: N may be 1 for two empty frames.
func TestPacketDuration_Code1_DTX(t *testing.T) {
	t.Parallel()
	// config 0 = SILK NB 10 ms
	toc := buildTOC(0, 0, 1)
	pkt := []byte{toc} // TOC only: two equal-sized 0-byte frames
	dur, err := PacketDuration(pkt)
	require.NoError(t, err)
	require.Equal(t, 20*time.Millisecond, dur)
}

// ---------------------------------------------------------------------------
// PacketDuration — Code 2 (two variable-sized frames, RFC 6716 §3.2.3)
// ---------------------------------------------------------------------------

// TestPacketDuration_Code2_TwoFrames verifies Code 2 returns 2x duration.
func TestPacketDuration_Code2_TwoFrames(t *testing.T) {
	t.Parallel()
	// config 1 = SILK NB 20 ms; two frames → 40 ms
	toc := buildTOC(1, 0, 2)
	// first frame length encoded as 0x03, then 3 bytes frame1, 2 bytes frame2
	pkt := []byte{toc, 0x03, 0xAA, 0xBB, 0xCC, 0xDD, 0xEE}
	dur, err := PacketDuration(pkt)
	require.NoError(t, err)
	require.Equal(t, 40*time.Millisecond, dur)
}

// ---------------------------------------------------------------------------
// PacketDuration — Code 3 (multiple frames, RFC 6716 §3.2.5)
// ---------------------------------------------------------------------------

// TestPacketDuration_Code3_MultipleFrames verifies Code 3 counts frames from byte 1.
func TestPacketDuration_Code3_MultipleFrames(t *testing.T) {
	t.Parallel()
	// config 0 = SILK NB 10 ms; 3 frames → 30 ms
	toc := buildTOC(0, 0, 3)
	pkt := []byte{toc, 0x03, 0x01, 0x01, 0x01} // M=3, 3 x 1-byte frames
	dur, err := PacketDuration(pkt)
	require.NoError(t, err)
	require.Equal(t, 30*time.Millisecond, dur)
}

// TestPacketDuration_Code3_MaxFrames verifies M=48 (the maximum per RFC 6716 §3.2.5) is accepted.
func TestPacketDuration_Code3_MaxFrames(t *testing.T) {
	t.Parallel()
	// config 16 = CELT NB 2.5 ms; 48 frames → 120 ms (maximum packet duration)
	toc := buildTOC(16, 0, 3)
	// M=48 in the frame count byte (CBR flag=0, padding flag=0, M=48)
	pkt := make([]byte, 2+48)
	pkt[0] = toc
	pkt[1] = 48
	dur, err := PacketDuration(pkt)
	require.NoError(t, err)
	require.Equal(t, 48*2500*time.Microsecond, dur)
}

// TestPacketDuration_Code3_ZeroFrames verifies M=0 is rejected (RFC 6716 §3.2.5: M ∈ [1,48]).
func TestPacketDuration_Code3_ZeroFrames(t *testing.T) {
	t.Parallel()
	toc := buildTOC(0, 0, 3)
	pkt := []byte{toc, 0x00} // M=0 — invalid
	_, err := PacketDuration(pkt)
	require.Error(t, err)
}

// TestPacketDuration_Code3_TooManyFrames verifies M=49 is rejected (RFC 6716 §3.2.5: M ≤ 48).
func TestPacketDuration_Code3_TooManyFrames(t *testing.T) {
	t.Parallel()
	toc := buildTOC(0, 0, 3)
	pkt := []byte{toc, 49} // M=49 — invalid
	_, err := PacketDuration(pkt)
	require.Error(t, err)
}

// TestPacketDuration_Code3_TruncatedHeader verifies error when Code 3 packet is only 1 byte.
func TestPacketDuration_Code3_TruncatedHeader(t *testing.T) {
	t.Parallel()
	toc := buildTOC(0, 0, 3)
	pkt := []byte{toc} // missing frame count byte
	_, err := PacketDuration(pkt)
	require.Error(t, err)
}

// TestPacketDuration_EmptyPacket verifies that a zero-length packet returns an error.
func TestPacketDuration_EmptyPacket(t *testing.T) {
	t.Parallel()
	_, err := PacketDuration([]byte{})
	require.Error(t, err)
}

// TestPacketDuration_Stereo verifies that the stereo flag does not affect duration calculation.
func TestPacketDuration_Stereo(t *testing.T) {
	t.Parallel()
	// config 0 = SILK NB 10 ms, stereo=1, code=0 (single frame)
	toc := buildTOC(0, 1, 0)
	pkt := []byte{toc, 0xAA}
	dur, err := PacketDuration(pkt)
	require.NoError(t, err)
	require.Equal(t, 10*time.Millisecond, dur)
}

// ---------------------------------------------------------------------------
// CodecParameters
// ---------------------------------------------------------------------------

// TestNewCodecParameters_Mono verifies mono parameters are stored correctly.
func TestNewCodecParameters_Mono(t *testing.T) {
	t.Parallel()
	cp := NewCodecParameters(0, gomedia.ChMono, 48000)
	require.NotNil(t, cp)
	require.Equal(t, gomedia.OPUS, cp.Type())
	require.Equal(t, uint64(48000), cp.SampleRate())
	require.Equal(t, uint8(1), cp.Channels())
	require.Equal(t, gomedia.S16, cp.SampleFormat())
	require.Equal(t, "opus", cp.Tag())
}

// TestNewCodecParameters_Stereo verifies stereo parameters are stored correctly.
func TestNewCodecParameters_Stereo(t *testing.T) {
	t.Parallel()
	cp := NewCodecParameters(1, gomedia.ChStereo, 48000)
	require.NotNil(t, cp)
	require.Equal(t, uint8(2), cp.Channels())
	require.Equal(t, uint64(48000), cp.SampleRate())
}

// TestNewCodecParameters_StreamIndex verifies the stream index is set on the base parameters.
func TestNewCodecParameters_StreamIndex(t *testing.T) {
	t.Parallel()
	cp := NewCodecParameters(7, gomedia.ChMono, 16000)
	require.Equal(t, uint8(7), cp.StreamIndex())
}

// TestNewCodecParameters_RTPClockRate verifies 48000 Hz as per RFC 7587 §4.
// RFC 7587 mandates the RTP timestamp clock rate is always 48000 Hz for Opus,
// regardless of actual input sample rate.
func TestNewCodecParameters_RTPClockRate(t *testing.T) {
	t.Parallel()
	cp := NewCodecParameters(0, gomedia.ChMono, 48000)
	require.Equal(t, uint64(48000), cp.SampleRate(),
		"RFC 7587 §4: Opus RTP clock rate MUST be 48000 Hz")
}

// ---------------------------------------------------------------------------
// Packet
// ---------------------------------------------------------------------------

// TestNewPacket verifies all fields are stored correctly.
func TestNewPacket(t *testing.T) {
	t.Parallel()
	cp := NewCodecParameters(2, gomedia.ChStereo, 48000)
	data := []byte{0x01, 0x02, 0x03}
	ts := 960 * time.Millisecond // 48 frames of 20 ms
	dur := 20 * time.Millisecond
	absTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	sourceID := "rtsp://camera/audio"

	pkt := NewPacket(data, ts, sourceID, absTime, cp, dur)
	require.NotNil(t, pkt)
	require.Equal(t, uint8(2), pkt.StreamIndex())
	require.Equal(t, ts, pkt.Timestamp())
	require.Equal(t, dur, pkt.Duration())
	require.Equal(t, sourceID, pkt.SourceID())
	require.Equal(t, data, pkt.Data())
	require.Equal(t, len(data), pkt.Len())
	require.Equal(t, absTime, pkt.StartTime())
	require.Equal(t, gomedia.OPUS, pkt.CodecParameters().Type())
	require.Equal(t, uint64(48000), pkt.CodecParameters().SampleRate())
	require.Equal(t, uint8(2), pkt.CodecParameters().Channels())
}

// TestPacket_Clone_CopyData verifies Clone(true) produces an independent deep copy.
func TestPacket_Clone_CopyData(t *testing.T) {
	t.Parallel()
	cp := NewCodecParameters(0, gomedia.ChMono, 48000)
	original := []byte{0xAA, 0xBB, 0xCC}
	pkt := NewPacket(original, 20*time.Millisecond, "src", time.Time{}, cp, 20*time.Millisecond)

	cloned, ok := pkt.Clone(true).(*Packet)
	require.True(t, ok)
	require.Equal(t, pkt.Timestamp(), cloned.Timestamp())
	require.Equal(t, pkt.Duration(), cloned.Duration())
	require.Equal(t, pkt.Data(), cloned.Data())

	// Mutating the clone must not affect the original.
	cloned.Data()[0] = 0x00
	require.Equal(t, byte(0xAA), pkt.Data()[0], "original must be unaffected by clone mutation")
}

// TestPacket_Clone_SharedData verifies Clone(false) shares the underlying buffer.
func TestPacket_Clone_SharedData(t *testing.T) {
	t.Parallel()
	cp := NewCodecParameters(0, gomedia.ChMono, 48000)
	data := []byte{0x11, 0x22, 0x33}
	pkt := NewPacket(data, 0, "src", time.Time{}, cp, 0)

	cloned, ok := pkt.Clone(false).(*Packet)
	require.True(t, ok)
	require.Equal(t, pkt.Data(), cloned.Data())

	pkt.Release()
	cloned.Release()
}

// TestPacket_Release verifies Release is safe for heap-backed packets.
func TestPacket_Release(t *testing.T) {
	t.Parallel()
	cp := NewCodecParameters(0, gomedia.ChMono, 48000)
	pkt := NewPacket([]byte{0x01}, 0, "src", time.Time{}, cp, 0)
	require.NotPanics(t, pkt.Release)
}

// ---------------------------------------------------------------------------
// PacketDuration round-trip: build a Code 0 packet and confirm duration
// ---------------------------------------------------------------------------

// TestPacketDuration_RTPTimestampIncrement verifies that frame durations match the
// RTP timestamp increments defined in RFC 7587 §4 (at 48000 Hz clock).
func TestPacketDuration_RTPTimestampIncrement(t *testing.T) {
	t.Parallel()

	// RFC 7587 Table: frame size → samples at 48 kHz
	cases := []struct {
		config   uint8
		expected time.Duration
		samples  int // expected RTP timestamp increment at 48000 Hz
	}{
		{16, 2500 * time.Microsecond, 120},  // 2.5 ms CELT NB
		{17, 5 * time.Millisecond, 240},     // 5 ms CELT NB
		{18, 10 * time.Millisecond, 480},    // 10 ms CELT NB
		{19, 20 * time.Millisecond, 960},    // 20 ms CELT NB
		{0, 10 * time.Millisecond, 480},     // 10 ms SILK NB
		{1, 20 * time.Millisecond, 960},     // 20 ms SILK NB
		{2, 40 * time.Millisecond, 1920},    // 40 ms SILK NB
		{3, 60 * time.Millisecond, 2880},    // 60 ms SILK NB
	}

	for _, tc := range cases {
		toc := buildTOC(tc.config, 0, 0)
		pkt := []byte{toc}
		dur, err := PacketDuration(pkt)
		require.NoError(t, err, "config %d", tc.config)
		require.Equal(t, tc.expected, dur, "config %d", tc.config)

		// Verify the sample count matches RFC 7587 at 48000 Hz
		samplesAtClock := int(dur.Seconds() * 48000)
		require.Equal(t, tc.samples, samplesAtClock, "config %d: RTP sample increment", tc.config)
	}
}
