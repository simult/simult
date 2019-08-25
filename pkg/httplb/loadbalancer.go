package httplb

import (
	"context"
	"net"
	"sync"
	"time"
)

const (
	maxLineLen = 1 * 1024 * 1024
)

type LoadBalancerOptions struct {
	Timeout        time.Duration
	DefaultBackend *Backend
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

func (l *LoadBalancer) getBackend(feHls []headerLine) (b *Backend) {
	return l.opts.DefaultBackend
}

func (l *LoadBalancer) serveSingle(ctx context.Context, okCh chan<- bool, feConn *bufConn) {
	defer feConn.Flush()
	feHls, feHdr, err := splitHeader(feConn.Reader)
	if err != nil || len(feHls) <= 0 {
		feConn.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
		okCh <- false
		return
	}

	b := l.getBackend(feHls)
	bs := b.FindServer(ctx)
	if bs == nil {
		feConn.Write([]byte("HTTP/1.1 503 Service Unavailable\r\n\r\n"))
		okCh <- false
		return
	}

	beConn, err := bs.ConnAcquire(ctx)
	if err != nil {
		feConn.Write([]byte("HTTP/1.1 503 Service Unavailable\r\n\r\n"))
		okCh <- false
		return
	}

	_, err = writeHeaderLines(beConn.Writer, feHls)
	if err != nil {
		beConn.Close()
		okCh <- false
		return
	}

	ingressOKCh := make(chan bool, 1)
	go func() {
		var err error
		_, err = copyBody(beConn.Writer, feConn.Reader, feHdr, true)
		ingressOKCh <- err == nil
	}()

	beHls, beHdr, err := splitHeader(beConn.Reader)
	if err != nil || len(beHls) <= 0 {
		beConn.Close()
		okCh <- false
		return
	}

	_, err = writeHeaderLines(feConn.Writer, beHls)
	if err != nil {
		beConn.Close()
		okCh <- false
		return
	}

	_, err = copyBody(feConn.Writer, beConn.Reader, beHdr, false)
	if err != nil {
		beConn.Close()
		okCh <- false
		return
	}

	if ok := <-ingressOKCh; !ok {
		beConn.Close()
		okCh <- false
		return
	}

	bs.ConnRelease(beConn)
	okCh <- true
	return
}

func (l *LoadBalancer) Serve(ctx context.Context, conn net.Conn) {
	feConn := newBufConn(conn)
	defer feConn.Close()
	ok := true
	for ok {
		l.optsMu.RLock()
		opts := l.opts
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
