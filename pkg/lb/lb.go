package lb

import (
	"errors"
	"regexp"
)

const (
	maxHTTPHeaderLineLen = 1 * 1024 * 1024
	maxHTTPHeadersLen    = 10 * 1024 * 1024
)

var (
	errExpectedEOF = errors.New("expected EOF")
)

var (
	doubleslashRgx = regexp.MustCompile(regexp.QuoteMeta(`//`))
	slashDotRgx    = regexp.MustCompile(regexp.QuoteMeta(`/.`))
)
