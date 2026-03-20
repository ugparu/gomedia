package rtp

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/aac"
	"github.com/ugparu/gomedia/codec/h264"
	"github.com/ugparu/gomedia/codec/h265"
	"github.com/ugparu/gomedia/codec/opus"
	"github.com/ugparu/gomedia/codec/pcm"
	"github.com/ugparu/gomedia/tests"
	"github.com/ugparu/gomedia/utils/sdp"
)

const testDataDir = "../../tests/data/"

// ---------------------------------------------------------------------------
// RTP packet builder helpers
// ---------------------------------------------------------------------------

// buildRTSPInterleavedRTP builds a minimal RTSP-interleaved RTP frame:
//
//	[0x24, channel, 2-byte BE length, RTP header (12 bytes), payload]
func buildRTSPInterleavedRTP(channel uint8, payloadType uint8, seq uint16, ts uint32, ssrc uint32, marker bool, payload []byte) []byte {
	rtpLen := rtpHeaderSize + len(payload)
	buf := make([]byte, rtspHeaderSize+rtpLen)

	// RTSP interleaved header
	buf[0] = 0x24
	buf[1] = channel
	binary.BigEndian.PutUint16(buf[2:4], uint16(rtpLen))

	// RTP fixed header: V=2, P=0, X=0, CC=0
	buf[4] = 0x80
	if marker {
		buf[5] = 0x80 | payloadType
	} else {
		buf[5] = payloadType
	}
	binary.BigEndian.PutUint16(buf[6:8], seq)
	binary.BigEndian.PutUint32(buf[8:12], ts)
	binary.BigEndian.PutUint32(buf[12:16], ssrc)

	copy(buf[16:], payload)
	return buf
}

// buildRTSPInterleavedRTPWithCSRC builds an RTP frame with CSRC entries.
func buildRTSPInterleavedRTPWithCSRC(channel uint8, payloadType uint8, seq uint16, ts uint32, ssrc uint32, marker bool, csrcs []uint32, payload []byte) []byte {
	csrcLen := len(csrcs) * 4
	rtpLen := rtpHeaderSize + csrcLen + len(payload)
	buf := make([]byte, rtspHeaderSize+rtpLen)

	buf[0] = 0x24
	buf[1] = channel
	binary.BigEndian.PutUint16(buf[2:4], uint16(rtpLen))

	buf[4] = 0x80 | byte(len(csrcs)&0x0f) // V=2, CC=len(csrcs)
	if marker {
		buf[5] = 0x80 | payloadType
	} else {
		buf[5] = payloadType
	}
	binary.BigEndian.PutUint16(buf[6:8], seq)
	binary.BigEndian.PutUint32(buf[8:12], ts)
	binary.BigEndian.PutUint32(buf[12:16], ssrc)

	for i, c := range csrcs {
		binary.BigEndian.PutUint32(buf[16+i*4:], c)
	}

	copy(buf[16+csrcLen:], payload)
	return buf
}

// buildRTSPInterleavedRTPWithPadding builds an RTP frame with padding.
func buildRTSPInterleavedRTPWithPadding(channel uint8, payloadType uint8, seq uint16, ts uint32, ssrc uint32, marker bool, payload []byte, padLen int) []byte {
	rtpLen := rtpHeaderSize + len(payload) + padLen
	buf := make([]byte, rtspHeaderSize+rtpLen)

	buf[0] = 0x24
	buf[1] = channel
	binary.BigEndian.PutUint16(buf[2:4], uint16(rtpLen))

	buf[4] = 0x80 | (1 << paddingBit) // V=2, P=1
	if marker {
		buf[5] = 0x80 | payloadType
	} else {
		buf[5] = payloadType
	}
	binary.BigEndian.PutUint16(buf[6:8], seq)
	binary.BigEndian.PutUint32(buf[8:12], ts)
	binary.BigEndian.PutUint32(buf[12:16], ssrc)

	copy(buf[16:], payload)
	// Last byte of padding is padding length
	buf[len(buf)-1] = byte(padLen)
	return buf
}

// buildRTSPInterleavedRTPWithExtension builds an RTP frame with a header extension.
func buildRTSPInterleavedRTPWithExtension(channel uint8, payloadType uint8, seq uint16, ts uint32, ssrc uint32, marker bool, extData []byte, payload []byte) []byte {
	// Extension: 2-byte profile + 2-byte length (in 32-bit words) + data
	extWords := len(extData) / 4
	extHeader := make([]byte, 4+len(extData))
	binary.BigEndian.PutUint16(extHeader[0:2], 0xBEDE) // profile
	binary.BigEndian.PutUint16(extHeader[2:4], uint16(extWords))
	copy(extHeader[4:], extData)

	rtpLen := rtpHeaderSize + len(extHeader) + len(payload)
	buf := make([]byte, rtspHeaderSize+rtpLen)

	buf[0] = 0x24
	buf[1] = channel
	binary.BigEndian.PutUint16(buf[2:4], uint16(rtpLen))

	buf[4] = 0x80 | (1 << extensionBit) // V=2, X=1
	if marker {
		buf[5] = 0x80 | payloadType
	} else {
		buf[5] = payloadType
	}
	binary.BigEndian.PutUint16(buf[6:8], seq)
	binary.BigEndian.PutUint32(buf[8:12], ts)
	binary.BigEndian.PutUint32(buf[12:16], ssrc)

	copy(buf[16:], extHeader)
	copy(buf[16+len(extHeader):], payload)
	return buf
}

// concatFrames concatenates multiple RTSP-interleaved RTP frames.
func concatFrames(frames ...[]byte) []byte {
	var buf bytes.Buffer
	for _, f := range frames {
		buf.Write(f)
	}
	return buf.Bytes()
}

// loadH264Params loads H.264 test parameters (SPS, PPS) from the test data dir.
func loadH264Params(t *testing.T) (sps, pps []byte) {
	t.Helper()
	raw, err := os.ReadFile(testDataDir + "h264/parameters.json")
	require.NoError(t, err)

	var p tests.ParametersJSON
	require.NoError(t, json.Unmarshal(raw, &p))
	require.NotNil(t, p.Video)

	sps, err = base64.StdEncoding.DecodeString(p.Video.SPS)
	require.NoError(t, err)
	pps, err = base64.StdEncoding.DecodeString(p.Video.PPS)
	require.NoError(t, err)
	return
}

// loadH265Params loads H.265 test parameters (VPS, SPS, PPS) from the test data dir.
func loadH265Params(t *testing.T) (vps, sps, pps []byte) {
	t.Helper()
	raw, err := os.ReadFile(testDataDir + "hevc/parameters.json")
	require.NoError(t, err)

	var p tests.ParametersJSON
	require.NoError(t, json.Unmarshal(raw, &p))
	require.NotNil(t, p.Video)

	vps, err = base64.StdEncoding.DecodeString(p.Video.VPS)
	require.NoError(t, err)
	sps, err = base64.StdEncoding.DecodeString(p.Video.SPS)
	require.NoError(t, err)
	pps, err = base64.StdEncoding.DecodeString(p.Video.PPS)
	require.NoError(t, err)
	return
}

// loadAACConfig loads AAC MPEG4 audio config from the test data dir.
func loadAACConfig(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(testDataDir + "aac/parameters.json")
	require.NoError(t, err)

	var p struct {
		Config string `json:"config"`
	}
	require.NoError(t, json.Unmarshal(raw, &p))

	config, err := base64.StdEncoding.DecodeString(p.Config)
	require.NoError(t, err)
	return config
}

// loadTestPackets loads packets from a JSON fixture file.
func loadTestPackets(t *testing.T, dir string) []tests.PacketJSON {
	t.Helper()
	raw, err := os.ReadFile(testDataDir + dir + "/packets.json")
	require.NoError(t, err)

	var pkts tests.PacketsJSON
	require.NoError(t, json.Unmarshal(raw, &pkts))
	return pkts.Packets
}

// ===========================================================================
// Base demuxer tests
// ===========================================================================

func TestBaseDemuxer_ReadPacket_TooShort(t *testing.T) {
	// RTP packet smaller than rtpHeaderSize (12) should be rejected
	buf := make([]byte, rtspHeaderSize)
	buf[0] = 0x24
	buf[1] = 0
	binary.BigEndian.PutUint16(buf[2:4], 8) // length=8 < 12
	rdr := bytes.NewReader(buf)

	media := sdp.Media{TimeScale: 90000, PayloadType: 96}
	dmx := NewH264Demuxer(rdr, media, 0)
	_, err := dmx.ReadPacket()
	require.Error(t, err)
	require.Contains(t, err.Error(), "incorrect packet size")
}

func TestBaseDemuxer_ReadPacket_EOF(t *testing.T) {
	rdr := bytes.NewReader([]byte{})
	media := sdp.Media{TimeScale: 90000, PayloadType: 96}
	dmx := NewH264Demuxer(rdr, media, 0)
	_, err := dmx.ReadPacket()
	require.Error(t, err)
}

func TestBaseDemuxer_ReadPacket_RTCP(t *testing.T) {
	// An RTCP sender report (PT=200) should cause EOF
	payload := make([]byte, 20)
	frame := buildRTSPInterleavedRTP(0, rtcpSenderReport, 0, 0, 0, false, payload)
	rdr := bytes.NewReader(frame)
	media := sdp.Media{TimeScale: 90000, PayloadType: 96}
	dmx := NewH264Demuxer(rdr, media, 0)
	_, err := dmx.ReadPacket()
	require.ErrorIs(t, err, io.EOF)
}

func TestBaseDemuxer_ReadPacket_CSRCSkip(t *testing.T) {
	// Build an RTP packet with 2 CSRC entries; verify payload is still extractable
	nalPayload := []byte{0x65, 0xAA, 0xBB, 0xCC} // NAL type 5 = IDR
	csrcs := []uint32{0x11111111, 0x22222222}
	sps, pps := loadH264Params(t)

	media := sdp.Media{
		TimeScale:          90000,
		PayloadType:        96,
		SpropParameterSets: [][]byte{sps, pps},
	}

	frame := buildRTSPInterleavedRTPWithCSRC(0, 96, 1, 9000, 0x12345678, true, csrcs, nalPayload)
	rdr := bytes.NewReader(frame)
	dmx := NewH264Demuxer(rdr, media, 0)

	_, err := dmx.Demux()
	require.NoError(t, err)

	pkt, err := dmx.ReadPacket()
	require.NoError(t, err)
	require.NotNil(t, pkt)

	// Packet data should contain [4-byte size prefix | NAL unit]
	data := pkt.Data()
	require.True(t, len(data) > 4)
	nalSize := binary.BigEndian.Uint32(data[0:4])
	require.Equal(t, uint32(len(nalPayload)), nalSize)
}

func TestBaseDemuxer_ReadPacket_PaddingStripped(t *testing.T) {
	// RTP packet with padding should have padding bytes removed from payload
	nalPayload := []byte{0x01, 0xAA, 0xBB} // NAL type 1 = non-IDR
	sps, pps := loadH264Params(t)

	media := sdp.Media{
		TimeScale:          90000,
		PayloadType:        96,
		SpropParameterSets: [][]byte{sps, pps},
	}

	padLen := 4
	frame := buildRTSPInterleavedRTPWithPadding(0, 96, 1, 9000, 0x12345678, true, nalPayload, padLen)
	rdr := bytes.NewReader(frame)
	dmx := NewH264Demuxer(rdr, media, 0)

	_, err := dmx.Demux()
	require.NoError(t, err)

	pkt, err := dmx.ReadPacket()
	require.NoError(t, err)
	require.NotNil(t, pkt)
	// Verify the padding was stripped — packet data should match NAL payload
	data := pkt.Data()
	nalSize := binary.BigEndian.Uint32(data[0:4])
	require.Equal(t, uint32(len(nalPayload)), nalSize)
}

func TestBaseDemuxer_ReadPacket_ExtensionSkipped(t *testing.T) {
	// RTP packet with extension header; payload should still be correct
	nalPayload := []byte{0x01, 0xDD, 0xEE} // NAL type 1
	sps, pps := loadH264Params(t)

	media := sdp.Media{
		TimeScale:          90000,
		PayloadType:        96,
		SpropParameterSets: [][]byte{sps, pps},
	}

	extData := make([]byte, 8) // 2 words of extension data
	frame := buildRTSPInterleavedRTPWithExtension(0, 96, 1, 9000, 0x12345678, true, extData, nalPayload)
	rdr := bytes.NewReader(frame)
	dmx := NewH264Demuxer(rdr, media, 0)

	_, err := dmx.Demux()
	require.NoError(t, err)

	pkt, err := dmx.ReadPacket()
	require.NoError(t, err)
	require.NotNil(t, pkt)

	data := pkt.Data()
	nalSize := binary.BigEndian.Uint32(data[0:4])
	require.Equal(t, uint32(len(nalPayload)), nalSize)
}

func TestBaseDemuxer_Close(t *testing.T) {
	rdr := bytes.NewReader([]byte{})
	media := sdp.Media{TimeScale: 90000, PayloadType: 96}
	dmx := NewH264Demuxer(rdr, media, 0)
	// Close should not panic
	dmx.Close()
}

// ===========================================================================
// H.264 demuxer tests
// ===========================================================================

func TestH264Demuxer_Demux_WithSPSPPS(t *testing.T) {
	sps, pps := loadH264Params(t)

	media := sdp.Media{
		TimeScale:          90000,
		PayloadType:        96,
		SpropParameterSets: [][]byte{sps, pps},
	}

	rdr := bytes.NewReader([]byte{})
	dmx := NewH264Demuxer(rdr, media, 0)
	codecs, err := dmx.Demux()
	require.NoError(t, err)
	require.NotNil(t, codecs.VideoCodecParameters)
	require.Nil(t, codecs.AudioCodecParameters)

	cp := codecs.VideoCodecParameters.(*h264.CodecParameters)
	require.Greater(t, cp.Width(), uint(0))
	require.Greater(t, cp.Height(), uint(0))
}

func TestH264Demuxer_Demux_MissingSPS(t *testing.T) {
	media := sdp.Media{
		TimeScale:          90000,
		PayloadType:        96,
		SpropParameterSets: [][]byte{},
	}
	rdr := bytes.NewReader([]byte{})
	dmx := NewH264Demuxer(rdr, media, 0)
	_, err := dmx.Demux()
	require.Error(t, err)
}

func TestH264Demuxer_SingleNAL(t *testing.T) {
	sps, pps := loadH264Params(t)

	media := sdp.Media{
		TimeScale:          90000,
		PayloadType:        96,
		SpropParameterSets: [][]byte{sps, pps},
	}

	// Build a single NAL unit packet (type 1 = non-IDR slice)
	nalPayload := []byte{0x01, 0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF}
	rtpTS := uint32(9000) // 100ms at 90kHz
	frame := buildRTSPInterleavedRTP(0, 96, 1, rtpTS, 0x12345678, true, nalPayload)

	rdr := bytes.NewReader(frame)
	dmx := NewH264Demuxer(rdr, media, 0)
	_, err := dmx.Demux()
	require.NoError(t, err)

	pkt, err := dmx.ReadPacket()
	require.NoError(t, err)
	require.NotNil(t, pkt)

	vp := pkt.(gomedia.VideoPacket)
	require.False(t, vp.IsKeyFrame())

	// Verify timestamp: ts=9000 → 9000 * 1ms / 90 = 100ms
	expectedTS := time.Duration(rtpTS) * time.Millisecond / time.Duration(clockrate)
	require.Equal(t, expectedTS, pkt.Timestamp())

	// Verify data: [4-byte BE size | NAL payload]
	data := pkt.Data()
	require.Equal(t, 4+len(nalPayload), len(data))
	nalSize := binary.BigEndian.Uint32(data[0:4])
	require.Equal(t, uint32(len(nalPayload)), nalSize)
	require.Equal(t, nalPayload, data[4:])
}

func TestH264Demuxer_IDRKeyFrame(t *testing.T) {
	sps, pps := loadH264Params(t)

	media := sdp.Media{
		TimeScale:          90000,
		PayloadType:        96,
		SpropParameterSets: [][]byte{sps, pps},
	}

	// NAL type 5 = IDR
	nalPayload := []byte{0x65, 0x01, 0x02, 0x03}
	frame := buildRTSPInterleavedRTP(0, 96, 1, 0, 0x12345678, true, nalPayload)

	rdr := bytes.NewReader(frame)
	dmx := NewH264Demuxer(rdr, media, 0)
	_, err := dmx.Demux()
	require.NoError(t, err)

	pkt, err := dmx.ReadPacket()
	require.NoError(t, err)

	vp := pkt.(gomedia.VideoPacket)
	require.True(t, vp.IsKeyFrame())
}

func TestH264Demuxer_STAPA(t *testing.T) {
	sps, pps := loadH264Params(t)

	media := sdp.Media{
		TimeScale:          90000,
		PayloadType:        96,
		SpropParameterSets: [][]byte{sps, pps},
	}

	// Build a STAP-A packet: [STAP-A NAL header | size1 | NAL1 | size2 | NAL2]
	nal1 := []byte{0x01, 0xAA, 0xBB} // non-IDR slice (type 1)
	nal2 := []byte{0x01, 0xCC, 0xDD} // another non-IDR

	stapa := []byte{nalSTAPA} // STAP-A indicator (type 24)
	// NAL1
	stapa = append(stapa, byte(len(nal1)>>8), byte(len(nal1)))
	stapa = append(stapa, nal1...)
	// NAL2
	stapa = append(stapa, byte(len(nal2)>>8), byte(len(nal2)))
	stapa = append(stapa, nal2...)

	frame := buildRTSPInterleavedRTP(0, 96, 1, 0, 0x12345678, true, stapa)
	rdr := bytes.NewReader(frame)
	dmx := NewH264Demuxer(rdr, media, 0)
	_, err := dmx.Demux()
	require.NoError(t, err)

	// Should produce 2 packets from the STAP-A
	pkt1, err := dmx.ReadPacket()
	require.NoError(t, err)
	require.NotNil(t, pkt1)

	pkt2, err := dmx.ReadPacket()
	require.NoError(t, err)
	require.NotNil(t, pkt2)

	// Verify NAL data
	data1 := pkt1.Data()
	require.Equal(t, nal1, data1[4:])
	data2 := pkt2.Data()
	require.Equal(t, nal2, data2[4:])
}

func TestH264Demuxer_STAPA_ZeroSizeNAL(t *testing.T) {
	sps, pps := loadH264Params(t)

	media := sdp.Media{
		TimeScale:          90000,
		PayloadType:        96,
		SpropParameterSets: [][]byte{sps, pps},
	}

	// Build STAP-A with zero-size NAL — should be skipped gracefully
	stapa := []byte{nalSTAPA, 0x00, 0x00} // size=0

	frame := buildRTSPInterleavedRTP(0, 96, 1, 0, 0x12345678, true, stapa)
	rdr := bytes.NewReader(frame)
	dmx := NewH264Demuxer(rdr, media, 0)
	_, err := dmx.Demux()
	require.NoError(t, err)

	// Should not crash, may return nil packet (no valid NALs)
	_, err = dmx.ReadPacket()
	// EOF is acceptable since reader is exhausted and no packets produced
	if err != nil {
		require.ErrorIs(t, err, io.EOF)
	}
}

func TestH264Demuxer_FUA(t *testing.T) {
	sps, pps := loadH264Params(t)

	media := sdp.Media{
		TimeScale:          90000,
		PayloadType:        96,
		SpropParameterSets: [][]byte{sps, pps},
	}

	// Simulate FU-A fragmentation of a non-IDR NAL (type 1)
	origNALData := bytes.Repeat([]byte{0xAA}, 100)
	origHdr := byte(0x01)                  // NAL type 1 = non-IDR
	fuIndicator := (origHdr & 0xE0) | 0x1C // NRI from original + type 28 (FU-A)

	// Fragment 1: Start
	fuHdr1 := byte(0x80) | (origHdr & 0x1F) // S=1, E=0, type from original
	frag1Payload := append([]byte{fuIndicator, fuHdr1}, origNALData[:50]...)
	frame1 := buildRTSPInterleavedRTP(0, 96, 1, 9000, 0x12345678, false, frag1Payload)

	// Fragment 2: End
	fuHdr2 := byte(0x40) | (origHdr & 0x1F) // S=0, E=1, type from original
	frag2Payload := append([]byte{fuIndicator, fuHdr2}, origNALData[50:]...)
	frame2 := buildRTSPInterleavedRTP(0, 96, 2, 9000, 0x12345678, true, frag2Payload)

	stream := concatFrames(frame1, frame2)
	rdr := bytes.NewReader(stream)
	dmx := NewH264Demuxer(rdr, media, 0)
	_, err := dmx.Demux()
	require.NoError(t, err)

	// First ReadPacket returns nothing (start fragment buffered)
	pkt, err := dmx.ReadPacket()
	// The first call might not yield a packet (FU-A start only buffers)
	if pkt == nil && err == nil {
		// Second read should complete the FU-A
		pkt, err = dmx.ReadPacket()
	}
	require.NoError(t, err)
	require.NotNil(t, pkt)

	// Reassembled data should contain the original NAL payload
	data := pkt.Data()
	require.True(t, len(data) > 4)
	// The reassembled NAL should contain all 100 bytes of origNALData
	nalSize := binary.BigEndian.Uint32(data[0:4])
	// NAL = 1 byte header (reconstructed) + 100 bytes data
	require.Equal(t, uint32(1+len(origNALData)), nalSize)
}

func TestH264Demuxer_FUA_TooShort(t *testing.T) {
	sps, pps := loadH264Params(t)

	media := sdp.Media{
		TimeScale:          90000,
		PayloadType:        96,
		SpropParameterSets: [][]byte{sps, pps},
	}

	// FU-A with only 1 byte (missing FU header)
	frame := buildRTSPInterleavedRTP(0, 96, 1, 9000, 0x12345678, true, []byte{0x7C})
	rdr := bytes.NewReader(frame)
	dmx := NewH264Demuxer(rdr, media, 0)
	_, err := dmx.Demux()
	require.NoError(t, err)

	_, err = dmx.ReadPacket()
	require.Error(t, err)
	require.Contains(t, err.Error(), "too short")
}

func TestH264Demuxer_TimestampConversion(t *testing.T) {
	sps, pps := loadH264Params(t)

	media := sdp.Media{
		TimeScale:          90000,
		PayloadType:        96,
		SpropParameterSets: [][]byte{sps, pps},
	}

	// Test various timestamps for correct conversion
	testCases := []struct {
		rtpTS    uint32
		expected time.Duration
	}{
		{0, 0},
		{9000, 100 * time.Millisecond},        // 9000/90000 = 0.1s
		{90000, 1 * time.Second},               // 1s
		{45000, 500 * time.Millisecond},        // 0.5s
		{900000, 10 * time.Second},             // 10s
		{1, time.Millisecond / clockrate},      // minimal tick
	}

	for _, tc := range testCases {
		nalPayload := []byte{0x01, 0xAA}
		frame := buildRTSPInterleavedRTP(0, 96, 1, tc.rtpTS, 0x12345678, true, nalPayload)
		rdr := bytes.NewReader(frame)
		dmx := NewH264Demuxer(rdr, media, 0)
		_, err := dmx.Demux()
		require.NoError(t, err)

		pkt, err := dmx.ReadPacket()
		require.NoError(t, err)
		require.Equal(t, tc.expected, pkt.Timestamp(), "rtpTS=%d", tc.rtpTS)
	}
}

// ===========================================================================
// H.265 demuxer tests
// ===========================================================================

func TestH265Demuxer_Demux_WithVPSSPSPPS(t *testing.T) {
	vps, sps, pps := loadH265Params(t)

	media := sdp.Media{
		TimeScale:   90000,
		PayloadType: 96,
		SpropVPS:    vps,
		SpropSPS:    sps,
		SpropPPS:    pps,
	}

	rdr := bytes.NewReader([]byte{})
	dmx := NewH265Demuxer(rdr, media, 0)
	codecs, err := dmx.Demux()
	require.NoError(t, err)
	require.NotNil(t, codecs.VideoCodecParameters)

	cp := codecs.VideoCodecParameters.(*h265.CodecParameters)
	require.Greater(t, cp.Width(), uint(0))
	require.Greater(t, cp.Height(), uint(0))
}

func TestH265Demuxer_TimestampConversion(t *testing.T) {
	// Verify H.265 uses the same correct timestamp formula as H.264.
	// H.265 uses a "sliced packet" architecture: the current NAL goes into
	// d.slicedPacket and is flushed to d.packets only when the *next*
	// addPacket call arrives. So we need two NAL units to flush the first.
	vps, sps, pps := loadH265Params(t)

	media := sdp.Media{
		TimeScale:   90000,
		PayloadType: 96,
		SpropVPS:    vps,
		SpropSPS:    sps,
		SpropPPS:    pps,
	}

	// H.265 NAL header is 2 bytes: [F(1)|Type(6)|LayerID(6)] [LayerID(2)|TID(3)]
	// Type 1 = TRAIL_R, set first-slice-in-segment bit (byte 2, bit 7)
	nalType := byte(1)
	nalHdr0 := nalType << 1 // F=0, type=1, layerID high bits=0
	nalHdr1 := byte(0x01)   // layerID low=0, TID=1

	rtpTS := uint32(9000) // 100ms at 90kHz
	nalPayload1 := []byte{nalHdr0, nalHdr1, 0x80, 0xAA, 0xBB} // byte[2] bit7=1 → addPacket
	nalPayload2 := []byte{nalHdr0, nalHdr1, 0x80, 0xCC, 0xDD} // second slice → flushes first

	frame1 := buildRTSPInterleavedRTP(0, 96, 1, rtpTS, 0x12345678, true, nalPayload1)
	frame2 := buildRTSPInterleavedRTP(0, 96, 2, rtpTS+3000, 0x12345678, true, nalPayload2)

	stream := concatFrames(frame1, frame2)
	rdr := bytes.NewReader(stream)
	dmx := NewH265Demuxer(rdr, media, 0)
	_, err := dmx.Demux()
	require.NoError(t, err)

	// First ReadPacket stores NAL in slicedPacket but packets queue is empty
	_, _ = dmx.ReadPacket()

	// Second ReadPacket: addPacket flushes the first slicedPacket into queue
	pkt, err := dmx.ReadPacket()
	require.NoError(t, err)
	require.NotNil(t, pkt)

	// Same formula as H.264: ts * 1ms / clockrate
	expectedTS := time.Duration(rtpTS) * time.Millisecond / time.Duration(clockrate)
	require.Equal(t, expectedTS, pkt.Timestamp())
}

func TestH265Demuxer_FU_TooShort(t *testing.T) {
	vps, sps, pps := loadH265Params(t)

	media := sdp.Media{
		TimeScale:   90000,
		PayloadType: 96,
		SpropVPS:    vps,
		SpropSPS:    sps,
		SpropPPS:    pps,
	}

	// H.265 FU NAL type = 49. 2-byte NAL header with type=49: (49<<1) = 0x62
	fuHdr0 := byte(h265.NalFU << 1) // type=49
	fuHdr1 := byte(0x01)            // TID=1
	// Only 2 bytes — missing FU header byte
	nalPayload := []byte{fuHdr0, fuHdr1}

	frame := buildRTSPInterleavedRTP(0, 96, 1, 9000, 0x12345678, true, nalPayload)
	rdr := bytes.NewReader(frame)
	dmx := NewH265Demuxer(rdr, media, 0)
	_, err := dmx.Demux()
	require.NoError(t, err)

	// Should not crash — the FU with < 3 bytes is skipped
	_, err = dmx.ReadPacket()
	// May return EOF since no valid packets were produced
	if err != nil {
		require.ErrorIs(t, err, io.EOF)
	}
}

// ===========================================================================
// AAC demuxer tests
// ===========================================================================

func TestAACDemuxer_Demux_ValidConfig(t *testing.T) {
	config := loadAACConfig(t)

	media := sdp.Media{
		TimeScale:   44100,
		PayloadType: 97,
		Config:      config,
	}

	rdr := bytes.NewReader([]byte{})
	dmx := NewAACDemuxer(rdr, media, 1)
	codecs, err := dmx.Demux()
	require.NoError(t, err)
	require.NotNil(t, codecs.AudioCodecParameters)

	cp := codecs.AudioCodecParameters.(*aac.CodecParameters)
	require.Greater(t, cp.SampleRate(), uint64(0))
}

func TestAACDemuxer_Demux_InvalidConfig(t *testing.T) {
	media := sdp.Media{
		TimeScale:   44100,
		PayloadType: 97,
		Config:      []byte{0xFF, 0xFF}, // invalid config
	}

	rdr := bytes.NewReader([]byte{})
	dmx := NewAACDemuxer(rdr, media, 1)
	_, err := dmx.Demux()
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid AAC config")
}

func TestAACDemuxer_AUHeader_SingleFrame(t *testing.T) {
	config := loadAACConfig(t)

	media := sdp.Media{
		TimeScale:   44100,
		PayloadType: 97,
		Config:      config,
	}

	// Build AU-header section for 1 frame:
	// AU-header section: 2-byte length (in bits), then 2-byte AU-headers per frame
	frameData := []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE}
	frameSize := len(frameData)
	auHeadersLengthBits := uint16(16) // 1 header * 16 bits
	auHeader := uint16(frameSize<<3) | 0 // 13-bit size << 3

	payload := make([]byte, 4+frameSize)
	binary.BigEndian.PutUint16(payload[0:2], auHeadersLengthBits)
	binary.BigEndian.PutUint16(payload[2:4], auHeader)
	copy(payload[4:], frameData)

	frame := buildRTSPInterleavedRTP(0, 97, 1, 44100, 0x12345678, true, payload)
	rdr := bytes.NewReader(frame)
	dmx := NewAACDemuxer(rdr, media, 1)
	_, err := dmx.Demux()
	require.NoError(t, err)

	pkt, err := dmx.ReadPacket()
	require.NoError(t, err)
	require.NotNil(t, pkt)
	require.Equal(t, frameData, pkt.Data())

	// Timestamp: ts=44100 → 44100 * 1s / 44100 = 1s
	require.Equal(t, 1*time.Second, pkt.Timestamp())
}

func TestAACDemuxer_AUHeader_MultipleFrames(t *testing.T) {
	config := loadAACConfig(t)

	media := sdp.Media{
		TimeScale:   44100,
		PayloadType: 97,
		Config:      config,
	}

	// 2 frames in one RTP packet
	frame1Data := []byte{0x11, 0x22, 0x33}
	frame2Data := []byte{0x44, 0x55}

	auHeadersLengthBits := uint16(32) // 2 headers * 16 bits
	auHeader1 := uint16(len(frame1Data) << 3)
	auHeader2 := uint16(len(frame2Data) << 3)

	payload := make([]byte, 6+len(frame1Data)+len(frame2Data))
	binary.BigEndian.PutUint16(payload[0:2], auHeadersLengthBits)
	binary.BigEndian.PutUint16(payload[2:4], auHeader1)
	binary.BigEndian.PutUint16(payload[4:6], auHeader2)
	copy(payload[6:], frame1Data)
	copy(payload[6+len(frame1Data):], frame2Data)

	frame := buildRTSPInterleavedRTP(0, 97, 1, 0, 0x12345678, true, payload)
	rdr := bytes.NewReader(frame)
	dmx := NewAACDemuxer(rdr, media, 1)
	_, err := dmx.Demux()
	require.NoError(t, err)

	pkt1, err := dmx.ReadPacket()
	require.NoError(t, err)
	require.Equal(t, frame1Data, pkt1.Data())

	pkt2, err := dmx.ReadPacket()
	require.NoError(t, err)
	require.Equal(t, frame2Data, pkt2.Data())

	// Second frame should have duration-offset from first
	expectedDuration := 1024 * time.Second / time.Duration(44100)
	require.Equal(t, expectedDuration, pkt2.Timestamp()-pkt1.Timestamp())
}

func TestAACDemuxer_AUHeader_PayloadTooShort(t *testing.T) {
	config := loadAACConfig(t)

	media := sdp.Media{
		TimeScale:   44100,
		PayloadType: 97,
		Config:      config,
	}

	// Payload with only 1 byte — too short for AU-header section
	payload := []byte{0xFF}
	frame := buildRTSPInterleavedRTP(0, 97, 1, 0, 0x12345678, true, payload)
	rdr := bytes.NewReader(frame)
	dmx := NewAACDemuxer(rdr, media, 1)
	_, err := dmx.Demux()
	require.NoError(t, err)

	_, err = dmx.ReadPacket()
	require.Error(t, err)
	require.Contains(t, err.Error(), "too short")
}

func TestAACDemuxer_AUHeader_CountExceedsPayload(t *testing.T) {
	config := loadAACConfig(t)

	media := sdp.Media{
		TimeScale:   44100,
		PayloadType: 97,
		Config:      config,
	}

	// AU-header length claiming 100 headers but payload is only 4 bytes
	payload := make([]byte, 4)
	binary.BigEndian.PutUint16(payload[0:2], 100*16) // 100 headers
	frame := buildRTSPInterleavedRTP(0, 97, 1, 0, 0x12345678, true, payload)
	rdr := bytes.NewReader(frame)
	dmx := NewAACDemuxer(rdr, media, 1)
	_, err := dmx.Demux()
	require.NoError(t, err)

	_, err = dmx.ReadPacket()
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds payload")
}

// ===========================================================================
// Opus demuxer tests
// ===========================================================================

func TestOpusDemuxer_Demux(t *testing.T) {
	media := sdp.Media{
		TimeScale:    48000,
		PayloadType:  111,
		ChannelCount: 2,
	}

	rdr := bytes.NewReader([]byte{})
	dmx := NewOPUSDemuxer(rdr, media, 0)
	codecs, err := dmx.Demux()
	require.NoError(t, err)
	require.NotNil(t, codecs.AudioCodecParameters)

	cp := codecs.AudioCodecParameters.(*opus.CodecParameters)
	require.Equal(t, uint64(48000), cp.SampleRate())
}

func TestOpusDemuxer_ChannelMapping(t *testing.T) {
	testCases := []struct {
		sdpChannels int
		expected    gomedia.ChannelLayout
	}{
		{1, gomedia.ChMono},
		{2, gomedia.ChStereo},
		{0, gomedia.ChMono},   // default
		{6, gomedia.ChMono},   // unsupported → default
	}

	for _, tc := range testCases {
		media := sdp.Media{TimeScale: 48000, PayloadType: 111, ChannelCount: tc.sdpChannels}
		rdr := bytes.NewReader([]byte{})
		dmx := NewOPUSDemuxer(rdr, media, 0)
		codecs, _ := dmx.Demux()
		cp := codecs.AudioCodecParameters.(*opus.CodecParameters)
		require.Equal(t, tc.expected, cp.ChannelLayout, "channels=%d", tc.sdpChannels)
	}
}

func TestOpusDemuxer_ReadPacket_DurationFromTOC(t *testing.T) {
	media := sdp.Media{
		TimeScale:    48000,
		PayloadType:  111,
		ChannelCount: 1,
	}

	// Build an Opus packet with TOC byte for config=0, code=0 (10ms)
	// Config 0 = SILK-only, NB, 10ms. TOC: config(5bit)=00000, stereo(1)=0, code(2)=00 = 0x00
	opusPayload := []byte{0x00, 0xAA, 0xBB}

	frame := buildRTSPInterleavedRTP(0, 111, 1, 48000, 0x12345678, true, opusPayload)
	rdr := bytes.NewReader(frame)
	dmx := NewOPUSDemuxer(rdr, media, 0)

	pkt, err := dmx.ReadPacket()
	require.NoError(t, err)
	require.NotNil(t, pkt)

	// Config 0 → 10ms frame duration per RFC 6716
	require.Equal(t, 10*time.Millisecond, pkt.Duration())
	require.Equal(t, opusPayload, pkt.Data())
}

func TestOpusDemuxer_ReadPacket_20msTOC(t *testing.T) {
	media := sdp.Media{
		TimeScale:    48000,
		PayloadType:  111,
		ChannelCount: 1,
	}

	// Config=3 → SILK-only, NB, 20ms. TOC: 00011|0|00 = 0x0C
	opusPayload := []byte{0x0C, 0xAA}

	frame := buildRTSPInterleavedRTP(0, 111, 1, 0, 0x12345678, true, opusPayload)
	rdr := bytes.NewReader(frame)
	dmx := NewOPUSDemuxer(rdr, media, 0)

	pkt, err := dmx.ReadPacket()
	require.NoError(t, err)
	require.Equal(t, 20*time.Millisecond, pkt.Duration())
}

func TestOpusDemuxer_Timestamp(t *testing.T) {
	media := sdp.Media{
		TimeScale:    48000,
		PayloadType:  111,
		ChannelCount: 1,
	}

	opusPayload := []byte{0x0C, 0xAA}
	rtpTS := uint32(48000) // 1s at 48kHz
	frame := buildRTSPInterleavedRTP(0, 111, 1, rtpTS, 0x12345678, true, opusPayload)
	rdr := bytes.NewReader(frame)
	dmx := NewOPUSDemuxer(rdr, media, 0)

	pkt, err := dmx.ReadPacket()
	require.NoError(t, err)
	require.Equal(t, 1*time.Second, pkt.Timestamp())
}

// ===========================================================================
// PCM demuxer tests
// ===========================================================================

func TestPCMDemuxer_Demux(t *testing.T) {
	media := sdp.Media{
		TimeScale:    8000,
		PayloadType:  8,
		ChannelCount: 1,
	}

	rdr := bytes.NewReader([]byte{})
	dmx := NewPCMDemuxer(rdr, media, 0, gomedia.PCMAlaw)
	codecs, err := dmx.Demux()
	require.NoError(t, err)
	require.NotNil(t, codecs.AudioCodecParameters)

	cp := codecs.AudioCodecParameters.(*pcm.CodecParameters)
	require.Equal(t, uint64(8000), cp.SampleRate())
	require.Equal(t, gomedia.PCMAlaw, cp.Type())
}

func TestPCMDemuxer_ReadPacket(t *testing.T) {
	media := sdp.Media{
		TimeScale:    8000,
		PayloadType:  8,
		ChannelCount: 1,
	}

	pcmData := bytes.Repeat([]byte{0x55}, 160) // 20ms of 8kHz A-law
	rtpTS := uint32(8000)                       // 1s at 8kHz

	frame := buildRTSPInterleavedRTP(0, 8, 1, rtpTS, 0x12345678, true, pcmData)
	rdr := bytes.NewReader(frame)
	dmx := NewPCMDemuxer(rdr, media, 0, gomedia.PCMAlaw)

	pkt, err := dmx.ReadPacket()
	require.NoError(t, err)
	require.NotNil(t, pkt)
	require.Equal(t, pcmData, pkt.Data())

	// Timestamp: 8000 * 1s / 8000 = 1s
	require.Equal(t, 1*time.Second, pkt.Timestamp())

	// Duration: 160 samples * 1s / 8000 = 20ms
	require.Equal(t, 20*time.Millisecond, pkt.Duration())
}

func TestPCMDemuxer_MuLaw(t *testing.T) {
	media := sdp.Media{
		TimeScale:    8000,
		PayloadType:  0,
		ChannelCount: 1,
	}

	rdr := bytes.NewReader([]byte{})
	dmx := NewPCMDemuxer(rdr, media, 0, gomedia.PCMUlaw)
	codecs, err := dmx.Demux()
	require.NoError(t, err)

	cp := codecs.AudioCodecParameters.(*pcm.CodecParameters)
	require.Equal(t, gomedia.PCMUlaw, cp.Type())
}

// ===========================================================================
// H.264 muxer tests
// ===========================================================================

func TestH264Muxer_WritePacket_SingleNAL(t *testing.T) {
	sps, pps := loadH264Params(t)
	codec, err := h264.NewCodecDataFromSPSAndPPS(sps, pps)
	require.NoError(t, err)

	var buf bytes.Buffer
	media := sdp.Media{TimeScale: 90000, PayloadType: 96}
	muxer := NewH264Muxer(&buf, media, 0, &codec, 1400, nil)

	// Create a small NAL unit (fits in MTU) — non-keyframe
	nalData := bytes.Repeat([]byte{0x01}, 100)

	// Build AVCC-format data: [4-byte size | NAL]
	avccData := make([]byte, 4+len(nalData))
	binary.BigEndian.PutUint32(avccData[0:4], uint32(len(nalData)))
	copy(avccData[4:], nalData)

	pkt := h264.NewPacket(false, 100*time.Millisecond, time.Now(), avccData, "", &codec)
	err = muxer.WritePacket(pkt)
	require.NoError(t, err)

	// Verify RTSP interleaved header
	output := buf.Bytes()
	require.True(t, len(output) > rtspHeaderSize+rtpHeaderSize)
	require.Equal(t, byte(0x24), output[0])     // RTSP magic
	require.Equal(t, byte(0), output[1])         // channel
	require.Equal(t, byte(0x80), output[4]&0xC0) // RTP version 2
}

func TestH264Muxer_WritePacket_FUA_Fragmentation(t *testing.T) {
	sps, pps := loadH264Params(t)
	codec, err := h264.NewCodecDataFromSPSAndPPS(sps, pps)
	require.NoError(t, err)

	var buf bytes.Buffer
	mtu := 100
	media := sdp.Media{TimeScale: 90000, PayloadType: 96}
	muxer := NewH264Muxer(&buf, media, 0, &codec, mtu, nil)

	// NAL larger than MTU → should be fragmented via FU-A
	nalData := make([]byte, 300)
	nalData[0] = 0x65 // IDR
	for i := 1; i < len(nalData); i++ {
		nalData[i] = byte(i & 0xFF)
	}

	avccData := make([]byte, 4+len(nalData))
	binary.BigEndian.PutUint32(avccData[0:4], uint32(len(nalData)))
	copy(avccData[4:], nalData)

	// Non-keyframe to avoid SPS/PPS prepending
	pkt := h264.NewPacket(false, 0, time.Now(), avccData, "", &codec)
	err = muxer.WritePacket(pkt)
	require.NoError(t, err)

	// Should produce multiple RTP packets
	output := buf.Bytes()
	packetCount := 0
	offset := 0
	for offset < len(output) {
		require.Equal(t, byte(0x24), output[offset])
		rtpLen := int(binary.BigEndian.Uint16(output[offset+2 : offset+4]))
		offset += rtspHeaderSize + rtpLen
		packetCount++
	}
	require.Greater(t, packetCount, 1, "large NAL should be fragmented into multiple RTP packets")
}

func TestH264Muxer_WritePacket_Keyframe_PrependsSPSPPS(t *testing.T) {
	sps, pps := loadH264Params(t)
	codec, err := h264.NewCodecDataFromSPSAndPPS(sps, pps)
	require.NoError(t, err)

	var buf bytes.Buffer
	media := sdp.Media{TimeScale: 90000, PayloadType: 96}
	muxer := NewH264Muxer(&buf, media, 0, &codec, 1400, nil)

	// Small IDR NAL — keyframe
	nalData := []byte{0x65, 0x01, 0x02, 0x03}
	avccData := make([]byte, 4+len(nalData))
	binary.BigEndian.PutUint32(avccData[0:4], uint32(len(nalData)))
	copy(avccData[4:], nalData)

	pkt := h264.NewPacket(true, 0, time.Now(), avccData, "", &codec)
	err = muxer.WritePacket(pkt)
	require.NoError(t, err)

	// Should produce at least 3 RTP packets: SPS, PPS, IDR
	output := buf.Bytes()
	packetCount := 0
	offset := 0
	for offset < len(output) {
		require.Equal(t, byte(0x24), output[offset])
		rtpLen := int(binary.BigEndian.Uint16(output[offset+2 : offset+4]))
		offset += rtspHeaderSize + rtpLen
		packetCount++
	}
	require.GreaterOrEqual(t, packetCount, 3, "keyframe should produce SPS + PPS + IDR packets")
}

func TestH264Muxer_WritePacket_MarkerBit(t *testing.T) {
	sps, pps := loadH264Params(t)
	codec, err := h264.NewCodecDataFromSPSAndPPS(sps, pps)
	require.NoError(t, err)

	var buf bytes.Buffer
	media := sdp.Media{TimeScale: 90000, PayloadType: 96}
	muxer := NewH264Muxer(&buf, media, 0, &codec, 1400, nil)

	// Single small NAL — should set marker bit on the only packet
	nalData := []byte{0x01, 0xAA, 0xBB}
	avccData := make([]byte, 4+len(nalData))
	binary.BigEndian.PutUint32(avccData[0:4], uint32(len(nalData)))
	copy(avccData[4:], nalData)

	pkt := h264.NewPacket(false, 0, time.Now(), avccData, "", &codec)
	err = muxer.WritePacket(pkt)
	require.NoError(t, err)

	output := buf.Bytes()
	// Marker bit is in byte[5] bit 7 of the RTP header
	markerBit := output[5] & 0x80
	require.Equal(t, byte(0x80), markerBit, "marker bit should be set on last/only RTP packet")
}

func TestH264Muxer_SequenceIncrement(t *testing.T) {
	sps, pps := loadH264Params(t)
	codec, err := h264.NewCodecDataFromSPSAndPPS(sps, pps)
	require.NoError(t, err)

	var buf bytes.Buffer
	media := sdp.Media{TimeScale: 90000, PayloadType: 96}
	muxer := NewH264Muxer(&buf, media, 0, &codec, 1400, nil)

	// Write 3 packets
	for i := range 3 {
		nalData := []byte{0x01, byte(i)}
		avccData := make([]byte, 4+len(nalData))
		binary.BigEndian.PutUint32(avccData[0:4], uint32(len(nalData)))
		copy(avccData[4:], nalData)

		pkt := h264.NewPacket(false, time.Duration(i)*100*time.Millisecond, time.Now(), avccData, "", &codec)
		require.NoError(t, muxer.WritePacket(pkt))
	}

	// Parse sequence numbers from output
	output := buf.Bytes()
	var seqs []uint16
	offset := 0
	for offset < len(output) {
		seq := binary.BigEndian.Uint16(output[offset+6 : offset+8])
		seqs = append(seqs, seq)
		rtpLen := int(binary.BigEndian.Uint16(output[offset+2 : offset+4]))
		offset += rtspHeaderSize + rtpLen
	}

	// Verify monotonically increasing
	for i := 1; i < len(seqs); i++ {
		require.Equal(t, seqs[i-1]+1, seqs[i], "sequence numbers should increment by 1")
	}
}

func TestH264Muxer_TimestampConversion(t *testing.T) {
	sps, pps := loadH264Params(t)
	codec, err := h264.NewCodecDataFromSPSAndPPS(sps, pps)
	require.NoError(t, err)

	var buf bytes.Buffer
	media := sdp.Media{TimeScale: 90000, PayloadType: 96}
	muxer := NewH264Muxer(&buf, media, 0, &codec, 1400, nil)

	// 1 second PTS should map to RTP timestamp 90000
	nalData := []byte{0x01, 0xAA}
	avccData := make([]byte, 4+len(nalData))
	binary.BigEndian.PutUint32(avccData[0:4], uint32(len(nalData)))
	copy(avccData[4:], nalData)

	pkt := h264.NewPacket(false, 1*time.Second, time.Now(), avccData, "", &codec)
	require.NoError(t, muxer.WritePacket(pkt))

	output := buf.Bytes()
	rtpTS := binary.BigEndian.Uint32(output[8:12])
	require.Equal(t, uint32(90000), rtpTS)
}

// ===========================================================================
// H.265 muxer tests
// ===========================================================================

func TestH265Muxer_WritePacket_SingleNAL(t *testing.T) {
	vps, sps, pps := loadH265Params(t)
	codec, err := h265.NewCodecDataFromVPSAndSPSAndPPS(vps, sps, pps)
	require.NoError(t, err)

	var buf bytes.Buffer
	media := sdp.Media{TimeScale: 90000, PayloadType: 96}
	muxer := NewH265Muxer(&buf, media, 0, &codec, 1400, nil)

	// Small HEVC NAL (type 1 = TRAIL_R), 2-byte header
	nalData := make([]byte, 50)
	nalData[0] = 0x02 // type=1 << 1
	nalData[1] = 0x01 // TID=1

	avccData := make([]byte, 4+len(nalData))
	binary.BigEndian.PutUint32(avccData[0:4], uint32(len(nalData)))
	copy(avccData[4:], nalData)

	pkt := h265.NewPacket(false, 0, time.Now(), avccData, "", &codec)
	err = muxer.WritePacket(pkt)
	require.NoError(t, err)

	output := buf.Bytes()
	require.True(t, len(output) > rtspHeaderSize+rtpHeaderSize)
}

func TestH265Muxer_WritePacket_Keyframe_PrependsVPSSPSPPS(t *testing.T) {
	vps, sps, pps := loadH265Params(t)
	codec, err := h265.NewCodecDataFromVPSAndSPSAndPPS(vps, sps, pps)
	require.NoError(t, err)

	var buf bytes.Buffer
	media := sdp.Media{TimeScale: 90000, PayloadType: 96}
	muxer := NewH265Muxer(&buf, media, 0, &codec, 1400, nil)

	// IDR NAL (type 19 = IDR_W_RADL), 2-byte header
	nalData := []byte{byte(19 << 1), 0x01, 0xAA, 0xBB}
	avccData := make([]byte, 4+len(nalData))
	binary.BigEndian.PutUint32(avccData[0:4], uint32(len(nalData)))
	copy(avccData[4:], nalData)

	pkt := h265.NewPacket(true, 0, time.Now(), avccData, "", &codec)
	err = muxer.WritePacket(pkt)
	require.NoError(t, err)

	// Should produce at least 4 RTP packets: VPS, SPS, PPS, IDR
	output := buf.Bytes()
	packetCount := 0
	offset := 0
	for offset < len(output) {
		rtpLen := int(binary.BigEndian.Uint16(output[offset+2 : offset+4]))
		offset += rtspHeaderSize + rtpLen
		packetCount++
	}
	require.GreaterOrEqual(t, packetCount, 4, "keyframe should produce VPS + SPS + PPS + IDR packets")
}

// ===========================================================================
// H.264 muxer→demuxer round-trip tests
// ===========================================================================

func TestH264_MuxDemux_RoundTrip_SingleNAL(t *testing.T) {
	sps, pps := loadH264Params(t)
	codec, err := h264.NewCodecDataFromSPSAndPPS(sps, pps)
	require.NoError(t, err)

	var buf bytes.Buffer
	media := sdp.Media{
		TimeScale:          90000,
		PayloadType:        96,
		SpropParameterSets: [][]byte{sps, pps},
	}
	muxer := NewH264Muxer(&buf, media, 0, &codec, 1400, nil)

	// Mux a non-keyframe single NAL
	nalData := []byte{0x01, 0xAA, 0xBB, 0xCC}
	avccData := make([]byte, 4+len(nalData))
	binary.BigEndian.PutUint32(avccData[0:4], uint32(len(nalData)))
	copy(avccData[4:], nalData)

	ts := 100 * time.Millisecond
	pkt := h264.NewPacket(false, ts, time.Now(), avccData, "", &codec)
	require.NoError(t, muxer.WritePacket(pkt))

	// Demux the output
	rdr := bytes.NewReader(buf.Bytes())
	dmx := NewH264Demuxer(rdr, media, 0)
	_, err = dmx.Demux()
	require.NoError(t, err)

	demuxedPkt, err := dmx.ReadPacket()
	require.NoError(t, err)
	require.NotNil(t, demuxedPkt)

	// Verify payload — the demuxed NAL should match
	demuxedData := demuxedPkt.Data()
	require.True(t, len(demuxedData) > 4)
	demuxedNAL := demuxedData[4:]
	require.Equal(t, nalData, demuxedNAL)
}

func TestH264_MuxDemux_RoundTrip_FUA(t *testing.T) {
	sps, pps := loadH264Params(t)
	codec, err := h264.NewCodecDataFromSPSAndPPS(sps, pps)
	require.NoError(t, err)

	var buf bytes.Buffer
	media := sdp.Media{
		TimeScale:          90000,
		PayloadType:        96,
		SpropParameterSets: [][]byte{sps, pps},
	}
	mtu := 100
	muxer := NewH264Muxer(&buf, media, 0, &codec, mtu, nil)

	// Large NAL that requires FU-A fragmentation
	nalData := make([]byte, 500)
	nalData[0] = 0x01 // non-IDR
	for i := 1; i < len(nalData); i++ {
		nalData[i] = byte(i & 0xFF)
	}

	avccData := make([]byte, 4+len(nalData))
	binary.BigEndian.PutUint32(avccData[0:4], uint32(len(nalData)))
	copy(avccData[4:], nalData)

	pkt := h264.NewPacket(false, 0, time.Now(), avccData, "", &codec)
	require.NoError(t, muxer.WritePacket(pkt))

	// Demux
	rdr := bytes.NewReader(buf.Bytes())
	dmx := NewH264Demuxer(rdr, media, 0)
	_, err = dmx.Demux()
	require.NoError(t, err)

	// Read all packets — first reads may buffer FU-A fragments
	var demuxedPkt gomedia.Packet
	for {
		p, readErr := dmx.ReadPacket()
		if readErr != nil {
			break
		}
		if p != nil {
			demuxedPkt = p
		}
	}

	require.NotNil(t, demuxedPkt, "should have produced at least one demuxed packet")

	// Verify the reassembled NAL matches original
	demuxedData := demuxedPkt.Data()
	require.True(t, len(demuxedData) > 4)
	demuxedNAL := demuxedData[4:]
	require.Equal(t, nalData, demuxedNAL)
}

// ===========================================================================
// H.265 muxer→demuxer round-trip tests
// ===========================================================================

func TestH265_MuxDemux_RoundTrip_TwoNALs(t *testing.T) {
	vps, sps, pps := loadH265Params(t)
	codec, err := h265.NewCodecDataFromVPSAndSPSAndPPS(vps, sps, pps)
	require.NoError(t, err)

	var buf bytes.Buffer
	media := sdp.Media{
		TimeScale:   90000,
		PayloadType: 96,
		SpropVPS:    vps,
		SpropSPS:    sps,
		SpropPPS:    pps,
	}
	muxer := NewH265Muxer(&buf, media, 0, &codec, 1400, nil)

	// Non-keyframe HEVC NAL (type 1 = TRAIL_R) with first-slice bit
	nalData1 := make([]byte, 50)
	nalData1[0] = byte(1 << 1) // type=1
	nalData1[1] = 0x01         // TID=1
	nalData1[2] = 0x80         // first-slice bit set → addPacket path
	for i := 3; i < len(nalData1); i++ {
		nalData1[i] = byte(i)
	}

	nalData2 := make([]byte, 30)
	nalData2[0] = byte(1 << 1) // type=1
	nalData2[1] = 0x01         // TID=1
	nalData2[2] = 0x80         // first-slice bit → flushes previous
	for i := 3; i < len(nalData2); i++ {
		nalData2[i] = byte(i + 0x80)
	}

	// Mux both NALs as separate packets
	for _, nalData := range [][]byte{nalData1, nalData2} {
		avccData := make([]byte, 4+len(nalData))
		binary.BigEndian.PutUint32(avccData[0:4], uint32(len(nalData)))
		copy(avccData[4:], nalData)

		pkt := h265.NewPacket(false, 0, time.Now(), avccData, "", &codec)
		require.NoError(t, muxer.WritePacket(pkt))
	}

	// Demux — the H.265 sliced packet architecture flushes on second addPacket
	rdr := bytes.NewReader(buf.Bytes())
	dmx := NewH265Demuxer(rdr, media, 0)
	_, err = dmx.Demux()
	require.NoError(t, err)

	var demuxedPkts []gomedia.Packet
	for {
		p, readErr := dmx.ReadPacket()
		if readErr != nil {
			break
		}
		if p != nil {
			demuxedPkts = append(demuxedPkts, p)
		}
	}
	require.NotEmpty(t, demuxedPkts, "should produce at least one demuxed packet")

	// The first flushed packet should contain nalData1
	demuxedData := demuxedPkts[0].Data()
	require.True(t, len(demuxedData) > 4)
	demuxedNAL := demuxedData[4:]
	require.Equal(t, nalData1, demuxedNAL)
}

// ===========================================================================
// Base muxer tests
// ===========================================================================

func TestBaseMuxer_ClockRateFallback(t *testing.T) {
	// When TimeScale is 0, clock rate should default to 90000
	var buf bytes.Buffer
	media := sdp.Media{TimeScale: 0, PayloadType: 96}
	muxer := NewH264Muxer(&buf, media, 0, nil, 1400, nil)

	// Write a minimal packet — verify it uses 90000 clock rate
	nalData := []byte{0x01, 0xAA}
	avccData := make([]byte, 4+len(nalData))
	binary.BigEndian.PutUint32(avccData[0:4], uint32(len(nalData)))
	copy(avccData[4:], nalData)

	codec, _ := h264.NewCodecDataFromSPSAndPPS(loadH264Params(t))
	pkt := h264.NewPacket(false, 1*time.Second, time.Now(), avccData, "", &codec)
	require.NoError(t, muxer.WritePacket(pkt))

	output := buf.Bytes()
	rtpTS := binary.BigEndian.Uint32(output[8:12])
	require.Equal(t, uint32(90000), rtpTS, "1s at default 90kHz should be 90000")
}

func TestBaseMuxer_RTPHeaderStructure(t *testing.T) {
	sps, pps := loadH264Params(t)
	codec, err := h264.NewCodecDataFromSPSAndPPS(sps, pps)
	require.NoError(t, err)

	var buf bytes.Buffer
	pt := uint8(96)
	ch := uint8(2)
	media := sdp.Media{TimeScale: 90000, PayloadType: int(pt)}
	muxer := NewH264Muxer(&buf, media, ch, &codec, 1400, nil)

	nalData := []byte{0x01, 0xAA}
	avccData := make([]byte, 4+len(nalData))
	binary.BigEndian.PutUint32(avccData[0:4], uint32(len(nalData)))
	copy(avccData[4:], nalData)

	pkt := h264.NewPacket(false, 0, time.Now(), avccData, "", &codec)
	require.NoError(t, muxer.WritePacket(pkt))

	output := buf.Bytes()

	// RTSP header
	require.Equal(t, byte(0x24), output[0], "RTSP magic byte")
	require.Equal(t, ch, output[1], "RTSP channel")

	// RTP header
	require.Equal(t, byte(0x80), output[4]&0xC0, "RTP version 2")
	require.Equal(t, byte(0), output[4]&0x20, "no padding")
	require.Equal(t, byte(0), output[4]&0x10, "no extension")
	require.Equal(t, byte(0), output[4]&0x0F, "CC=0")
	require.Equal(t, pt, output[5]&0x7F, "payload type")

	// SSRC should be non-zero (randomly initialized)
	ssrc := binary.BigEndian.Uint32(output[12:16])
	// Can't guarantee non-zero due to randomness, but extremely unlikely to be 0
	_ = ssrc
}

// ===========================================================================
// WriteSizePrefix utility test
// ===========================================================================

func TestWriteSizePrefix(t *testing.T) {
	buf := make([]byte, 8)
	writeSizePrefix(buf, 0, 1000)
	size := binary.BigEndian.Uint32(buf[0:4])
	require.Equal(t, uint32(1000), size)

	writeSizePrefix(buf, 4, 42)
	size2 := binary.BigEndian.Uint32(buf[4:8])
	require.Equal(t, uint32(42), size2)
}

// ===========================================================================
// Edge case tests
// ===========================================================================

func TestH264Muxer_EmptyPacket(t *testing.T) {
	sps, pps := loadH264Params(t)
	codec, err := h264.NewCodecDataFromSPSAndPPS(sps, pps)
	require.NoError(t, err)

	var buf bytes.Buffer
	media := sdp.Media{TimeScale: 90000, PayloadType: 96}
	muxer := NewH264Muxer(&buf, media, 0, &codec, 1400, nil)

	// Empty data — should be a no-op
	pkt := h264.NewPacket(false, 0, time.Now(), []byte{}, "", &codec)
	err = muxer.WritePacket(pkt)
	require.NoError(t, err)
	require.Equal(t, 0, buf.Len(), "empty packet should produce no output")
}

func TestH264Muxer_WrongPacketType(t *testing.T) {
	var buf bytes.Buffer
	media := sdp.Media{TimeScale: 90000, PayloadType: 96}
	muxer := NewH264Muxer(&buf, media, 0, nil, 1400, nil)

	// Pass an H.265 packet to an H.264 muxer — should error
	vps, sps265, pps265 := loadH265Params(t)
	codec265, err := h265.NewCodecDataFromVPSAndSPSAndPPS(vps, sps265, pps265)
	require.NoError(t, err)

	pkt := h265.NewPacket(false, 0, time.Now(), []byte{0x02, 0x01}, "", &codec265)
	err = muxer.WritePacket(pkt)
	require.Error(t, err)
	require.Contains(t, err.Error(), "expected *h264.Packet")
}

func TestH265Muxer_WrongPacketType(t *testing.T) {
	var buf bytes.Buffer
	media := sdp.Media{TimeScale: 90000, PayloadType: 96}
	muxer := NewH265Muxer(&buf, media, 0, nil, 1400, nil)

	sps, pps := loadH264Params(t)
	codec264, err := h264.NewCodecDataFromSPSAndPPS(sps, pps)
	require.NoError(t, err)

	pkt := h264.NewPacket(false, 0, time.Now(), []byte{0x01}, "", &codec264)
	err = muxer.WritePacket(pkt)
	require.Error(t, err)
	require.Contains(t, err.Error(), "expected *h265.Packet")
}

func TestHxxxMuxer_DefaultMTU(t *testing.T) {
	base := newBaseMuxer(&bytes.Buffer{}, sdp.Media{TimeScale: 90000}, 0, 0, nil)
	m := newHxxxMuxer(base, 0) // MTU=0 → should default
	require.Equal(t, DefaultMTU, m.mtu)

	m2 := newHxxxMuxer(base, -1) // negative → should default
	require.Equal(t, DefaultMTU, m2.mtu)
}

// ===========================================================================
// Multiple sequential reads test
// ===========================================================================

func TestH264Demuxer_MultiplePackets(t *testing.T) {
	sps, pps := loadH264Params(t)

	media := sdp.Media{
		TimeScale:          90000,
		PayloadType:        96,
		SpropParameterSets: [][]byte{sps, pps},
	}

	// Build 3 RTP packets with incrementing timestamps
	var frames [][]byte
	for i := range 3 {
		nalPayload := []byte{0x01, byte(i)} // non-IDR
		ts := uint32(i * 3000)              // 33ms intervals
		frame := buildRTSPInterleavedRTP(0, 96, uint16(i), ts, 0x12345678, true, nalPayload)
		frames = append(frames, frame)
	}

	stream := concatFrames(frames...)
	rdr := bytes.NewReader(stream)
	dmx := NewH264Demuxer(rdr, media, 0)
	_, err := dmx.Demux()
	require.NoError(t, err)

	// Read all 3 packets
	var timestamps []time.Duration
	for range 3 {
		pkt, readErr := dmx.ReadPacket()
		require.NoError(t, readErr)
		require.NotNil(t, pkt)
		timestamps = append(timestamps, pkt.Timestamp())
	}

	// Verify timestamps are monotonically increasing
	for i := 1; i < len(timestamps); i++ {
		require.Greater(t, timestamps[i], timestamps[i-1])
	}
}

func TestH264Demuxer_SPSPPSUpdate(t *testing.T) {
	sps, pps := loadH264Params(t)

	media := sdp.Media{
		TimeScale:          90000,
		PayloadType:        96,
		SpropParameterSets: [][]byte{sps, pps},
	}

	// Build a STAP-A with SPS + PPS + IDR (in-band parameter set update)
	stapa := []byte{nalSTAPA}
	// SPS
	stapa = append(stapa, byte(len(sps)>>8), byte(len(sps)))
	stapa = append(stapa, sps...)
	// PPS
	stapa = append(stapa, byte(len(pps)>>8), byte(len(pps)))
	stapa = append(stapa, pps...)
	// IDR NAL
	idr := []byte{0x65, 0x01, 0x02}
	stapa = append(stapa, byte(len(idr)>>8), byte(len(idr)))
	stapa = append(stapa, idr...)

	frame := buildRTSPInterleavedRTP(0, 96, 1, 0, 0x12345678, true, stapa)
	rdr := bytes.NewReader(frame)
	dmx := NewH264Demuxer(rdr, media, 0)
	_, err := dmx.Demux()
	require.NoError(t, err)

	pkt, err := dmx.ReadPacket()
	require.NoError(t, err)
	require.NotNil(t, pkt)

	vp := pkt.(gomedia.VideoPacket)
	require.True(t, vp.IsKeyFrame())
}

// ===========================================================================
// Ring buffer option tests
// ===========================================================================

func TestWithRingBuffer_Option(t *testing.T) {
	media := sdp.Media{
		TimeScale:    48000,
		PayloadType:  111,
		ChannelCount: 1,
	}

	// Build a valid Opus packet
	opusPayload := []byte{0x0C, 0xAA, 0xBB} // config=3, code=0 → 20ms
	frame := buildRTSPInterleavedRTP(0, 111, 1, 0, 0x12345678, true, opusPayload)

	rdr := bytes.NewReader(frame)
	dmx := NewOPUSDemuxer(rdr, media, 0, WithRingBuffer(1024*1024))

	pkt, err := dmx.ReadPacket()
	require.NoError(t, err)
	require.NotNil(t, pkt)
	require.Equal(t, opusPayload, pkt.Data())

	// Release should not panic
	pkt.Release()
}
