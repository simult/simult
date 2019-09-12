package lb

import (
	"context"
	"crypto/tls"
	"net"
	"net/url"
	"sync"
	"sync/atomic"

	"github.com/pkg/errors"
	"github.com/simult/server/pkg/hc"
)

var backendDialer = &net.Dialer{
	Timeout:   0,
	KeepAlive: 0,
	DualStack: true,
}

type backendServer struct {
	server           string
	serverURL        *url.URL
	address          string
	useTLS           bool
	bcs              map[*bufConn]struct{}
	bcsMu            sync.Mutex
	ctx              context.Context
	ctxCancel        context.CancelFunc
	healthCheck      hc.HealthCheck
	healthCheckMu    sync.RWMutex
	sessionCount     int64
	rdBytes, wrBytes int64

	forked   bool
	forkedMu sync.Mutex
}

func newBackendServer(server string) (bs *backendServer, err error) {
	var serverURL *url.URL
	var address string
	var useTLS bool
	serverURL, err = url.Parse(server)
	if err != nil {
		err = errors.WithStack(err)
		return
	}
	if serverURL.Host == "" {
		err = errors.New("empty hostport")
		return
	}
	switch serverURL.Scheme {
	case "http":
		address = serverURL.Host
		if p := serverURL.Port(); p == "" {
			address += ":80"
		}
		useTLS = false
	case "https":
		address = serverURL.Host
		if p := serverURL.Port(); p == "" {
			address += ":443"
		}
		useTLS = true
	default:
		err = errors.New("wrong scheme")
		return
	}
	bs = &backendServer{
		server:    serverURL.Scheme + "://" + serverURL.Host,
		serverURL: serverURL,
		address:   address,
		useTLS:    useTLS,
		bcs:       make(map[*bufConn]struct{}, 16),
	}
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

	bs.healthCheckMu.Lock()
	if bs.healthCheck != nil {
		bs.healthCheck.Close()
		bs.healthCheck = nil
	}
	bs.healthCheckMu.Unlock()
}

func (bs *backendServer) SetForked(status bool) bool {
	bs.forkedMu.Lock()
	r := bs.forked
	bs.forked = status
	bs.forkedMu.Unlock()
	return r
}

func (bs *backendServer) SetHealthCheck(healthCheck hc.HealthCheck) {
	bs.healthCheckMu.Lock()
	select {
	case <-bs.ctx.Done():
		if healthCheck != nil {
			healthCheck.Close()
			healthCheck = nil
		}
	default:
	}
	if bs.healthCheck != nil {
		bs.healthCheck.Close()
	}
	bs.healthCheck = healthCheck
	bs.healthCheckMu.Unlock()
}

func (bs *backendServer) Healthy() bool {
	bs.healthCheckMu.RLock()
	defer bs.healthCheckMu.RUnlock()
	if bs.healthCheck != nil {
		return bs.healthCheck.Healthy()
	}
	return true
}

func (bs *backendServer) ConnAcquire(ctx context.Context) (bc *bufConn, err error) {
	bs.bcsMu.Lock()
	for bcr := range bs.bcs {
		delete(bs.bcs, bcr)
		if bcr.Check() {
			bc = bcr
			break
		}
		debugLogger.Printf("connection closed from %v\n", bcr.RemoteAddr())
		bcr.Close()
	}
	bs.bcsMu.Unlock()
	if bc == nil {
		var conn net.Conn
		conn, err = backendDialer.DialContext(ctx, "tcp", bs.address)
		if err != nil {
			err = errors.WithStack(err)
			return
		}
		if bs.useTLS {
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
			debugLogger.Printf("connection closed from %v\n", bc.RemoteAddr())
			bc.Close()
		}
	}
	bs.bcsMu.Unlock()
}