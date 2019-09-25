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
	errCommunication       = errors.New("communication error")
	errProtocol            = errors.New("protocol error")
	errFrontendTimeout     = errors.New("frontend timeout")
	errBackendTimeout      = errors.New("backend timeout")
	errBackend             = errors.New("backend error")
)

var (
	doubleslashRgx = regexp.MustCompile(regexp.QuoteMeta(`//`))
	slashDotRgx    = regexp.MustCompile(regexp.QuoteMeta(`/.`))
)
