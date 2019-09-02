package httplb

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
)

type LoadBalancerOptions struct {
	Timeout        time.Duration
	DefaultBackend *Backend
}

func (o *LoadBalancerOptions) CopyFrom(src *LoadBalancerOptions) {
	*o = *src
}

type LoadBalancer struct {
	opts   LoadBalancerOptions
	optsMu sync.RWMutex
}

func NewLoadBalancer(opts LoadBalancerOptions) (l *LoadBalancer) {
	l = &LoadBalancer{}
	l.opts.CopyFrom(&opts)
	return
}

func (l *LoadBalancer) GetOpts() (opts LoadBalancerOptions) {
	l.optsMu.RLock()
	opts.CopyFrom(&l.opts)
	l.optsMu.RUnlock()
	return
}

func (l *LoadBalancer) getBackend(feStatusLine string, feHdr http.Header) (b *Backend) {
	return l.opts.DefaultBackend
}

func (l *LoadBalancer) beServe(ctx context.Context, okCh chan<- bool, feConn *bufConn, feStatusLine string, feHdr http.Header, beConn *bufConn) {
	ok := false
	defer func() {
		if !ok {
			beConn.Flush()
			beConn.Close()
		}
		okCh <- ok
	}()

	ingressOKCh := make(chan bool, 1)
	go func() {
		var err error
		defer func() { ingressOKCh <- err == nil }()
		_, err = writeHeader(beConn.Writer, feStatusLine, feHdr)
		if err != nil {
			DebugLogger.Printf("write header to backend %v from frontend %v: %v\n", beConn.RemoteAddr(), feConn.RemoteAddr(), err)
			return
		}
		_, err = copyBody(beConn.Writer, feConn.Reader, feHdr, true)
		if err != nil {
			if errors.Cause(err) != eofBody {
				DebugLogger.Printf("write body to backend %v from frontend %v: %v\n", beConn.RemoteAddr(), feConn.RemoteAddr(), err)
			}
			return
		}
	}()

	beStatusLine, beHdr, _, err := splitHeader(beConn.Reader)
	if err != nil {
		DebugLogger.Printf("read header from backend %v: %v\n", beConn.RemoteAddr(), err)
		return
	}

	_, err = writeHeader(feConn.Writer, beStatusLine, beHdr)
	if err != nil {
		DebugLogger.Printf("write header to frontend %v from backend %v: %v\n", feConn.RemoteAddr(), beConn.RemoteAddr(), err)
		return
	}

	_, err = copyBody(feConn.Writer, beConn.Reader, beHdr, false)
	if err != nil {
		if errors.Cause(err) != eofBody {
			DebugLogger.Printf("write body to frontend %v from backend %v: %v\n", feConn.RemoteAddr(), beConn.RemoteAddr(), err)
		}
		return
	}

	if ingressOK := <-ingressOKCh; !ingressOK {
		return
	}

	switch strings.ToLower(beHdr.Get("Connection")) {
	case "keep-alive":
	case "close":
		fallthrough
	default:
		return
	}

	if beConn.Reader.Buffered() != 0 {
		DebugLogger.Printf("buffer order error on backend %v\n", beConn.RemoteAddr())
		return
	}

	ok = true
}

func (l *LoadBalancer) feServe(ctx context.Context, okCh chan<- bool, feConn *bufConn) {
	ok := false
	defer func() {
		if !ok {
			feConn.Flush()
			feConn.Close()
		}
		okCh <- ok
	}()

	feStatusLine, feHdr, nr, err := splitHeader(feConn.Reader)
	if err != nil {
		if nr > 0 {
			DebugLogger.Printf("read header from frontend %v: %v\n", feConn.RemoteAddr(), err)
		}
		feConn.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
		return
	}

	b := l.getBackend(feStatusLine, feHdr)
	bs := b.FindServer(ctx)
	if bs == nil {
		feConn.Write([]byte("HTTP/1.1 503 Service Unavailable\r\n\r\n"))
		return
	}

	beConn, err := bs.ConnAcquire(ctx)
	if err != nil {
		feConn.Write([]byte("HTTP/1.1 503 Service Unavailable\r\n\r\n"))
		return
	}
	if tcpConn, ok := beConn.Conn().(*net.TCPConn); ok {
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(1 * time.Second)
	}

	bOpts := b.GetOpts()
	beCtx, beCtxCancel := ctx, context.CancelFunc(func() {})
	if bOpts.Timeout > 0 {
		beCtx, beCtxCancel = context.WithTimeout(beCtx, bOpts.Timeout)
	}
	beOK := false
	beOKCh := make(chan bool, 1)
	go l.beServe(beCtx, beOKCh, feConn, feStatusLine, feHdr, beConn)
	select {
	case <-beCtx.Done():
		beConn.Close()
	case beOK = <-beOKCh:
	}
	beCtxCancel()
	if !beOK {
		return
	}

	bs.ConnRelease(beConn)

	if feConn.Reader.Buffered() != 0 {
		DebugLogger.Printf("buffer order error on frontend %v\n", feConn.RemoteAddr())
		return
	}

	ok = true
}

func (l *LoadBalancer) Serve(ctx context.Context, conn net.Conn) {
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(1 * time.Second)
	}
	feConn := newBufConn(conn)
	DebugLogger.Printf("connected %v to frontend %v\n", feConn.RemoteAddr(), feConn.LocalAddr())
	for {
		lOpts := l.GetOpts()
		feCtx, feCtxCancel := ctx, context.CancelFunc(func() {})
		if lOpts.Timeout > 0 {
			feCtx, feCtxCancel = context.WithTimeout(feCtx, lOpts.Timeout)
		}
		feOK := false
		feOKCh := make(chan bool, 1)
		go l.feServe(feCtx, feOKCh, feConn)
		select {
		case <-feCtx.Done():
			feConn.Close()
		case feOK = <-feOKCh:
		}
		feCtxCancel()
		if !feOK {
			break
		}
	}
	DebugLogger.Printf("disconnected %v to frontend %v\n", feConn.RemoteAddr(), feConn.LocalAddr())
	return
}
