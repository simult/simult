package lb

import (
	"context"
	"errors"
	"net"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/goinsane/xlog"
	"github.com/prometheus/client_golang/prometheus"
)

// HTTPFrontendRestriction defines HTTP frontend restriction
type HTTPFrontendRestriction struct {
	Network  *net.IPNet
	Path     string
	Invert   bool
	AndAfter bool

	pathRgx *regexp.Regexp
}

// HTTPFrontendRoute defines HTTP frontend route
type HTTPFrontendRoute struct {
	Host         string
	Path         string
	Backend      *HTTPBackend
	Restrictions []HTTPFrontendRestriction

	hostRgx *regexp.Regexp
	pathRgx *regexp.Regexp
}

// HTTPFrontendOptions holds HTTPFrontend options
type HTTPFrontendOptions struct {
	Name             string
	MaxConn          int
	MaxIdleConn      int
	Timeout          time.Duration
	RequestTimeout   time.Duration
	KeepAliveTimeout time.Duration
	DefaultBackend   *HTTPBackend
	Routes           []HTTPFrontendRoute
}

// CopyFrom sets the underlying HTTPFrontendOptions by given HTTPFrontendOptions
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

// HTTPFrontend implements a frontend for HTTP
type HTTPFrontend struct {
	opts            HTTPFrontendOptions
	activeConnCount int64
	idleConnCount   int64
	totalConnCount  int64

	workerTkr *time.Ticker
	workerWg  sync.WaitGroup

	ctx       context.Context
	ctxCancel context.CancelFunc

	promReadBytes               *prometheus.CounterVec
	promWriteBytes              *prometheus.CounterVec
	promRequestsTotal           *prometheus.CounterVec
	promRequestDurationSeconds  prometheus.ObserverVec
	promConnectionsTotal        *prometheus.CounterVec
	promDroppedConnectionsTotal *prometheus.CounterVec
	promActiveConnections       *prometheus.GaugeVec
	promIdleConnections         *prometheus.GaugeVec
}

// NewHTTPFrontend creates a new HTTPFrontend by given options
func NewHTTPFrontend(opts HTTPFrontendOptions) (f *HTTPFrontend, err error) {
	f, err = f.Fork(opts)
	return
}

// Fork forkes a HTTPFrontend and its own members by given options
func (f *HTTPFrontend) Fork(opts HTTPFrontendOptions) (fn *HTTPFrontend, err error) {
	fn = &HTTPFrontend{}
	fn.opts.CopyFrom(&opts)
	fn.workerTkr = time.NewTicker(100 * time.Millisecond)
	fn.ctx, fn.ctxCancel = context.WithCancel(context.Background())

	promLabels := prometheus.Labels{
		"frontend": fn.opts.Name,
	}
	fn.promReadBytes = promHTTPFrontendReadBytes.MustCurryWith(promLabels)
	fn.promWriteBytes = promHTTPFrontendWriteBytes.MustCurryWith(promLabels)
	fn.promRequestsTotal = promHTTPFrontendRequestsTotal.MustCurryWith(promLabels)
	fn.promRequestDurationSeconds = promHTTPFrontendRequestDurationSeconds.MustCurryWith(promLabels)
	fn.promConnectionsTotal = promHTTPFrontendConnectionsTotal.MustCurryWith(promLabels)
	fn.promDroppedConnectionsTotal = promHTTPFrontendDroppedConnectionsTotal.MustCurryWith(promLabels)
	fn.promActiveConnections = promHTTPFrontendActiveConnections.MustCurryWith(promLabels)
	fn.promIdleConnections = promHTTPFrontendIdleConnections.MustCurryWith(promLabels)

	defer func() {
		if err == nil {
			return
		}
		fn.Close()
		fn = nil
	}()

	fn.workerWg.Add(1)
	go fn.worker()

	return
}

// Close closes the HTTPFrontend and its own members
func (f *HTTPFrontend) Close() {
	f.ctxCancel()
	f.workerTkr.Stop()
	f.workerWg.Wait()
}

// GetOpts returns a copy of underlying HTTPFrontend's options
func (f *HTTPFrontend) GetOpts() (opts HTTPFrontendOptions) {
	opts.CopyFrom(&f.opts)
	return
}

func (f *HTTPFrontend) worker() {
	for done := false; !done; {
		select {
		case <-f.workerTkr.C:
		case <-f.ctx.Done():
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
		host := strings.ToLower(reqDesc.feURL.Hostname())
		path := strings.ToLower(normalizePath(reqDesc.feURL.Path))
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

	if f.opts.RequestTimeout > 0 {
		reqDesc.feConn.SetReadDeadline(time.Now().Add(f.opts.RequestTimeout))
	}
	reqDesc.feStatusLine, reqDesc.feHdr, _, err = splitHTTPHeader(reqDesc.feConn.Reader)
	if err != nil {
		if e := (*net.OpError)(nil); errors.As(err, &e) && e.Timeout() {
			err = errHTTPRequestTimeout
			xlog.V(100).Debugf("serve error on %s: read header from frontend: %v", reqDesc.FrontendSummary(), err)
			reqDesc.feConn.Write([]byte(httpRequestTimeout))
			return
		}
		xlog.V(100).Debugf("serve error on %s: read header from frontend: %v", reqDesc.FrontendSummary(), err)
		reqDesc.feConn.Write([]byte(httpBadRequest))
		return
	}
	reqDesc.feConn.SetReadDeadline(time.Time{})

	feStatusLineParts := strings.SplitN(reqDesc.feStatusLine, " ", 3)
	if len(feStatusLineParts) < 3 {
		err = errHTTPStatusLine
		xlog.V(100).Debugf("serve error on %s: %v", reqDesc.FrontendSummary(), err)
		reqDesc.feConn.Write([]byte(httpBadRequest))
		return
	}

	reqDesc.feStatusMethod = strings.ToUpper(feStatusLineParts[0])

	reqDesc.feStatusURI = feStatusLineParts[1]
	if reqDesc.feStatusURI == "" || reqDesc.feStatusURI[0] != '/' {
		err = errHTTPStatusURI
		xlog.V(100).Debugf("serve error on %s: %v", reqDesc.FrontendSummary(), err)
		reqDesc.feConn.Write([]byte(httpBadRequest))
	}

	reqDesc.feStatusVersion = strings.ToUpper(feStatusLineParts[2])
	if reqDesc.feStatusVersion != "HTTP/1.0" && reqDesc.feStatusVersion != "HTTP/1.1" {
		err = errHTTPStatusVersion
		xlog.V(100).Debugf("serve error on %s: %v", reqDesc.FrontendSummary(), err)
		reqDesc.feConn.Write([]byte(httpVersionNotSupported))
		return
	}

	reqDesc.feStatusMethodGrouped = groupHTTPStatusMethod(reqDesc.feStatusMethod)

	scheme := "http"
	if reqDesc.leTLS {
		scheme = "https"
	}
	host := reqDesc.feHdr.Get("Host")
	if host != "" {
		if strings.IndexByte(host, '/') < 0 {
			reqDesc.feURL, err = url.Parse(scheme + "://" + host + reqDesc.feStatusURI)
		} else {
			err = errors.New("invalid host")
		}
	} else {
		reqDesc.feURL, err = url.Parse(scheme + "://host" + reqDesc.feStatusURI)
		reqDesc.feURL.Host = ""
	}
	if err != nil {
		err = newfHTTPError(httpErrGroupProtocol, "parse full URL error: %w", err)
		xlog.V(100).Debugf("serve error on %s: %v", reqDesc.FrontendSummary(), err)
		reqDesc.feConn.Write([]byte(httpBadRequest))
	}

	reqDesc.feCookies = readCookies(reqDesc.feHdr, "")
	if tcpAddr, ok := reqDesc.feConn.RemoteAddr().(*net.TCPAddr); ok {
		reqDesc.feRemoteIP = tcpAddr.IP.String()
	}

	reqDesc.feRealIP = reqDesc.feHdr.Get("X-Real-IP")
	if reqDesc.feRealIP == "" {
		reqDesc.feRealIP = strings.SplitN(reqDesc.feHdr.Get("X-Forwarded-For"), ",", 2)[0]
		reqDesc.feRealIP = strings.TrimSpace(reqDesc.feRealIP)
		if reqDesc.feRealIP == "" {
			reqDesc.feRealIP = reqDesc.feRemoteIP
		}
	}

	b := f.findBackend(reqDesc)
	if b == nil {
		err = errHTTPRestrictedRequest
		xlog.V(100).Debugf("serve error on %s: %v", reqDesc.FrontendSummary(), err)
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
		xlog.V(100).Debugf("serve error on %s: %v", reqDesc.FrontendSummary(), err)
		return
	}
}

func (f *HTTPFrontend) serve(ctx context.Context, reqDesc *httpReqDesc) (err error) {
	if f.opts.Timeout > 0 {
		var ctxCancel context.CancelFunc
		ctx, ctxCancel = context.WithTimeout(ctx, f.opts.Timeout)
		defer ctxCancel()
	}

	// monitoring start
	startTime := time.Now()

	asyncErrCh := make(chan error, 1)
	go f.serveAsync(ctx, asyncErrCh, reqDesc)
	select {
	case <-ctx.Done():
		atomic.CompareAndSwapUint32(&reqDesc.isTransferErrLogged, 0, 1)
		err = errHTTPFrontendTimeout
		xlog.V(100).Debugf("serve error on %s: %v", reqDesc.FrontendSummary(), err)
		reqDesc.feConn.Flush()
		reqDesc.feConn.Close()
		<-asyncErrCh
	case err = <-asyncErrCh:
		if err != nil {
			reqDesc.feConn.Flush()
			reqDesc.feConn.Close()
		}
	}
	// resetting and reading stats
	r, w := reqDesc.feConn.Stats()

	// monitoring end
	promLabels := prometheus.Labels{
		"host":     reqDesc.feHost,
		"path":     reqDesc.fePath,
		"method":   reqDesc.feStatusMethodGrouped,
		"backend":  reqDesc.beName,
		"server":   reqDesc.beServer,
		"code":     reqDesc.beStatusCodeGrouped,
		"listener": reqDesc.leName,
	}
	f.promReadBytes.With(promLabels).Add(float64(r))
	f.promWriteBytes.With(promLabels).Add(float64(w))
	errDesc := ""
	if err != nil && !errors.Is(err, errExpectedEOF) {
		if e := (*httpError)(nil); errors.As(err, &e) {
			errDesc = e.Group
		} else {
			errDesc = "unknown"
			xlog.V(100).Debugf("unknown error on listener %q on frontend %q. may be it is a bug: %v", reqDesc.leName, reqDesc.feName, err)
		}
	} else {
		f.promRequestDurationSeconds.With(promLabels).Observe(time.Now().Sub(startTime).Seconds())
	}
	f.promRequestsTotal.MustCurryWith(promLabels).With(prometheus.Labels{"error": errDesc}).Inc()

	return
}

// Serve implements Frontend's Serve method
func (f *HTTPFrontend) Serve(ctx context.Context, l *Listener, conn net.Conn) {
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(1 * time.Second)
	}
	feConn := newBufConn(conn)
	defer feConn.Flush()
	xlog.V(200).Debugf("connected client %q to listener %q on frontend %q", feConn.RemoteAddr().String(), l.opts.Name, f.opts.Name)
	defer xlog.V(200).Debugf("disconnected client %q from listener %q on frontend %q", feConn.RemoteAddr().String(), l.opts.Name, f.opts.Name)

	promLabels := prometheus.Labels{
		"listener": l.opts.Name,
	}
	if f.opts.MaxConn > 0 && f.totalConnCount >= int64(f.opts.MaxConn) {
		f.promDroppedConnectionsTotal.With(promLabels).Inc()
		xlog.V(100).Debugf("serve error on %s: %v", (&httpReqDesc{
			leName: l.opts.Name,
			feName: f.opts.Name,
			feConn: feConn,
		}).FrontendSummary(), errHTTPFrontendExhausted)
		return
	}
	atomic.AddInt64(&f.totalConnCount, 1)
	defer atomic.AddInt64(&f.totalConnCount, -1)

	f.promConnectionsTotal.With(promLabels).Inc()
	for reqCount, done := 0, false; !done; reqCount++ {
		atomic.AddInt64(&f.idleConnCount, 1)
		f.promIdleConnections.With(promLabels).Set(float64(f.idleConnCount))

		readCh := make(chan error, 1)
		go func() {
			_, e := feConn.Reader.Peek(1)
			atomic.AddInt64(&f.idleConnCount, -1)
			f.promIdleConnections.With(promLabels).Set(float64(f.idleConnCount))
			readCh <- e
		}()

		timeoutCtx, timeoutCtxCancel := ctx, context.CancelFunc(func() { /* null function */ })
		if reqCount > 0 {
			if f.opts.KeepAliveTimeout > 0 {
				timeoutCtx, timeoutCtxCancel = context.WithTimeout(timeoutCtx, f.opts.KeepAliveTimeout)
			}
		} else {
			if f.opts.RequestTimeout > 0 {
				timeoutCtx, timeoutCtxCancel = context.WithTimeout(timeoutCtx, f.opts.RequestTimeout)
			}
		}

		select {
		case e := <-readCh:
			if e != nil {
				done = true
				break
			}
			atomic.AddInt64(&f.activeConnCount, 1)
			f.promActiveConnections.With(promLabels).Set(float64(f.activeConnCount))
			reqDesc := &httpReqDesc{
				leName: l.opts.Name,
				leTLS:  l.opts.TLSConfig != nil,
				feName: f.opts.Name,
				feConn: feConn,
			}
			reqDesc.leHost, reqDesc.lePort = splitHostPort(l.opts.Address)
			if e := f.serve(ctx, reqDesc); e != nil {
				done = true
			}
			atomic.AddInt64(&f.activeConnCount, -1)
			f.promActiveConnections.With(promLabels).Set(float64(f.activeConnCount))
			if f.opts.MaxIdleConn > 0 && f.idleConnCount >= int64(f.opts.MaxIdleConn) {
				done = true
			}
		case <-timeoutCtx.Done():
			feConn.Write([]byte(httpRequestTimeout))
			done = true
		}

		timeoutCtxCancel()
	}

	return
}
