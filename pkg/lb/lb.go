package lb

import (
	"errors"
	"github.com/goinsane/xlog"
	"math/rand"
	"regexp"
	"strings"
	"sync/atomic"
	"unsafe"
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

var (
	accessLogger *xlog.Logger
)

// SetAccessLogger sets xlog.Logger for access logs
func SetAccessLogger(l *xlog.Logger) {
	atomic.StorePointer((*unsafe.Pointer)(unsafe.Pointer(&accessLogger)), unsafe.Pointer(l))
}

func validOptionalPort(port string) bool {
	if port == "" {
		return true
	}
	if port[0] != ':' {
		return false
	}
	for _, b := range port[1:] {
		if b < '0' || b > '9' {
			return false
		}
	}
	return true
}

func splitHostPort(hostport string) (host, port string) {
	host = hostport

	colon := strings.LastIndexByte(host, ':')
	if colon != -1 && validOptionalPort(host[colon:]) {
		host, port = host[:colon], host[colon+1:]
	}

	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}

	return
}

func genRandByteSlice(size int) []byte {
	if size < 0 {
		return nil
	}
	r := make([]byte, size)
	x := uint64(0)
	for i := range r {
		if x == 0 {
			x = rand.Uint64()
		}
		r[i] = byte(x)
		x >>= 8
	}
	return r
}

type nopWriter struct {
}

func (w *nopWriter) Write(p []byte) (n int, err error) {
	return
}
