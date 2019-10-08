package lb

import (
	"regexp"

	"github.com/pkg/errors"
)

const (
	maxHTTPHeaderLineLen = 1 * 1024 * 1024
)

var (
	errGracefulTermination = errors.New("graceful termination")
	errExpectedEOF         = errors.New("expected EOF")
)

var (
	doubleslashRgx = regexp.MustCompile(regexp.QuoteMeta(`//`))
	slashDotRgx    = regexp.MustCompile(regexp.QuoteMeta(`/.`))
)
