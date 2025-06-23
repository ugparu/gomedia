package rtsp

import (
	"strings"
)

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
