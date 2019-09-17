package lb

import (
	"context"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type HTTPFrontendRoute struct {
	Host    *regexp.Regexp
	Path    *regexp.Regexp
	Backend *HTTPBackend
}

type HTTPFrontendOptions struct {
	Name           string
	Timeout        time.Duration
	DefaultBackend *HTTPBackend
	Routes         []HTTPFrontendRoute
}

func (o *HTTPFrontendOptions) CopyFrom(src *HTTPFrontendOptions) {
	*o = *src
	o.Routes = make([]HTTPFrontendRoute, len(src.Routes))
	copy(o.Routes, src.Routes)
}

type HTTPFrontend struct {
	opts     HTTPFrontendOptions
	forked   bool
	forkedMu sync.Mutex

	promHTTPFrontendReadBytes              prometheus.Counter
	promHTTPFrontendWriteBytes             prometheus.Counter
	promHTTPFrontendRequestsTotal          *prometheus.CounterVec
	promHTTPFrontendRequestDurationSeconds prometheus.ObserverVec
}

func NewHTTPFrontend(opts HTTPFrontendOptions) (f *HTTPFrontend, err error) {
	f, err = f.Fork(opts)
	return
}

func (f *HTTPFrontend) Fork(opts HTTPFrontendOptions) (fn *HTTPFrontend, err error) {
	fn = &HTTPFrontend{}
	fn.opts.CopyFrom(&opts)

	promLabels := map[string]string{"name": fn.opts.Name}
	fn.promHTTPFrontendReadBytes = promHTTPFrontendReadBytes.With(promLabels)
	fn.promHTTPFrontendWriteBytes = promHTTPFrontendWriteBytes.With(promLabels)
	fn.promHTTPFrontendRequestsTotal = promHTTPFrontendRequestsTotal.MustCurryWith(promLabels)
	fn.promHTTPFrontendRequestDurationSeconds = promHTTPFrontendRequestDurationSeconds.MustCurryWith(promLabels)
	return
}

func (f *HTTPFrontend) Close() {
	if !f.setForked(false) {
	}
}

func (f *HTTPFrontend) GetOpts() (opts HTTPFrontendOptions) {
	opts.CopyFrom(&f.opts)
	return
}

func (f *HTTPFrontend) setForked(status bool) bool {
	f.forkedMu.Lock()
	r := f.forked
	f.forked = status
	f.forkedMu.Unlock()
	return r
}

func (f *HTTPFrontend) getBackend(feStatusLine string, feHdr http.Header) (b *HTTPBackend) {
	return f.opts.DefaultBackend
}

func (f *HTTPFrontend) serveAsync(ctx context.Context, okCh chan<- bool, reqDesc *httpReqDesc) {
	ok := false
	defer func() {
		if !ok {
			reqDesc.feConn.Flush()
			reqDesc.feConn.Close()
		}
		okCh <- ok
	}()

	var nr int64

	reqDesc.feStatusLine, reqDesc.feHdr, nr, reqDesc.err = splitHTTPHeader(reqDesc.feConn.Reader)
	if reqDesc.err != nil {
		if nr > 0 {
			debugLogger.Printf("read header from listener %q on frontend %q: %v", reqDesc.feConn.RemoteAddr().String(), f.opts.Name, reqDesc.err)
			reqDesc.feConn.Write([]byte("HTTP/1.0 400 Bad Request\r\n\r\n"))
			return
		}
		reqDesc.err = errGracefulTermination
		return
	}

	if !f.getBackend(reqDesc.feStatusLine, reqDesc.feHdr).serve(ctx, reqDesc) {
		return
	}

	if reqDesc.feConn.Reader.Buffered() != 0 {
		debugLogger.Printf("buffer order error on listener %q on frontend %q", reqDesc.feConn.RemoteAddr().String(), f.opts.Name)
		return
	}

	ok = true
}

func (f *HTTPFrontend) serve(ctx context.Context, reqDesc *httpReqDesc) (ok bool) {
	asyncCtx, asyncCtxCancel := ctx, context.CancelFunc(func() {})
	if f.opts.Timeout > 0 {
		asyncCtx, asyncCtxCancel = context.WithTimeout(asyncCtx, f.opts.Timeout)
	}
	defer asyncCtxCancel()

	// monitoring start
	startTime := time.Now()

	asyncOK := false
	asyncOKCh := make(chan bool, 1)
	go f.serveAsync(asyncCtx, asyncOKCh, reqDesc)
	select {
	case <-asyncCtx.Done():
		reqDesc.feConn.Close()
	case asyncOK = <-asyncOKCh:
	}

	// monitoring end
	if reqDesc.err != errGracefulTermination {
		promMethodCodeLabels := prometheus.Labels{"method": "", "code": ""}
		if reqDesc.feStatusLine != "" {
			ar := strings.SplitN(reqDesc.feStatusLine, " ", 3)
			if len(ar) > 0 {
				promMethodCodeLabels["method"] = ar[0]
			}
		}
		if reqDesc.beStatusLine != "" {
			ar := strings.SplitN(reqDesc.beStatusLine, " ", 3)
			if len(ar) > 1 {
				promMethodCodeLabels["code"] = ar[1]
			}
		}
		f.promHTTPFrontendRequestsTotal.With(promMethodCodeLabels).Inc()
		f.promHTTPFrontendRequestDurationSeconds.With(promMethodCodeLabels).Observe(time.Now().Sub(startTime).Seconds())
	}

	if !asyncOK {
		return
	}

	ok = true
	return
}

func (f *HTTPFrontend) Serve(ctx context.Context, conn net.Conn) {
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(1 * time.Second)
	}
	feConn := newBufConn(conn)
	//debugLogger.Printf("connected %q to listener %q on frontend %q", feConn.RemoteAddr().String(), feConn.LocalAddr().String(), f.opts.Name)
	var readBytes, writeBytes int64
	for done := false; !done; {
		reqDesc := &httpReqDesc{
			feConn: feConn,
		}
		if !f.serve(ctx, reqDesc) {
			done = true
		}
		r, w := feConn.Stats()
		f.promHTTPFrontendReadBytes.Add(float64(r - readBytes))
		f.promHTTPFrontendWriteBytes.Add(float64(w - writeBytes))
		readBytes, writeBytes = r, w
	}
	//debugLogger.Printf("disconnected %q to listener %q on frontend %q", feConn.RemoteAddr().String(), feConn.LocalAddr().String(), f.opts.Name)
	return
}
