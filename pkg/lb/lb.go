package lb

import (
	"regexp"

	"github.com/pkg/errors"
)

const (
	maxHTTPHeaderLineLen = 1 * 1024 * 1024
)

var (
	errExpectedEOF          = errors.New("expected EOF")
	errGracefulTermination  = errors.New("graceful termination")
	errFrontendTimeout      = errors.New("frontend timeout")
	errBackendTimeout       = errors.New("backend timeout")
	errBufferOrder          = errors.New("buffer order error")
	errFindBackendServer    = errors.New("can not find backend server")
	errConnectBackendServer = errors.New("can not connect backend server")
)

var (
	doubleslashRgx = regexp.MustCompile(regexp.QuoteMeta(`//`))
	slashDotRgx    = regexp.MustCompile(regexp.QuoteMeta(`/.`))
)
