package lb

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
)

type HTTPFrontendRoute struct {
	Host    string
	Path    string
	Backend *HTTPBackend

	hostRgx *regexp.Regexp
	pathRgx *regexp.Regexp
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
	patternToRgx := func(pattern string) *regexp.Regexp {
		reg := regexp.QuoteMeta(strings.ToLower(pattern))
		reg = strings.Replace(reg, "\\*", ".*", -1)
		reg = strings.Replace(reg, "\\?", ".", -1)
		reg = "^" + reg + "$"
		return regexp.MustCompile(reg)
	}
	for i := range o.Routes {
		route := &o.Routes[i]
		if route.Host == "" {
			route.Host = "*"
		}
		route.hostRgx = patternToRgx(route.Host)
		if route.Path == "" {
			route.Path = "*"
		}
		route.pathRgx = patternToRgx(route.Path)
	}
}

type HTTPFrontend struct {
	opts            HTTPFrontendOptions
	workerTkr       *time.Ticker
	workerCtx       context.Context
	workerCtxCancel context.CancelFunc
	workerWg        sync.WaitGroup

	promReadBytes              *prometheus.CounterVec
	promWriteBytes             *prometheus.CounterVec
	promRequestsTotal          *prometheus.CounterVec
	promRequestDurationSeconds prometheus.ObserverVec
	promActiveConnections      *prometheus.GaugeVec
}

func NewHTTPFrontend(opts HTTPFrontendOptions) (f *HTTPFrontend, err error) {
	f, err = f.Fork(opts)
	return
}

func (f *HTTPFrontend) Fork(opts HTTPFrontendOptions) (fn *HTTPFrontend, err error) {
	fn = &HTTPFrontend{}
	fn.opts.CopyFrom(&opts)
	fn.workerTkr = time.NewTicker(100 * time.Millisecond)
	fn.workerCtx, fn.workerCtxCancel = context.WithCancel(context.Background())
	fn.workerWg.Add(1)
	go fn.worker(fn.workerCtx)

	promLabels := map[string]string{
		"name": fn.opts.Name,
	}
	fn.promReadBytes = promHTTPFrontendReadBytes.MustCurryWith(promLabels)
	fn.promWriteBytes = promHTTPFrontendWriteBytes.MustCurryWith(promLabels)
	fn.promRequestsTotal = promHTTPFrontendRequestsTotal.MustCurryWith(promLabels)
	fn.promRequestDurationSeconds = promHTTPFrontendRequestDurationSeconds.MustCurryWith(promLabels)
	fn.promActiveConnections = promHTTPFrontendActiveConnections.MustCurryWith(promLabels)

	defer func() {
		if err == nil {
			return
		}
		fn.Close()
		fn = nil
	}()

	return
}

func (f *HTTPFrontend) Close() {
	f.workerTkr.Stop()
	f.workerCtxCancel()
	f.workerWg.Wait()
}

func (f *HTTPFrontend) GetOpts() (opts HTTPFrontendOptions) {
	opts.CopyFrom(&f.opts)
	return
}

func (f *HTTPFrontend) worker(ctx context.Context) {
	for done := false; !done; {
		select {
		case <-f.workerTkr.C:
		case <-ctx.Done():
			done = true
		}
	}
	f.workerWg.Done()
}

func (f *HTTPFrontend) findBackend(reqDesc *httpReqDesc) (b *HTTPBackend) {
	for i := range f.opts.Routes {
		r := &f.opts.Routes[i]
		host := strings.ToLower(reqDesc.feHdr.Get("Host"))
		path := strings.ToLower(uriToPath(reqDesc.feStatusURI))
		if r.hostRgx.MatchString(host) &&
			(r.pathRgx.MatchString(path) || r.pathRgx.MatchString(path+"/")) {
			reqDesc.feHost = r.Host
			reqDesc.fePath = r.Path
			return r.Backend
		}
	}
	return f.opts.DefaultBackend
}

func (f *HTTPFrontend) serveAsync(ctx context.Context, errCh chan<- error, reqDesc *httpReqDesc) {
	var err error
	defer func() {
		if err != nil {
			reqDesc.feConn.Flush()
			reqDesc.feConn.Close()
		}
		errCh <- err
	}()

	var nr int64

	reqDesc.feStatusLine, reqDesc.feHdr, nr, err = splitHTTPHeader(reqDesc.feConn.Reader)
	if err != nil {
		if nr > 0 {
			e := &httpError{
				Cause: err,
				Group: "communication",
				Msg:   fmt.Sprintf("read header from listener %q on frontend %q: %v", reqDesc.feConn.LocalAddr().String(), f.opts.Name, err),
			}
			err = errors.WithStack(e)
			e.PrintDebugLog()
			reqDesc.feConn.Write([]byte("HTTP/1.0 400 Bad Request\r\n\r\n"))
			return
		}
		err = errors.WithStack(errGracefulTermination)
		return
	}
	feStatusLineParts := strings.SplitN(reqDesc.feStatusLine, " ", 3)
	if len(feStatusLineParts) < 3 {
		e := &httpError{
			Cause: nil,
			Group: "protocol",
			Msg:   fmt.Sprintf("status line format error from listener %q on frontend %q", reqDesc.feConn.LocalAddr().String(), f.opts.Name),
		}
		err = errors.WithStack(e)
		e.PrintDebugLog()
		return
	}
	reqDesc.feStatusMethod = strings.ToUpper(feStatusLineParts[0])
	reqDesc.feStatusURI = feStatusLineParts[1]
	reqDesc.feStatusVersion = feStatusLineParts[2]
	if reqDesc.feStatusVersion != "HTTP/1.0" && reqDesc.feStatusVersion != "HTTP/1.1" {
		e := &httpError{
			Cause: nil,
			Group: "protocol",
			Msg:   fmt.Sprintf("HTTP version error from listener %q on frontend %q", reqDesc.feConn.LocalAddr().String(), f.opts.Name),
		}
		err = errors.WithStack(e)
		e.PrintDebugLog()
		return
	}

	if err = f.findBackend(reqDesc).serve(ctx, reqDesc); err != nil {
		return
	}

	if reqDesc.feConn.Reader.Buffered() != 0 {
		e := &httpError{
			Cause: nil,
			Group: "protocol",
			Msg:   fmt.Sprintf("buffer order error on listener %q on frontend %q", reqDesc.feConn.LocalAddr().String(), f.opts.Name),
		}
		err = errors.WithStack(e)
		e.PrintDebugLog()
		return
	}
}

func (f *HTTPFrontend) serve(ctx context.Context, reqDesc *httpReqDesc) (err error) {
	reqDesc.feName = f.opts.Name

	asyncCtx, asyncCtxCancel := ctx, context.CancelFunc(func() {})
	if f.opts.Timeout > 0 {
		asyncCtx, asyncCtxCancel = context.WithTimeout(asyncCtx, f.opts.Timeout)
	}
	defer asyncCtxCancel()

	// monitoring start
	startTime := time.Now()

	asyncErrCh := make(chan error, 1)
	go f.serveAsync(asyncCtx, asyncErrCh, reqDesc)
	select {
	case <-asyncCtx.Done():
		reqDesc.feConn.Flush()
		reqDesc.feConn.Close()
		<-asyncErrCh
		e := &httpError{
			Cause: nil,
			Group: "frontend timeout",
			Msg:   fmt.Sprintf("timeout exceeded on listener %q on frontend %q", reqDesc.feConn.LocalAddr().String(), f.opts.Name),
		}
		err = errors.WithStack(e)
		e.PrintDebugLog()
	case err = <-asyncErrCh:
	}

	// monitoring end
	promLabels := prometheus.Labels{
		"address": reqDesc.feConn.LocalAddr().String(),
		"host":    reqDesc.feHost,
		"path":    reqDesc.fePath,
		"backend": reqDesc.beName,
		"method":  reqDesc.feStatusMethod,
		"code":    reqDesc.beStatusCode,
	}
	r, w := reqDesc.feConn.Stats()
	f.promReadBytes.With(promLabels).Add(float64(r))
	f.promWriteBytes.With(promLabels).Add(float64(w))
	if e := errors.Cause(err); e != errGracefulTermination {
		errDesc := ""
		if e != nil && e != errExpectedEOF {
			if e, ok := e.(*httpError); ok {
				errDesc = e.Group
			} else {
				errDesc = "unknown"
			}
		} else {
			f.promRequestDurationSeconds.With(promLabels).Observe(time.Now().Sub(startTime).Seconds())
		}
		f.promRequestsTotal.MustCurryWith(promLabels).With(prometheus.Labels{"error": errDesc}).Inc()
	}

	return
}

func (f *HTTPFrontend) Serve(ctx context.Context, conn net.Conn) {
	f.promActiveConnections.With(nil).Inc()
	defer f.promActiveConnections.With(nil).Dec()

	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(1 * time.Second)
	}
	feConn := newBufConn(conn)

	//debugLogger.Printf("connected %q to listener %q on frontend %q", feConn.RemoteAddr().String(), feConn.LocalAddr().String(), f.opts.Name)
	for done := false; !done; {
		reqDesc := &httpReqDesc{
			feConn: feConn,
		}
		if e := f.serve(ctx, reqDesc); e != nil {
			done = true
		}
	}
	//debugLogger.Printf("disconnected %q to listener %q on frontend %q", feConn.RemoteAddr().String(), feConn.LocalAddr().String(), f.opts.Name)
	return
}
