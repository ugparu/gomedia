package rtsp

import (
	"strings"
)

// controlTrack returns the full RTSP URI for a track control string.
// If track already contains "rtsp://", it is returned as-is. Otherwise it is appended to base.
func controlTrack(base, track string) string {
	if strings.Contains(track, "rtsp://") {
		return track
	}
	if !strings.HasSuffix(base, "/") {
		track = "/" + track
	}
	return base + track
}

// stringInBetween returns the substring between start and end, or "" if either
// delimiter is missing.
func stringInBetween(str string, start string, end string) (result string) {
	s := strings.Index(str, start)
	if s == -1 {
		return
	}
	str = str[s+len(start):]
	e := strings.Index(str, end)
	if e == -1 {
		return
	}
	return str[:e]
}
