package lb

import (
	"context"
	"errors"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type HTTPFrontendRestriction struct {
	Network  *net.IPNet
	Path     string
	Invert   bool
	AndAfter bool

	pathRgx *regexp.Regexp
}

type HTTPFrontendRoute struct {
	Host         string
	Path         string
	Backend      *HTTPBackend
	Restrictions []HTTPFrontendRestriction

	hostRgx *regexp.Regexp
	pathRgx *regexp.Regexp
}

type HTTPFrontendOptions struct {
	Name             string
	Timeout          time.Duration
	KeepAliveTimeout time.Duration
	DefaultBackend   *HTTPBackend
	Routes           []HTTPFrontendRoute
}

func (o *HTTPFrontendOptions) CopyFrom(src *HTTPFrontendOptions) {
	patternToRgx := func(pattern string) *regexp.Regexp {
		reg := regexp.QuoteMeta(strings.ToLower(pattern))
		reg = strings.Replace(reg, "\\*", ".*", -1)
		reg = strings.Replace(reg, "\\?", ".", -1)
		reg = "^" + reg + "$"
		return regexp.MustCompile(reg)
	}

	*o = *src
	o.Routes = make([]HTTPFrontendRoute, len(src.Routes))
	copy(o.Routes, src.Routes)
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

		oldRestrictions := route.Restrictions
		route.Restrictions = make([]HTTPFrontendRestriction, len(oldRestrictions))
		copy(route.Restrictions, oldRestrictions)
		for j := range route.Restrictions {
			restriction := &route.Restrictions[j]
			if restriction.Path == "" {
				restriction.pathRgx = nil
				continue
			}
			restriction.pathRgx = patternToRgx(restriction.Path)
		}
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
	promIdleConnections        *prometheus.GaugeVec
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
		"frontend": fn.opts.Name,
	}
	promLabelsEmpty := prometheus.Labels{
		"host":     "",
		"path":     "",
		"method":   "",
		"backend":  "",
		"server":   "",
		"code":     "",
		"listener": "",
	}

	fn.promReadBytes = promHTTPFrontendReadBytes.MustCurryWith(promLabels)
	fn.promReadBytes.With(promLabelsEmpty).Add(0)

	fn.promWriteBytes = promHTTPFrontendWriteBytes.MustCurryWith(promLabels)
	fn.promWriteBytes.With(promLabelsEmpty).Add(0)

	fn.promRequestsTotal = promHTTPFrontendRequestsTotal.MustCurryWith(promLabels)
	fn.promRequestsTotal.MustCurryWith(prometheus.Labels{"error": ""}).With(promLabelsEmpty).Add(0)

	fn.promRequestDurationSeconds = promHTTPFrontendRequestDurationSeconds.MustCurryWith(promLabels)
	fn.promActiveConnections = promHTTPFrontendActiveConnections.MustCurryWith(promLabels)
	fn.promIdleConnections = promHTTPFrontendIdleConnections.MustCurryWith(promLabels)

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

func (f *HTTPFrontend) isRouteRestricted(reqDesc *httpReqDesc, route *HTTPFrontendRoute, host, path string) bool {
	andOK := true
	for i := range route.Restrictions {
		restriction := &route.Restrictions[i]
		restrictionOK := false
		if tcpAddr, ok := reqDesc.feConn.Conn().RemoteAddr().(*net.TCPAddr); ok && restriction.Network != nil {
			ok := restriction.Network.Contains(tcpAddr.IP)
			if restriction.Invert {
				ok = !ok
			}
			restrictionOK = restrictionOK || ok
		}
		if restriction.pathRgx != nil {
			ok := restriction.pathRgx.MatchString(path) || restriction.pathRgx.MatchString(path+"/")
			if restriction.Invert {
				ok = !ok
			}
			restrictionOK = restrictionOK || ok
		}
		if !restriction.AndAfter {
			if andOK && restrictionOK {
				return true
			}
			andOK = true
		} else {
			andOK = andOK && restrictionOK
		}
	}
	return false
}

func (f *HTTPFrontend) findBackend(reqDesc *httpReqDesc) (b *HTTPBackend) {
	for i := range f.opts.Routes {
		route := &f.opts.Routes[i]
		host := strings.ToLower(reqDesc.feHdr.Get("Host"))
		path := strings.ToLower(uriToPath(reqDesc.feStatusURI))
		if route.hostRgx.MatchString(host) &&
			(route.pathRgx.MatchString(path) || route.pathRgx.MatchString(path+"/")) {
			reqDesc.feHost = route.Host
			reqDesc.fePath = route.Path
			if f.isRouteRestricted(reqDesc, route, host, path) {
				return nil
			}
			return route.Backend
		}
	}
	reqDesc.feHost = "*"
	reqDesc.fePath = "*"
	return f.opts.DefaultBackend
}

func (f *HTTPFrontend) serveAsync(ctx context.Context, errCh chan<- error, reqDesc *httpReqDesc) {
	var err error
	defer func() { errCh <- err }()

	var nr int64

	reqDesc.feStatusLine, reqDesc.feHdr, nr, err = splitHTTPHeader(reqDesc.feConn.Reader)
	if err != nil {
		if nr > 0 {
			debugLogger.Printf("serve error on %s: read header from frontend: %v", reqDesc.FrontendSummary(), err)
			reqDesc.feConn.Write([]byte(httpBadRequest))
			return
		}
		err = errGracefulTermination
		return
	}
	feStatusLineParts := strings.SplitN(reqDesc.feStatusLine, " ", 3)
	if len(feStatusLineParts) < 3 {
		err = errHTTPStatusLineFormat
		debugLogger.Printf("serve error on %s: read header from frontend: %v", reqDesc.FrontendSummary(), err)
		reqDesc.feConn.Write([]byte(httpBadRequest))
		return
	}
	reqDesc.feStatusMethod = strings.ToUpper(feStatusLineParts[0])
	reqDesc.feStatusURI = feStatusLineParts[1]
	reqDesc.feStatusVersion = strings.ToUpper(feStatusLineParts[2])
	if reqDesc.feStatusVersion != "HTTP/1.0" && reqDesc.feStatusVersion != "HTTP/1.1" {
		err = errHTTPVersion
		debugLogger.Printf("serve error on %s: read header from frontend: %v", reqDesc.FrontendSummary(), err)
		reqDesc.feConn.Write([]byte(httpVersionNotSupported))
		return
	}

	b := f.findBackend(reqDesc)
	if b == nil {
		err = errHTTPRestrictedRequest
		debugLogger.Printf("serve error on %s: %v", reqDesc.FrontendSummary(), err)
		reqDesc.feConn.Write([]byte(httpForbidden))
		return
	}
	reqDesc.beName = b.opts.Name
	if err = b.serve(ctx, reqDesc); err != nil {
		return
	}

	// it can be happened when client has been started new request before ending request body transfer!
	if reqDesc.feConn.Reader.Buffered() != 0 {
		err = errHTTPBufferOrder
		debugLogger.Printf("serve error on %s: %v", reqDesc.FrontendSummary(), err)
		return
	}
}

func (f *HTTPFrontend) serve(ctx context.Context, reqDesc *httpReqDesc) (err error) {
	asyncCtx, asyncCtxCancel := ctx, context.CancelFunc(func() { /* null function */ })
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
		err = errHTTPFrontendTimeout
		debugLogger.Printf("serve error on %s: %v", reqDesc.FrontendSummary(), err)
	case err = <-asyncErrCh:
		if err != nil {
			reqDesc.feConn.Flush()
			reqDesc.feConn.Close()
		}
	}

	// monitoring end
	promLabels := prometheus.Labels{
		"host":     reqDesc.feHost,
		"path":     reqDesc.fePath,
		"method":   reqDesc.feStatusMethod,
		"backend":  reqDesc.beName,
		"server":   reqDesc.beServer,
		"code":     reqDesc.beStatusCode,
		"listener": reqDesc.leName,
	}
	r, w := reqDesc.feConn.Stats()
	f.promReadBytes.With(promLabels).Add(float64(r))
	f.promWriteBytes.With(promLabels).Add(float64(w))
	if !errors.Is(err, errGracefulTermination) {
		errDesc := ""
		if err != nil && !errors.Is(err, errExpectedEOF) {
			if e, ok := err.(*httpError); ok {
				errDesc = e.Group
			} else {
				errDesc = "unknown"
				debugLogger.Printf("unknown error on listener %q on frontend %q. may be it is a bug: %v", reqDesc.leName, reqDesc.feName, err)
			}
		} else {
			f.promRequestDurationSeconds.With(promLabels).Observe(time.Now().Sub(startTime).Seconds())
		}
		f.promRequestsTotal.MustCurryWith(promLabels).With(prometheus.Labels{"error": errDesc}).Inc()
	}

	return
}

func (f *HTTPFrontend) Serve(ctx context.Context, l *Listener, conn net.Conn) {
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(1 * time.Second)
	}
	feConn := newBufConn(conn)

	promLabels := prometheus.Labels{
		"listener": l.opts.Name,
	}

	for done := false; !done; {
		f.promIdleConnections.With(promLabels).Inc()

		readCh := make(chan error, 1)
		go func() {
			_, e := feConn.Reader.Peek(1)
			f.promIdleConnections.With(promLabels).Dec()
			readCh <- e
		}()

		keepAliveCtx, keepAliveCtxCancel := ctx, context.CancelFunc(func() { /* null function */ })
		if f.opts.KeepAliveTimeout > 0 {
			keepAliveCtx, keepAliveCtxCancel = context.WithTimeout(keepAliveCtx, f.opts.KeepAliveTimeout)
		}

		select {
		case e := <-readCh:
			if e != nil {
				done = true
				break
			}
			f.promActiveConnections.With(promLabels).Inc()
			reqDesc := &httpReqDesc{
				leName: l.opts.Name,
				feName: f.opts.Name,
				feConn: feConn,
			}
			if e := f.serve(ctx, reqDesc); e != nil {
				done = true
			}
			f.promActiveConnections.With(promLabels).Dec()
		case <-keepAliveCtx.Done():
			done = true
		}

		keepAliveCtxCancel()
	}

	return
}
