package sdp

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/ugparu/gomedia"
)

// Generate builds an SDP suitable for RTSP ANNOUNCE/RECORD.
// It is intentionally minimal but roundtrippable by Parse().
func Generate(sess Session, medias []Media) string {
	lines := make([]string, 0, 32)

	// Session-level.
	lines = append(lines,
		"v=0",
		"o=- 0 0 IN IP4 127.0.0.1",
		"s=gomedia",
		"t=0 0",
	)
	if sess.URI != "" {
		lines = append(lines, "u="+sess.URI)
	}
	lines = append(lines, "a=control:*")

	// Deterministic output: sort by AVType then payload type.
	mediasCopy := append([]Media(nil), medias...)
	sort.SliceStable(mediasCopy, func(i, j int) bool {
		pri := func(av string) int {
			switch av {
			case "video":
				return 0
			case "audio":
				return 1
			default:
				return 2
			}
		}
		if pri(mediasCopy[i].AVType) != pri(mediasCopy[j].AVType) {
			return pri(mediasCopy[i].AVType) < pri(mediasCopy[j].AVType)
		}
		return mediasCopy[i].PayloadType < mediasCopy[j].PayloadType
	})

	for _, m := range mediasCopy {
		for _, l := range marshalMedia(m) {
			lines = append(lines, l)
		}
	}

	// RTSP bodies conventionally use CRLF.
	return strings.Join(lines, "\r\n") + "\r\n"
}

func marshalMedia(m Media) []string {
	av := m.AVType
	if av == "" {
		av = defaultAVType(m.Type)
	}
	if av == "" {
		av = "video"
	}

	pt := m.PayloadType
	if pt == 0 {
		pt = defaultPayloadType(m.Type)
	}

	ts := m.TimeScale
	if ts == 0 {
		ts = defaultTimeScale(m.Type)
	}

	lines := make([]string, 0, 12)

	// m=<media> <port> <proto> <fmt>
	lines = append(lines, fmt.Sprintf("m=%s 0 RTP/AVP %d", av, pt))

	// a=rtpmap:<pt> <encoding>/<clock>[/<channels>]
	enc := rtpmapEncoding(m.Type)
	if enc != "" {
		if isAudio(m.Type) {
			ch := m.ChannelCount
			if ch == 0 {
				ch = defaultChannels(m.Type)
			}
			// For OPUS/PCMA/PCMU parser expects "ENC/<TimeScale>/<channels>".
			lines = append(lines, fmt.Sprintf("a=rtpmap:%d %s/%d/%d", pt, enc, ts, ch))
		} else {
			lines = append(lines, fmt.Sprintf("a=rtpmap:%d %s/%d", pt, enc, ts))
		}
	}

	// a=fmtp:<pt> ...
	if fmtp := fmtpLine(m, pt); fmtp != "" {
		lines = append(lines, "a=fmtp:"+fmtp)
	}

	if m.FPS > 0 {
		lines = append(lines, fmt.Sprintf("a=x-framerate:%d", m.FPS))
	}
	if m.Width > 0 && m.Height > 0 {
		lines = append(lines, fmt.Sprintf("a=x-dimensions:%d,%d", m.Width, m.Height))
	}
	if m.Control != "" {
		lines = append(lines, "a=control:"+m.Control)
	}

	return lines
}

func fmtpLine(m Media, pt int) string {
	switch m.Type {
	case gomedia.H264:
		// Prefer explicit SPS/PPS fields, otherwise use SpropParameterSets.
		sps := m.SpropSPS
		pps := m.SpropPPS
		if len(sps) == 0 && len(pps) == 0 && len(m.SpropParameterSets) >= 2 {
			sps = m.SpropParameterSets[0]
			pps = m.SpropParameterSets[1]
		}
		if len(sps) == 0 || len(pps) == 0 {
			return ""
		}
		val := base64.StdEncoding.EncodeToString(sps) + "," + base64.StdEncoding.EncodeToString(pps)
		// Put sprop first so Parse() sees it (it can parse any order, but keep stable).
		return fmt.Sprintf("%d sprop-parameter-sets=%s;packetization-mode=1", pt, val)
	case gomedia.H265:
		if len(m.SpropVPS) == 0 || len(m.SpropSPS) == 0 || len(m.SpropPPS) == 0 {
			return ""
		}
		// Keep semicolon-separated, Parse() scans key=value segments.
		return fmt.Sprintf("%d sprop-vps=%s;sprop-sps=%s;sprop-pps=%s",
			pt,
			base64.StdEncoding.EncodeToString(m.SpropVPS),
			base64.StdEncoding.EncodeToString(m.SpropSPS),
			base64.StdEncoding.EncodeToString(m.SpropPPS),
		)
	case gomedia.AAC:
		if len(m.Config) == 0 {
			return ""
		}
		sizeLen := m.SizeLength
		if sizeLen == 0 {
			sizeLen = 13
		}
		indexLen := m.IndexLength
		if indexLen == 0 {
			indexLen = 3
		}

		// Minimal MPEG4-GENERIC fmtp fields that our Parse() understands.
		// Other fields (mode, profile-level-id, etc.) are intentionally omitted.
		return fmt.Sprintf("%d config=%s;sizelength=%d;indexlength=%d",
			pt, strings.ToUpper(hex.EncodeToString(m.Config)), sizeLen, indexLen)
	default:
		// OPUS/PCM/PCMA/PCMU usually don't require fmtp for our current Parse()/demuxers.
		return ""
	}
}

func defaultAVType(ct gomedia.CodecType) string {
	if isAudio(ct) {
		return "audio"
	}
	if isVideo(ct) {
		return "video"
	}
	return ""
}

func isAudio(ct gomedia.CodecType) bool {
	switch ct {
	case gomedia.AAC, gomedia.OPUS, gomedia.PCM, gomedia.PCMAlaw, gomedia.PCMUlaw:
		return true
	default:
		return false
	}
}

func isVideo(ct gomedia.CodecType) bool {
	switch ct {
	case gomedia.H264, gomedia.H265, gomedia.MJPEG:
		return true
	default:
		return false
	}
}

func rtpmapEncoding(ct gomedia.CodecType) string {
	switch ct {
	case gomedia.AAC:
		return "MPEG4-GENERIC"
	case gomedia.OPUS:
		return "OPUS"
	case gomedia.PCM:
		return "L16"
	case gomedia.PCMAlaw:
		return "PCMA"
	case gomedia.PCMUlaw:
		return "PCMU"
	case gomedia.H264:
		return "H264"
	case gomedia.H265:
		return "H265"
	case gomedia.MJPEG:
		// Parse() maps both "JPEG" and "MJPEG" to MJPEG.
		return "JPEG"
	default:
		return ""
	}
}

func defaultTimeScale(ct gomedia.CodecType) int {
	switch ct {
	case gomedia.H264, gomedia.H265, gomedia.MJPEG:
		return 90000
	case gomedia.OPUS:
		return 48000
	case gomedia.AAC:
		return 48000
	case gomedia.PCM, gomedia.PCMAlaw, gomedia.PCMUlaw:
		return 8000
	default:
		return 90000
	}
}

func defaultChannels(ct gomedia.CodecType) int {
	switch ct {
	case gomedia.OPUS, gomedia.AAC, gomedia.PCM:
		return 2
	case gomedia.PCMAlaw, gomedia.PCMUlaw:
		return 1
	default:
		return 1
	}
}

func defaultPayloadType(ct gomedia.CodecType) int {
	// Prefer static PTs for G.711 if present; otherwise dynamic 96.
	switch ct {
	case gomedia.PCMUlaw:
		return 0
	case gomedia.PCMAlaw:
		return 8
	default:
		return 96
	}
}
