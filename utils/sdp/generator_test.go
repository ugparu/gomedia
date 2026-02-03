package sdp

import (
	"bytes"
	"testing"

	"github.com/ugparu/gomedia"
)

func TestGenerate_ParseRoundtrip_SessionURI(t *testing.T) {
	sess := Session{URI: "rtsp://example.com/stream"}
	out := Generate(sess, nil)
	gotSess, _ := Parse(out)
	if gotSess.URI != sess.URI {
		t.Fatalf("URI mismatch: got %q want %q", gotSess.URI, sess.URI)
	}
}

func TestGenerate_ParseRoundtrip_H264(t *testing.T) {
	sps := []byte{0x67, 0x64, 0x00, 0x1f}
	pps := []byte{0x68, 0xee, 0x3c, 0x80}

	in := Media{
		AVType:             "video",
		Type:               gomedia.H264,
		TimeScale:          90000,
		PayloadType:        96,
		Control:            "trackID=0",
		SpropParameterSets: [][]byte{sps, pps},
		FPS:                25,
		Width:              1920,
		Height:             1080,
	}
	out := Generate(Session{URI: "rtsp://example.com/live"}, []Media{in})
	_, medias := Parse(out)
	if len(medias) != 1 {
		t.Fatalf("expected 1 media, got %d", len(medias))
	}
	m := medias[0]
	if m.Type != gomedia.H264 {
		t.Fatalf("codec type: got %v want %v", m.Type, gomedia.H264)
	}
	if m.PayloadType != 96 {
		t.Fatalf("payload type: got %d want %d", m.PayloadType, 96)
	}
	if m.TimeScale != 90000 {
		t.Fatalf("timescale: got %d want %d", m.TimeScale, 90000)
	}
	if m.Control != in.Control {
		t.Fatalf("control: got %q want %q", m.Control, in.Control)
	}
	if m.FPS != in.FPS {
		t.Fatalf("fps: got %d want %d", m.FPS, in.FPS)
	}
	if m.Width != in.Width || m.Height != in.Height {
		t.Fatalf("dimensions: got %dx%d want %dx%d", m.Width, m.Height, in.Width, in.Height)
	}
	if len(m.SpropParameterSets) < 2 {
		t.Fatalf("expected sprop-parameter-sets, got %d entries", len(m.SpropParameterSets))
	}
	if !bytes.Equal(m.SpropParameterSets[0], sps) || !bytes.Equal(m.SpropParameterSets[1], pps) {
		t.Fatalf("sprop-parameter-sets mismatch")
	}
}

func TestGenerate_ParseRoundtrip_H265(t *testing.T) {
	vps := []byte{0x40, 0x01, 0x0c, 0x01}
	sps := []byte{0x42, 0x01, 0x01, 0x60}
	pps := []byte{0x44, 0x01, 0xc0, 0xf1}

	in := Media{
		AVType:      "video",
		Type:        gomedia.H265,
		TimeScale:   90000,
		PayloadType: 98,
		Control:     "trackID=1",
		SpropVPS:    vps,
		SpropSPS:    sps,
		SpropPPS:    pps,
	}
	out := Generate(Session{}, []Media{in})
	_, medias := Parse(out)
	if len(medias) != 1 {
		t.Fatalf("expected 1 media, got %d", len(medias))
	}
	m := medias[0]
	if m.Type != gomedia.H265 {
		t.Fatalf("codec type: got %v want %v", m.Type, gomedia.H265)
	}
	if m.PayloadType != 98 {
		t.Fatalf("payload type: got %d want %d", m.PayloadType, 98)
	}
	if m.TimeScale != 90000 {
		t.Fatalf("timescale: got %d want %d", m.TimeScale, 90000)
	}
	if !bytes.Equal(m.SpropVPS, vps) || !bytes.Equal(m.SpropSPS, sps) || !bytes.Equal(m.SpropPPS, pps) {
		t.Fatalf("sprop-vps/sps/pps mismatch")
	}
}

func TestGenerate_ParseRoundtrip_AAC(t *testing.T) {
	cfg := []byte{0x12, 0x10}
	in := Media{
		AVType:      "audio",
		Type:        gomedia.AAC,
		TimeScale:   48000,
		PayloadType: 97,
		Config:      cfg,
		SizeLength:  13,
		IndexLength: 3,
		Control:     "trackID=2",
	}
	out := Generate(Session{}, []Media{in})
	_, medias := Parse(out)
	if len(medias) != 1 {
		t.Fatalf("expected 1 media, got %d", len(medias))
	}
	m := medias[0]
	if m.Type != gomedia.AAC {
		t.Fatalf("codec type: got %v want %v", m.Type, gomedia.AAC)
	}
	if m.TimeScale != 48000 {
		t.Fatalf("timescale: got %d want %d", m.TimeScale, 48000)
	}
	if m.PayloadType != 97 {
		t.Fatalf("payload type: got %d want %d", m.PayloadType, 97)
	}
	if !bytes.Equal(m.Config, cfg) {
		t.Fatalf("aac config mismatch: got %x want %x", m.Config, cfg)
	}
	if m.SizeLength != 13 || m.IndexLength != 3 {
		t.Fatalf("aac lengths: got sizelength=%d indexlength=%d", m.SizeLength, m.IndexLength)
	}
}

func TestGenerate_ParseRoundtrip_OPUS(t *testing.T) {
	in := Media{
		AVType:       "audio",
		Type:         gomedia.OPUS,
		TimeScale:    48000,
		PayloadType:  111,
		ChannelCount: 2,
		Control:      "trackID=3",
	}
	out := Generate(Session{}, []Media{in})
	_, medias := Parse(out)
	if len(medias) != 1 {
		t.Fatalf("expected 1 media, got %d", len(medias))
	}
	m := medias[0]
	if m.Type != gomedia.OPUS {
		t.Fatalf("codec type: got %v want %v", m.Type, gomedia.OPUS)
	}
	if m.TimeScale != 48000 {
		t.Fatalf("timescale: got %d want %d", m.TimeScale, 48000)
	}
	// Note: Parse() sets ChannelCount for OPUS only when 3rd rtpmap segment is present.
	if m.ChannelCount != 2 {
		t.Fatalf("channels: got %d want %d", m.ChannelCount, 2)
	}
}

func TestGenerate_ParseRoundtrip_G711(t *testing.T) {
	tests := []struct {
		name        string
		ct          gomedia.CodecType
		payloadType int
	}{
		{name: "PCMU", ct: gomedia.PCMUlaw, payloadType: 0},
		{name: "PCMA", ct: gomedia.PCMAlaw, payloadType: 8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := Media{
				AVType:      "audio",
				Type:        tt.ct,
				TimeScale:   8000,
				PayloadType: tt.payloadType,
				Control:     "trackID=4",
			}
			out := Generate(Session{}, []Media{in})
			_, medias := Parse(out)
			if len(medias) != 1 {
				t.Fatalf("expected 1 media, got %d", len(medias))
			}
			m := medias[0]
			if m.Type != tt.ct {
				t.Fatalf("codec type: got %v want %v", m.Type, tt.ct)
			}
			if m.PayloadType != tt.payloadType {
				t.Fatalf("payload type: got %d want %d", m.PayloadType, tt.payloadType)
			}
			if m.TimeScale != 8000 {
				t.Fatalf("timescale: got %d want %d", m.TimeScale, 8000)
			}
			if m.ChannelCount != 1 {
				t.Fatalf("channels: got %d want %d", m.ChannelCount, 1)
			}
		})
	}
}
