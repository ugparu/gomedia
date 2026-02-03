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

// stringInBetween extracts a substring from the input string that is between the specified start and end substrings.
func stringInBetween(str string, start string, end string) (result string) {
	// Find the index of the start substring in the input string.
	s := strings.Index(str, start)
	if s == -1 {
		return
	}
	// Extract the substring starting from the end of the start substring.
	str = str[s+len(start):]
	// Find the index of the end substring in the remaining string.
	e := strings.Index(str, end)
	if e == -1 {
		return
	}
	// Extract the substring up to the end substring, creating the result.
	str = str[:e]
	return str
}
