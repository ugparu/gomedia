// nolint: all
package mp4io

import (
	"errors"
	"fmt"
	"strings"
)

var errParse = new(ParseError)

type ParseError struct {
	Debug  string
	Offset int
	prev   *ParseError
}

func (p *ParseError) Error() string {
	s := []string{}
	for err := p; err != nil; err = err.prev {
		s = append(s, fmt.Sprintf("%s:%d", err.Debug, err.Offset))
	}
	return "mp4io: parse error: " + strings.Join(s, ",")
}

func parseErr(debug string, offset int, prev error) (err error) {
	if !errors.As(prev, &errParse) {
		return prev
	}
	ppe, _ := prev.(*ParseError) // nolint: errorlint

	return &ParseError{Debug: debug, Offset: offset, prev: ppe}
}
