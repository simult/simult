package lb

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/goinsane/xlog"
	"github.com/simult/server/pkg/hc"
)

var backendDialer = &net.Dialer{
	Timeout:   0,
	KeepAlive: 0,
	DualStack: true,
}

type backendServer struct {
	server          string
	serverURL       *url.URL
	address         string
	useTLS          bool
	bcs             map[*bufConn]struct{}
	bcsMu           sync.Mutex
	healthCheck     hc.HealthCheck
	healthCheckMu   sync.RWMutex
	activeConnCount int64
	idleConnCount   int64
	totalConnCount  int64

	workerTkr *time.Ticker
	workerWg  sync.WaitGroup

	ctx       context.Context
	ctxCancel context.CancelFunc

	shared   bool
	sharedMu sync.Mutex
}

func newBackendServer(server string) (bs *backendServer, err error) {
	var serverURL *url.URL
	var address string
	var useTLS bool
	serverURL, err = url.Parse(server)
	if err != nil {
		err = fmt.Errorf("server url parse error: %w", err)
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
		server:    serverURL.Scheme + "://" + address,
		serverURL: serverURL,
		address:   address,
		useTLS:    useTLS,
		bcs:       make(map[*bufConn]struct{}, 16),
	}
	bs.workerTkr = time.NewTicker(100 * time.Millisecond)
	bs.ctx, bs.ctxCancel = context.WithCancel(context.Background())

	bs.workerWg.Add(1)
	go bs.worker()

	return
}

func (bs *backendServer) Close() {
	if bs.SetShared(false) {
		return
	}

	bs.ctxCancel()
	bs.workerTkr.Stop()
	bs.workerWg.Wait()

	bs.bcsMu.Lock()
	for bcr := range bs.bcs {
		delete(bs.bcs, bcr)
		atomic.AddInt64(&bs.idleConnCount, -1)
		atomic.AddInt64(&bs.totalConnCount, -1)
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

func (bs *backendServer) worker() {
	for done := false; !done; {
		select {
		case <-bs.workerTkr.C:
			bs.bcsMu.Lock()
			for bcr := range bs.bcs {
				if !bcr.Check() {
					delete(bs.bcs, bcr)
					atomic.AddInt64(&bs.idleConnCount, -1)
					atomic.AddInt64(&bs.totalConnCount, -1)
					bcr.Close()
					continue
				}
			}
			bs.bcsMu.Unlock()
		case <-bs.ctx.Done():
			done = true
		}
	}
	bs.workerWg.Done()
}

func (bs *backendServer) IsShared() bool {
	bs.sharedMu.Lock()
	r := bs.shared
	bs.sharedMu.Unlock()
	return r
}

func (bs *backendServer) SetShared(status bool) bool {
	bs.sharedMu.Lock()
	r := bs.shared
	bs.shared = status
	bs.sharedMu.Unlock()
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
		atomic.AddInt64(&bs.idleConnCount, -1)
		atomic.AddInt64(&bs.totalConnCount, -1)
		if bcr.Check() {
			bc = bcr
			break
		}
		xlog.V(100).Debugf("connection closed from backend server %q", bcr.RemoteAddr().String())
		bcr.Close()
	}
	bs.bcsMu.Unlock()
	atomic.AddInt64(&bs.activeConnCount, 1)
	atomic.AddInt64(&bs.totalConnCount, 1)
	if bc == nil {
		var conn net.Conn
		conn, err = backendDialer.DialContext(ctx, "tcp", bs.address)
		if err != nil {
			atomic.AddInt64(&bs.activeConnCount, -1)
			atomic.AddInt64(&bs.totalConnCount, -1)
			return
		}
		if bs.useTLS {
			conn = tls.Client(conn, &tls.Config{InsecureSkipVerify: true})
		}
		bc = newBufConn(conn)
		xlog.V(100).Debugf("connected to backend server %q", bc.RemoteAddr().String())
	}
	return
}

func (bs *backendServer) ConnRelease(bc *bufConn) {
	if bc == nil {
		return
	}
	atomic.AddInt64(&bs.activeConnCount, -1)
	atomic.AddInt64(&bs.totalConnCount, -1)
	bs.bcsMu.Lock()
	select {
	case <-bs.ctx.Done():
		bc.Close()
	default:
		if bc.Check() {
			if _, ok := bs.bcs[bc]; !ok {
				bs.bcs[bc] = struct{}{}
				atomic.AddInt64(&bs.idleConnCount, 1)
				atomic.AddInt64(&bs.totalConnCount, 1)
			}
		} else {
			xlog.V(100).Debugf("connection closed from backend server %q", bc.RemoteAddr().String())
			bc.Close()
		}
	}
	bs.bcsMu.Unlock()
}
