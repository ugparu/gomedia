package sdp

import (
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/ugparu/gomedia"
)

// Session represents the information related to an SDP session.
type Session struct {
	URI string
}

// Media represents the information related to a media stream in an SDP session.
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
}

// parseMediaDescription parses the media description line
func parseMediaDescription(fields []string) (*Media, bool) {
	if len(fields) == 0 {
		return nil, false
	}

	switch fields[0] {
	case "audio", "video":
		media := Media{
			AVType:             fields[0],
			Type:               0,
			FPS:                0,
			TimeScale:          0,
			Control:            "",
			Rtpmap:             0,
			ChannelCount:       0,
			Config:             []byte{},
			SpropParameterSets: [][]byte{},
			SpropVPS:           []byte{},
			SpropSPS:           []byte{},
			SpropPPS:           []byte{},
			PayloadType:        0,
			SizeLength:         0,
			IndexLength:        0,
		}

		mfields := strings.Split(fields[1], " ")
		if len(mfields) >= 3 { //nolint:mnd
			media.PayloadType, _ = strconv.Atoi(mfields[2])
		}

		const (
			pcmu = 0
			pcma = 8
		)

		// Set codec type based on payload type
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

// parseCodecType sets the codec type based on the key
func parseCodecType(media *Media, key string, keyval []string) {
	switch strings.ToUpper(key) {
	case "MPEG4-GENERIC":
		media.Type = gomedia.AAC
	case "L16":
		media.Type = gomedia.PCM
	case "OPUS":
		media.Type = gomedia.OPUS
		// Parse additional parameters for OPUS codec
		if len(keyval) > 2 { //nolint:mnd
			if i, err := strconv.Atoi(keyval[2]); err == nil {
				media.ChannelCount = i
			}
		}
	case "H264":
		media.Type = gomedia.H264
	case "JPEG":
		media.Type = gomedia.JPEG
	case "H265", "HEVC":
		media.Type = gomedia.H265
	case "PCMA":
		media.Type = gomedia.PCMAlaw
		media.ChannelCount = 1
	case "PCMU":
		media.Type = gomedia.PCMUlaw
		media.ChannelCount = 1
	}

	// Parse time scale
	if len(keyval) > 1 {
		if i, err := strconv.Atoi(keyval[1]); err == nil {
			media.TimeScale = i
		}
	}
}

// parseAttributeKeyValue processes attribute key-value pairs
func parseAttributeKeyValue(media *Media, key, val string) {
	switch key {
	case "config":
		media.Config, _ = hex.DecodeString(val)
	case "sizelength":
		media.SizeLength, _ = strconv.Atoi(val)
	case "indexlength":
		media.IndexLength, _ = strconv.Atoi(val)
	case "sprop-vps":
		// Decode base64 and handle errors
		decoded, err := base64.StdEncoding.DecodeString(val)
		if err == nil {
			media.SpropVPS = decoded
		}
	case "sprop-sps":
		// Decode base64 and handle errors
		decoded, err := base64.StdEncoding.DecodeString(val)
		if err == nil {
			media.SpropSPS = decoded
		}
	case "sprop-pps":
		// Decode base64 and handle errors
		decoded, err := base64.StdEncoding.DecodeString(val)
		if err == nil {
			media.SpropPPS = decoded
		}
	case "sprop-parameter-sets":
		// Split by "," and decode base64 for each field
		fields := strings.Split(val, ",")
		for _, field := range fields {
			if field == "" {
				continue
			}
			decoded, _ := base64.StdEncoding.DecodeString(field)
			media.SpropParameterSets = append(media.SpropParameterSets, decoded)
		}
	}
}

// parseAttribute processes attribute lines
func parseAttribute(media *Media, fields []string) {
	for _, field := range fields {
		// Process key:value format
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
			}
		}

		// Process key/value format
		keyval = strings.Split(field, "/")
		if len(keyval) >= 2 { //nolint:mnd
			parseCodecType(media, keyval[0], keyval)
		}

		// Process key=value format in semicolon-separated list
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

// Parse parses the SDP content and returns Session and Media information.
func Parse(content string) (sess Session, medias []Media) {
	var media *Media

	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		// Handle special case for x-framerate
		if strings.Contains(line, "x-framerate") {
			line = strings.Replace(line, " ", "", -1) //nolint:gocritic
		}

		// Split line into key and value
		typeval := strings.SplitN(line, "=", 2) //nolint:mnd
		if len(typeval) != 2 {                  //nolint:mnd
			continue
		}

		fields := strings.SplitN(typeval[1], " ", 2) //nolint:mnd

		switch typeval[0] {
		case "m":
			// Start of a new media description
			newMedia, valid := parseMediaDescription(fields)
			if valid {
				medias = append(medias, *newMedia)
				media = &medias[len(medias)-1]
			} else {
				media = nil
			}

		case "u":
			// Session URI
			sess.URI = typeval[1]

		case "a":
			// Attribute information
			if media != nil {
				parseAttribute(media, fields)
			}
		}
	}
	return
}
