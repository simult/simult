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

type FrontendOptions struct {
	Timeout        time.Duration
	DefaultBackend *Backend
}

func (o *FrontendOptions) CopyFrom(src *FrontendOptions) {
	*o = *src
}

type Frontend struct {
	opts   FrontendOptions
	optsMu sync.RWMutex
}

func NewFrontend(opts FrontendOptions) (f *Frontend) {
	f = &Frontend{}
	f.opts.CopyFrom(&opts)
	return
}

func (f *Frontend) GetOpts() (opts FrontendOptions) {
	f.optsMu.RLock()
	opts.CopyFrom(&f.opts)
	f.optsMu.RUnlock()
	return
}

func (f *Frontend) getBackend(feStatusLine string, feHdr http.Header) (b *Backend) {
	return f.opts.DefaultBackend
}

func (f *Frontend) beServe(ctx context.Context, okCh chan<- bool, feConn *bufConn, feStatusLine string, feHdr http.Header, beConn *bufConn) {
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

func (f *Frontend) feServe(ctx context.Context, okCh chan<- bool, feConn *bufConn) {
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

	b := f.getBackend(feStatusLine, feHdr)
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
	go f.beServe(beCtx, beOKCh, feConn, feStatusLine, feHdr, beConn)
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

func (f *Frontend) Serve(ctx context.Context, conn net.Conn) {
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(1 * time.Second)
	}
	feConn := newBufConn(conn)
	DebugLogger.Printf("connected %v to frontend %v\n", feConn.RemoteAddr(), feConn.LocalAddr())
	for {
		fOpts := f.GetOpts()
		feCtx, feCtxCancel := ctx, context.CancelFunc(func() {})
		if fOpts.Timeout > 0 {
			feCtx, feCtxCancel = context.WithTimeout(feCtx, fOpts.Timeout)
		}
		feOK := false
		feOKCh := make(chan bool, 1)
		go f.feServe(feCtx, feOKCh, feConn)
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
