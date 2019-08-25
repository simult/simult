package httplb

import (
	"context"
	"crypto/tls"
	"net"
	"net/url"
	"sync"
	"sync/atomic"

	"github.com/pkg/errors"
)

var backendDialer = &net.Dialer{
	Timeout:   0,
	KeepAlive: 0,
	DualStack: true,
}

type backendServer struct {
	rawurl           string
	u                *url.URL
	addr             string
	tls              bool
	bcs              map[*bufConn]struct{}
	bcsMu            sync.Mutex
	ctx              context.Context
	ctxCancel        context.CancelFunc
	sessionCount     int64
	rdBytes, wrBytes int64
}

func newBackendServer(rawurl string) (bs *backendServer, err error) {
	bs = &backendServer{
		rawurl: rawurl,
	}
	bs.u, err = url.Parse(bs.rawurl)
	if err != nil {
		bs = nil
		err = errors.WithStack(err)
		return
	}
	if bs.u.Host == "" {
		bs = nil
		err = errors.New("empty hostport")
		return
	}
	switch bs.u.Scheme {
	case "http":
		bs.addr = bs.u.Host
		if p := bs.u.Port(); p == "" {
			bs.addr += ":80"
		}
		bs.tls = false
	case "https":
		bs.addr = bs.u.Host
		if p := bs.u.Port(); p == "" {
			bs.addr += ":443"
		}
		bs.tls = true
	default:
		bs = nil
		err = errors.New("wrong scheme")
		return
	}
	bs.bcs = make(map[*bufConn]struct{}, 16)
	bs.ctx, bs.ctxCancel = context.WithCancel(context.Background())
	return
}

func (bs *backendServer) Close() {
	bs.ctxCancel()
	bs.bcsMu.Lock()
	for bcr := range bs.bcs {
		delete(bs.bcs, bcr)
		bcr.Close()
	}
	bs.bcsMu.Unlock()
}

func (bs *backendServer) ConnAcquire(ctx context.Context) (bc *bufConn, err error) {
	bs.bcsMu.Lock()
	for bcr := range bs.bcs {
		delete(bs.bcs, bcr)
		if bcr.Check() {
			bc = bcr
			break
		}
	}
	bs.bcsMu.Unlock()
	if bc == nil {
		var conn net.Conn
		conn, err = backendDialer.DialContext(ctx, "tcp", bs.addr)
		if err != nil {
			err = errors.WithStack(err)
			return
		}
		if bs.tls {
			conn = tls.Client(conn, &tls.Config{InsecureSkipVerify: true})
		}
		bc = newBufConn(conn)
	}
	atomic.AddInt64(&bs.sessionCount, 1)
	return
}

func (bs *backendServer) ConnRelease(bc *bufConn) {
	atomic.AddInt64(&bs.sessionCount, -1)
	bs.bcsMu.Lock()
	select {
	case <-bs.ctx.Done():
		bc.Close()
	default:
		if bc.Check() {
			bs.bcs[bc] = struct{}{}
		} else {
			bc.Close()
		}
	}
	bs.bcsMu.Unlock()
}
