package sdp

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ugparu/gomedia"
)

// ---------------------------------------------------------------------------
// Parse — real-world SDP examples
// ---------------------------------------------------------------------------

func TestParse_FullRTSPSessionH264AAC(t *testing.T) {
	sdpContent := strings.Join([]string{
		"v=0",
		"o=- 0 0 IN IP4 192.168.1.100",
		"s=RTSP Session",
		"u=rtsp://192.168.1.100/live",
		"t=0 0",
		"m=video 0 RTP/AVP 96",
		"a=rtpmap:96 H264/90000",
		"a=fmtp:96 sprop-parameter-sets=Z0IAKeKQFoA=,aM4xsg==;packetization-mode=1",
		"a=control:trackID=0",
		"a=x-framerate:30",
		"a=x-dimensions:1920,1080",
		"m=audio 0 RTP/AVP 97",
		"a=rtpmap:97 MPEG4-GENERIC/44100/2",
		"a=fmtp:97 config=1210;sizelength=13;indexlength=3",
		"a=control:trackID=1",
	}, "\r\n")

	sess, medias := Parse(sdpContent)

	assert.Equal(t, "rtsp://192.168.1.100/live", sess.URI)
	require.Equal(t, 2, len(medias))

	// Video
	v := medias[0]
	assert.Equal(t, "video", v.AVType)
	assert.Equal(t, gomedia.H264, v.Type)
	assert.Equal(t, 96, v.PayloadType)
	assert.Equal(t, 90000, v.TimeScale)
	assert.Equal(t, "trackID=0", v.Control)
	assert.Equal(t, 30, v.FPS)
	assert.Equal(t, 1920, v.Width)
	assert.Equal(t, 1080, v.Height)
	require.Equal(t, 2, len(v.SpropParameterSets))

	// Audio
	a := medias[1]
	assert.Equal(t, "audio", a.AVType)
	assert.Equal(t, gomedia.AAC, a.Type)
	assert.Equal(t, 97, a.PayloadType)
	assert.Equal(t, 44100, a.TimeScale)
	// Note: parser only sets ChannelCount for OPUS/PCMA/PCMU, not AAC.
	assert.Equal(t, []byte{0x12, 0x10}, a.Config)
	assert.Equal(t, 13, a.SizeLength)
	assert.Equal(t, 3, a.IndexLength)
	assert.Equal(t, "trackID=1", a.Control)
}

func TestParse_H265WithSpropVPSSPSPPS(t *testing.T) {
	sdpContent := strings.Join([]string{
		"v=0",
		"o=- 0 0 IN IP4 0.0.0.0",
		"s=Session",
		"t=0 0",
		"m=video 0 RTP/AVP 96",
		"a=rtpmap:96 H265/90000",
		"a=fmtp:96 sprop-vps=QAEMAf//AWAAAAMAAAMAAAMAAAMAlqwJ;sprop-sps=QgEBAWAAAAMAAAMAAAMAAAMAlqADwIAo;sprop-pps=RAHA8vA8",
		"a=control:trackID=0",
	}, "\r\n")

	_, medias := Parse(sdpContent)
	require.Equal(t, 1, len(medias))

	m := medias[0]
	assert.Equal(t, gomedia.H265, m.Type)
	assert.Equal(t, 90000, m.TimeScale)
	assert.NotEmpty(t, m.SpropVPS)
	assert.NotEmpty(t, m.SpropSPS)
	assert.NotEmpty(t, m.SpropPPS)
}

// ---------------------------------------------------------------------------
// Parse — codec type detection via rtpmap
// ---------------------------------------------------------------------------

func TestParse_CodecTypeDetection(t *testing.T) {
	tests := []struct {
		name     string
		rtpmap   string
		wantType gomedia.CodecType
	}{
		{"H264", "a=rtpmap:96 H264/90000", gomedia.H264},
		{"H265", "a=rtpmap:96 H265/90000", gomedia.H265},
		{"HEVC", "a=rtpmap:96 HEVC/90000", gomedia.H265},
		{"AAC", "a=rtpmap:97 MPEG4-GENERIC/44100/2", gomedia.AAC},
		{"OPUS", "a=rtpmap:111 OPUS/48000/2", gomedia.OPUS},
		{"PCMA", "a=rtpmap:8 PCMA/8000", gomedia.PCMAlaw},
		{"PCMU", "a=rtpmap:0 PCMU/8000", gomedia.PCMUlaw},
		{"L16", "a=rtpmap:96 L16/44100", gomedia.PCM},
		{"JPEG", "a=rtpmap:26 JPEG/90000", gomedia.MJPEG},
		{"MJPEG", "a=rtpmap:26 MJPEG/90000", gomedia.MJPEG},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sdpContent := "v=0\r\nm=video 0 RTP/AVP 96\r\n" + tt.rtpmap
			// Audio codecs need m=audio
			if tt.wantType.IsAudio() {
				sdpContent = "v=0\r\nm=audio 0 RTP/AVP 96\r\n" + tt.rtpmap
			}
			_, medias := Parse(sdpContent)
			require.Equal(t, 1, len(medias), "expected 1 media for %s", tt.name)
			assert.Equal(t, tt.wantType, medias[0].Type)
		})
	}
}

// ---------------------------------------------------------------------------
// Parse — static payload types (no rtpmap needed)
// ---------------------------------------------------------------------------

func TestParse_StaticPayloadType_PCMU(t *testing.T) {
	sdpContent := "v=0\r\nm=audio 0 RTP/AVP 0\r\n"
	_, medias := Parse(sdpContent)
	require.Equal(t, 1, len(medias))
	assert.Equal(t, gomedia.PCMUlaw, medias[0].Type)
	assert.Equal(t, 0, medias[0].PayloadType)
}

func TestParse_StaticPayloadType_PCMA(t *testing.T) {
	sdpContent := "v=0\r\nm=audio 0 RTP/AVP 8\r\n"
	_, medias := Parse(sdpContent)
	require.Equal(t, 1, len(medias))
	assert.Equal(t, gomedia.PCMAlaw, medias[0].Type)
	assert.Equal(t, 8, medias[0].PayloadType)
}

// ---------------------------------------------------------------------------
// Parse — OPUS channel count
// ---------------------------------------------------------------------------

func TestParse_OPUS_ChannelCount(t *testing.T) {
	sdpContent := "v=0\r\nm=audio 0 RTP/AVP 111\r\na=rtpmap:111 OPUS/48000/2\r\n"
	_, medias := Parse(sdpContent)
	require.Equal(t, 1, len(medias))
	assert.Equal(t, gomedia.OPUS, medias[0].Type)
	assert.Equal(t, 48000, medias[0].TimeScale)
	assert.Equal(t, 2, medias[0].ChannelCount)
}

func TestParse_OPUS_MonoChannel(t *testing.T) {
	sdpContent := "v=0\r\nm=audio 0 RTP/AVP 111\r\na=rtpmap:111 OPUS/48000/1\r\n"
	_, medias := Parse(sdpContent)
	require.Equal(t, 1, len(medias))
	assert.Equal(t, 1, medias[0].ChannelCount)
}

// ---------------------------------------------------------------------------
// Parse — fmtp attributes
// ---------------------------------------------------------------------------

func TestParse_Fmtp_AACConfig(t *testing.T) {
	sdpContent := strings.Join([]string{
		"v=0",
		"m=audio 0 RTP/AVP 97",
		"a=rtpmap:97 MPEG4-GENERIC/48000/2",
		"a=fmtp:97 config=1190;sizelength=13;indexlength=3",
	}, "\r\n")

	_, medias := Parse(sdpContent)
	require.Equal(t, 1, len(medias))

	m := medias[0]
	assert.Equal(t, []byte{0x11, 0x90}, m.Config)
	assert.Equal(t, 13, m.SizeLength)
	assert.Equal(t, 3, m.IndexLength)
}

func TestParse_Fmtp_SpropParameterSets(t *testing.T) {
	sdpContent := strings.Join([]string{
		"v=0",
		"m=video 0 RTP/AVP 96",
		"a=rtpmap:96 H264/90000",
		"a=fmtp:96 sprop-parameter-sets=Z0IAKeKQFoA=,aM4xsg==;packetization-mode=1",
	}, "\r\n")

	_, medias := Parse(sdpContent)
	require.Equal(t, 1, len(medias))

	m := medias[0]
	require.GreaterOrEqual(t, len(m.SpropParameterSets), 2)
	// Verify base64 decoding happened correctly.
	assert.NotEmpty(t, m.SpropParameterSets[0])
	assert.NotEmpty(t, m.SpropParameterSets[1])
}

// ---------------------------------------------------------------------------
// Parse — x-framerate and x-dimensions
// ---------------------------------------------------------------------------

func TestParse_XFramerate(t *testing.T) {
	sdpContent := "v=0\r\nm=video 0 RTP/AVP 96\r\na=x-framerate:25\r\n"
	_, medias := Parse(sdpContent)
	require.Equal(t, 1, len(medias))
	assert.Equal(t, 25, medias[0].FPS)
}

func TestParse_XFramerateWithSpaces(t *testing.T) {
	// The parser strips spaces from x-framerate lines.
	sdpContent := "v=0\r\nm=video 0 RTP/AVP 96\r\na=x-framerate: 30\r\n"
	_, medias := Parse(sdpContent)
	require.Equal(t, 1, len(medias))
	assert.Equal(t, 30, medias[0].FPS)
}

func TestParse_XDimensions(t *testing.T) {
	sdpContent := "v=0\r\nm=video 0 RTP/AVP 96\r\na=x-dimensions:3840,2160\r\n"
	_, medias := Parse(sdpContent)
	require.Equal(t, 1, len(medias))
	assert.Equal(t, 3840, medias[0].Width)
	assert.Equal(t, 2160, medias[0].Height)
}

// ---------------------------------------------------------------------------
// Parse — control attribute
// ---------------------------------------------------------------------------

func TestParse_Control(t *testing.T) {
	sdpContent := "v=0\r\nm=video 0 RTP/AVP 96\r\na=control:rtsp://host/track1\r\n"
	_, medias := Parse(sdpContent)
	require.Equal(t, 1, len(medias))
	assert.Equal(t, "rtsp://host/track1", medias[0].Control)
}

// ---------------------------------------------------------------------------
// Parse — edge cases
// ---------------------------------------------------------------------------

func TestParse_EmptyInput(t *testing.T) {
	sess, medias := Parse("")
	assert.Equal(t, "", sess.URI)
	assert.Empty(t, medias)
}

func TestParse_NoMediaLines(t *testing.T) {
	sdpContent := "v=0\r\no=- 0 0 IN IP4 0.0.0.0\r\ns=NoMedia\r\nt=0 0\r\n"
	_, medias := Parse(sdpContent)
	assert.Empty(t, medias)
}

func TestParse_UnknownMediaType(t *testing.T) {
	sdpContent := "v=0\r\nm=application 0 RTP/AVP 96\r\n"
	_, medias := Parse(sdpContent)
	assert.Empty(t, medias, "application media type should be ignored")
}

func TestParse_MultipleVideoStreams(t *testing.T) {
	sdpContent := strings.Join([]string{
		"v=0",
		"m=video 0 RTP/AVP 96",
		"a=rtpmap:96 H264/90000",
		"a=control:track1",
		"m=video 0 RTP/AVP 97",
		"a=rtpmap:97 H265/90000",
		"a=control:track2",
	}, "\r\n")

	_, medias := Parse(sdpContent)
	require.Equal(t, 2, len(medias))
	assert.Equal(t, gomedia.H264, medias[0].Type)
	assert.Equal(t, "track1", medias[0].Control)
	assert.Equal(t, gomedia.H265, medias[1].Type)
	assert.Equal(t, "track2", medias[1].Control)
}

func TestParse_AttributesBeforeMedia(t *testing.T) {
	// Attributes before any m= line should be ignored (no media context).
	sdpContent := "v=0\r\na=control:ignored\r\nm=video 0 RTP/AVP 96\r\na=control:valid\r\n"
	_, medias := Parse(sdpContent)
	require.Equal(t, 1, len(medias))
	assert.Equal(t, "valid", medias[0].Control)
}

func TestParse_LFLineEndings(t *testing.T) {
	// Parser should handle LF-only line endings.
	sdpContent := "v=0\nm=video 0 RTP/AVP 96\na=rtpmap:96 H264/90000\na=control:track1\n"
	_, medias := Parse(sdpContent)
	require.Equal(t, 1, len(medias))
	assert.Equal(t, gomedia.H264, medias[0].Type)
	assert.Equal(t, "track1", medias[0].Control)
}

// ---------------------------------------------------------------------------
// Parse — session URI
// ---------------------------------------------------------------------------

func TestParse_SessionURI(t *testing.T) {
	sdpContent := "v=0\r\nu=rtsp://cam.local/stream1\r\nm=video 0 RTP/AVP 96\r\n"
	sess, _ := Parse(sdpContent)
	assert.Equal(t, "rtsp://cam.local/stream1", sess.URI)
}

func TestParse_NoSessionURI(t *testing.T) {
	sdpContent := "v=0\r\nm=video 0 RTP/AVP 96\r\n"
	sess, _ := Parse(sdpContent)
	assert.Equal(t, "", sess.URI)
}
