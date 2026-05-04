package sdp

import (
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/ugparu/gomedia"
)

// Session holds session-level SDP data.
type Session struct {
	URI string
}

// Media describes a single media stream parsed from SDP ("m=" line plus its attributes).
type Media struct {
	AVType             string
	Type               gomedia.CodecType
	FPS                int
	TimeScale          int
	Control            string
	Rtpmap             int
	ChannelCount       int
	Config             []byte
	SpropParameterSets [][]byte
	SpropVPS           []byte
	SpropSPS           []byte
	SpropPPS           []byte
	PayloadType        int
	SizeLength         int
	IndexLength        int
	Width              int
	Height             int
}

func parseMediaDescription(fields []string) (*Media, bool) {
	if len(fields) == 0 {
		return nil, false
	}

	switch fields[0] {
	case "audio", "video":
		media := Media{
			AVType:             fields[0],
			Config:             []byte{},
			SpropParameterSets: [][]byte{},
			SpropVPS:           []byte{},
			SpropSPS:           []byte{},
			SpropPPS:           []byte{},
		}

		mfields := strings.Split(fields[1], " ")
		if len(mfields) >= 3 { //nolint:mnd // m-line format: <port> <proto> <fmt>
			media.PayloadType, _ = strconv.Atoi(mfields[2])
		}

		// RFC 3551 static payload types; dynamic types (>=96) are resolved later via rtpmap.
		const (
			pcmu = 0
			pcma = 8
		)
		switch media.PayloadType {
		case pcmu:
			media.Type = gomedia.PCMUlaw
		case pcma:
			media.Type = gomedia.PCMAlaw
		}

		return &media, true
	default:
		return nil, false
	}
}

func parseCodecType(media *Media, key string, keyval []string) {
	switch strings.ToUpper(key) {
	case "MPEG4-GENERIC":
		media.Type = gomedia.AAC
	case "L16":
		media.Type = gomedia.PCM
	case "OPUS":
		media.Type = gomedia.OPUS
		// RFC 7587: rtpmap for Opus is "opus/48000/2" — channel count lives in the third slash-separated field.
		if len(keyval) > 2 { //nolint:mnd
			if i, err := strconv.Atoi(keyval[2]); err == nil {
				media.ChannelCount = i
			}
		}
	case "H264":
		media.Type = gomedia.H264
	case "JPEG":
		media.Type = gomedia.MJPEG
	case "H265", "HEVC":
		media.Type = gomedia.H265
	case "PCMA":
		media.Type = gomedia.PCMAlaw
		media.ChannelCount = 1
	case "PCMU":
		media.Type = gomedia.PCMUlaw
		media.ChannelCount = 1
	case "MJPEG":
		media.Type = gomedia.MJPEG
	}

	if len(keyval) > 1 {
		if i, err := strconv.Atoi(keyval[1]); err == nil {
			media.TimeScale = i
		}
	}
}

func parseAttributeKeyValue(media *Media, key, val string) {
	switch key {
	case "config":
		media.Config, _ = hex.DecodeString(val)
	case "sizelength":
		media.SizeLength, _ = strconv.Atoi(val)
	case "indexlength":
		media.IndexLength, _ = strconv.Atoi(val)
	case "sprop-vps":
		if decoded, err := base64.StdEncoding.DecodeString(val); err == nil {
			media.SpropVPS = decoded
		}
	case "sprop-sps":
		if decoded, err := base64.StdEncoding.DecodeString(val); err == nil {
			media.SpropSPS = decoded
		}
	case "sprop-pps":
		if decoded, err := base64.StdEncoding.DecodeString(val); err == nil {
			media.SpropPPS = decoded
		}
	case "sprop-parameter-sets":
		// RFC 6184 §8.1: comma-separated, base64-encoded parameter sets.
		for _, field := range strings.Split(val, ",") {
			if field == "" {
				continue
			}
			decoded, _ := base64.StdEncoding.DecodeString(field)
			media.SpropParameterSets = append(media.SpropParameterSets, decoded)
		}
	}
}

func parseAttribute(media *Media, fields []string) {
	for _, field := range fields {
		keyval := strings.SplitN(field, ":", 2) //nolint:mnd
		if len(keyval) >= 2 {                   //nolint:mnd
			key := keyval[0]
			val := keyval[1]
			switch key {
			case "control":
				media.Control = val
			case "rtpmap":
				media.Rtpmap, _ = strconv.Atoi(val)
			case "x-framerate":
				media.FPS, _ = strconv.Atoi(val)
			case "x-dimensions":
				dims := strings.Split(val, ",")
				if len(dims) == 2 { //nolint:mnd // "WxH" coordinate pair
					media.Width, _ = strconv.Atoi(dims[0])
					media.Height, _ = strconv.Atoi(dims[1])
				}
			}
		}

		keyval = strings.Split(field, "/")
		if len(keyval) >= 2 { //nolint:mnd
			parseCodecType(media, keyval[0], keyval)
		}

		keyval = strings.Split(field, ";")
		if len(keyval) > 1 {
			for _, subfield := range keyval {
				subKeyVal := strings.SplitN(subfield, "=", 2) //nolint:mnd
				if len(subKeyVal) == 2 {                      //nolint:mnd
					key := strings.TrimSpace(subKeyVal[0])
					val := subKeyVal[1]
					parseAttributeKeyValue(media, key, val)
				}
			}
		}
	}
}

// Parse parses an SDP payload and returns the session and every "m=" media block.
func Parse(content string) (sess Session, medias []Media) {
	var media *Media

	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		// Some cameras emit "x-framerate: 25" with stray whitespace; collapse it so the
		// subsequent split on "=" still yields a clean key/value pair.
		if strings.Contains(line, "x-framerate") {
			line = strings.ReplaceAll(line, " ", "")
		}

		typeval := strings.SplitN(line, "=", 2) //nolint:mnd
		if len(typeval) != 2 {                  //nolint:mnd
			continue
		}

		fields := strings.SplitN(typeval[1], " ", 2) //nolint:mnd

		switch typeval[0] {
		case "m":
			newMedia, valid := parseMediaDescription(fields)
			if valid {
				medias = append(medias, *newMedia)
				media = &medias[len(medias)-1]
			} else {
				media = nil
			}
		case "u":
			sess.URI = typeval[1]
		case "a":
			if media != nil {
				parseAttribute(media, fields)
			}
		}
	}
	return
}
