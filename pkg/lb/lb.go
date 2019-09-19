package lb

import (
	"github.com/pkg/errors"
)

var (
	errExpectedEOF          = errors.New("expected EOF")
	errGracefulTermination  = errors.New("graceful termination")
	errBufferOrder          = errors.New("buffer order error")
	errFindBackendServer    = errors.New("can not find backend server")
	errConnectBackendServer = errors.New("can not connect backend server")
)
