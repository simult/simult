package httplb

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"
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
	l = &LoadBalancer{
		opts: opts,
	}
	return
}

func (l *LoadBalancer) getBackend(feStatusLine string, feHdr http.Header) (b *Backend) {
	return l.opts.DefaultBackend
}

func (l *LoadBalancer) serveSingle(ctx context.Context, okCh chan<- bool, feConn *bufConn) {
	ok := false
	defer func() { okCh <- ok }()
	defer feConn.Flush()

	feStatusLine, feHdr, err := splitHeader(feConn.Reader)
	if err != nil || feStatusLine == "" {
		DebugLogger.Printf("read header from frontend %v: %v\n", feConn.RemoteAddr(), err)
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

	_, err = writeHeader(beConn.Writer, feStatusLine, feHdr)
	if err != nil {
		DebugLogger.Printf("write header to backend %v from frontend %v: %v\n", beConn.RemoteAddr(), feConn.RemoteAddr(), err)
		beConn.Close()
		return
	}

	ingressOKCh := make(chan bool, 1)
	go func() {
		var err error
		_, err = copyBody(beConn.Writer, feConn.Reader, feHdr, true)
		if err != nil {
			DebugLogger.Printf("write body to backend %v from frontend %v: %v\n", beConn.RemoteAddr(), feConn.RemoteAddr(), err)
			beConn.Close()
		}
		ingressOKCh <- err == nil
	}()

	beStatusLine, beHdr, err := splitHeader(beConn.Reader)
	if err != nil || beStatusLine == "" {
		DebugLogger.Printf("read header from backend %v: %v\n", beConn.RemoteAddr(), err)
		beConn.Close()
		return
	}

	_, err = writeHeader(feConn.Writer, beStatusLine, beHdr)
	if err != nil {
		DebugLogger.Printf("write header to frontend %v from backend %v: %v\n", feConn.RemoteAddr(), beConn.RemoteAddr(), err)
		beConn.Close()
		return
	}

	_, err = copyBody(feConn.Writer, beConn.Reader, beHdr, false)
	if err != nil {
		DebugLogger.Printf("write body to frontend %v from backend %v: %v\n", feConn.RemoteAddr(), beConn.RemoteAddr(), err)
		beConn.Close()
		return
	}

	if ok := <-ingressOKCh; !ok {
		return
	}

	if feConn.Reader.Buffered() != 0 {
		DebugLogger.Printf("buffer order error on frontend %v\n", feConn.RemoteAddr())
		beConn.Close()
		return
	}

	if beConn.Reader.Buffered() != 0 {
		DebugLogger.Printf("buffer order error on backend %v\n", beConn.RemoteAddr())
		beConn.Close()
		return
	}

	bs.ConnRelease(beConn)
	ok = true
	return
}

func (l *LoadBalancer) Serve(ctx context.Context, conn net.Conn) {
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(1 * time.Second)
	}
	feConn := newBufConn(conn)
	defer feConn.Close()

	DebugLogger.Printf("connected %v to frontend %v\n", feConn.RemoteAddr(), feConn.LocalAddr())
	defer DebugLogger.Printf("disconnected %v to frontend %v\n", feConn.RemoteAddr(), feConn.LocalAddr())

	for ok := true; ok; {
		var opts LoadBalancerOptions
		l.optsMu.RLock()
		opts.CopyFrom(&l.opts)
		l.optsMu.RUnlock()
		singleCtx, singleCtxCancel := ctx, context.CancelFunc(func() {})
		if opts.Timeout > 0 {
			singleCtx, singleCtxCancel = context.WithTimeout(ctx, opts.Timeout)
		}
		singleOKCh := make(chan bool, 1)
		go l.serveSingle(singleCtx, singleOKCh, feConn)
		select {
		case <-singleCtx.Done():
			ok = false
		case ok = <-singleOKCh:
		}
		singleCtxCancel()
	}
	return
}
