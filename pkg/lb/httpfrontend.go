package lb

import (
	"context"
	"errors"
	"io"
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
	Backup       *HTTPBackend
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
	MaxKeepAliveReqs int
	KeepAliveTimeout time.Duration
	DefaultBackend   *HTTPBackend
	DefaultBackup    *HTTPBackend
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
	opts             HTTPFrontendOptions
	activeConnCount  int64
	idleConnCount    int64
	waitingConnCount int64
	totalConnCount   int64

	workerTkr *time.Ticker
	workerWg  sync.WaitGroup

	ctx       context.Context
	ctxCancel context.CancelFunc

	promReadBytes              *prometheus.CounterVec
	promWriteBytes             *prometheus.CounterVec
	promRequestsTotal          *prometheus.CounterVec
	promRequestDurationSeconds prometheus.ObserverVec
	promConnectionsTotal       *prometheus.CounterVec
	promActiveConnections      *prometheus.GaugeVec
	promIdleConnections        *prometheus.GaugeVec
	promWaitingConnections     *prometheus.GaugeVec
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
	fn.promActiveConnections = promHTTPFrontendActiveConnections.MustCurryWith(promLabels)
	fn.promIdleConnections = promHTTPFrontendIdleConnections.MustCurryWith(promLabels)
	fn.promWaitingConnections = promHTTPFrontendWaitingConnections.MustCurryWith(promLabels)

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

func (f *HTTPFrontend) findBackend(reqDesc *httpReqDesc) (b *HTTPBackend, bb *HTTPBackend) {
	for i := range f.opts.Routes {
		route := &f.opts.Routes[i]
		host := strings.ToLower(reqDesc.feURL.Hostname())
		path := strings.ToLower(normalizePath(reqDesc.feURL.Path))
		if route.hostRgx.MatchString(host) &&
			(route.pathRgx.MatchString(path) || route.pathRgx.MatchString(path+"/")) {
			reqDesc.feHost = route.Host
			reqDesc.fePath = route.Path
			if f.isRouteRestricted(reqDesc, route, host, path) {
				return nil, nil
			}
			return route.Backend, route.Backup
		}
	}
	reqDesc.feHost = "*"
	reqDesc.fePath = "*"
	return f.opts.DefaultBackend, f.opts.DefaultBackup
}

func (f *HTTPFrontend) serveAsync(ctx context.Context, errCh chan<- error, reqDesc *httpReqDesc) {
	var err error
	defer func() { errCh <- err }()

	reqDesc.feStatusLine, reqDesc.feHdr, _, err = splitHTTPHeader(reqDesc.feConn.Reader)
	if err != nil {
		if e := (*net.OpError)(nil); reqDesc.reqIdx <= 0 && errors.As(err, &e) && e.Timeout() {
			err = wrapHTTPError(httpErrGroupRequestTimeout, err)
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
	/*if !strings.HasPrefix(reqDesc.feStatusURI, "/") {
		err = errHTTPStatusURI
		xlog.V(100).Debugf("serve error on %s: %v", reqDesc.FrontendSummary(), err)
		reqDesc.feConn.Write([]byte(httpBadRequest))
		return
	}*/

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
	uri := strings.TrimPrefix(reqDesc.feStatusURI, "/")
	uriHasPrefix := uri != reqDesc.feStatusURI
	if host != "" {
		if strings.IndexByte(host, '/') < 0 {
			reqDesc.feURL, err = url.Parse(scheme + "://" + host + "/" + uri)
		} else {
			err = errors.New("invalid host")
		}
	} else {
		reqDesc.feURL, err = url.Parse(scheme + "://host/" + uri)
		if err == nil {
			reqDesc.feURL.Host = ""
		}
	}
	if err != nil {
		err = newfHTTPError(httpErrGroupProtocol, "parse full URL error: %w", err)
		xlog.V(100).Debugf("serve error on %s: %v", reqDesc.FrontendSummary(), err)
		reqDesc.feConn.Write([]byte(httpBadRequest))
		return
	}
	if !uriHasPrefix {
		reqDesc.feURL.Path = strings.TrimPrefix(reqDesc.feURL.Path, "/")
		reqDesc.feURL.RawPath = strings.TrimPrefix(reqDesc.feURL.RawPath, "/")
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

	b, bb := f.findBackend(reqDesc)
	if b == nil {
		err = errHTTPRestrictedRequest
		xlog.V(100).Debugf("serve error on %s: %v", reqDesc.FrontendSummary(), err)
		reqDesc.feConn.Write([]byte(httpForbidden))
		return
	}
	reqDesc.beFinal = bb == nil
	reqDesc.beName = b.opts.Name
	if err = b.serve(ctx, reqDesc); err != nil {
		if bb == nil || reqDesc.beFinal {
			return
		}

		e := err.(*httpError)
		promLabels := prometheus.Labels{
			"host":     reqDesc.feHost,
			"path":     reqDesc.fePath,
			"method":   reqDesc.feStatusMethodGrouped,
			"backend":  reqDesc.beName,
			"server":   reqDesc.beServer,
			"code":     reqDesc.beStatusCodeGrouped,
			"listener": reqDesc.leName,
			"error":    "dropped: " + e.Group,
		}
		f.promRequestsTotal.With(promLabels).Inc()

		reqDesc.beFinal = true
		reqDesc.beName = bb.opts.Name
		reqDesc.beServer = ""
		reqDesc.beConn = nil
		err = bb.serve(ctx, reqDesc)
		if err != nil {
			return
		}
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
		tcpConn.SetKeepAlivePeriod(5 * time.Second)
	}
	feConn := newBufConn(conn)
	defer feConn.Flush()
	xlog.V(200).Debugf("connected client %q to listener %q on frontend %q", feConn.RemoteAddr().String(), l.opts.Name, f.opts.Name)
	defer xlog.V(200).Debugf("disconnected client %q from listener %q on frontend %q", feConn.RemoteAddr().String(), l.opts.Name, f.opts.Name)

	promLabels := prometheus.Labels{
		"listener": l.opts.Name,
	}
	f.promConnectionsTotal.With(promLabels).Inc()

	if f.opts.MaxConn > 0 && f.totalConnCount >= int64(f.opts.MaxConn) {
		err := errHTTPFrontendExhausted
		xlog.V(100).Debugf("serve error on %s: %v", (&httpReqDesc{
			leName: l.opts.Name,
			feName: f.opts.Name,
			feConn: feConn,
		}).FrontendSummary(), err)
		e := err.(*httpError)
		promLabels := prometheus.Labels{
			"host":     "",
			"path":     "",
			"method":   "",
			"backend":  "",
			"server":   "",
			"code":     "",
			"listener": l.opts.Name,
			"error":    e.Group,
		}
		f.promRequestsTotal.With(promLabels).Inc()
		return
	}
	atomic.AddInt64(&f.totalConnCount, 1)
	defer atomic.AddInt64(&f.totalConnCount, -1)

	for reqIdx, done := 0, false; !done; reqIdx++ {
		if reqIdx > 0 {
			atomic.AddInt64(&f.idleConnCount, 1)
			f.promIdleConnections.With(promLabels).Inc()
		} else {
			atomic.AddInt64(&f.waitingConnCount, 1)
			f.promWaitingConnections.With(promLabels).Inc()
		}

		readErrCh := make(chan error, 1)
		go func(reqIdx int) {
			if reqIdx <= 0 && f.opts.RequestTimeout > 0 {
				feConn.SetReadDeadline(time.Now().Add(f.opts.RequestTimeout))
			}
			_, e := feConn.Reader.Peek(1)
			if reqIdx > 0 {
				atomic.AddInt64(&f.idleConnCount, -1)
				f.promIdleConnections.With(promLabels).Dec()
			} else {
				atomic.AddInt64(&f.waitingConnCount, -1)
				f.promWaitingConnections.With(promLabels).Dec()
			}
			readErrCh <- e
		}(reqIdx)

		ctx, ctxCancel := ctx, context.CancelFunc(func() { /* null function */ })
		if reqIdx > 0 && f.opts.KeepAliveTimeout > 0 {
			ctx, ctxCancel = context.WithTimeout(ctx, f.opts.KeepAliveTimeout)
		}

		select {
		case err := <-readErrCh:
			if err != nil {
				if !errors.Is(err, io.EOF) {
					if e := (*net.OpError)(nil); reqIdx <= 0 && errors.As(err, &e) && e.Timeout() {
						err = wrapHTTPError(httpErrGroupRequestTimeout, err)
						xlog.V(100).Debugf("serve error: read first byte from frontend: %v", err)
						feConn.Write([]byte(httpRequestTimeout))
					} else {
						err = wrapHTTPError(httpErrGroupCommunication, err)
						xlog.V(100).Debugf("serve error: read first byte from frontend: %v", err)
					}
					e := err.(*httpError)
					promLabels := prometheus.Labels{
						"host":     "",
						"path":     "",
						"method":   "",
						"backend":  "",
						"server":   "",
						"code":     "",
						"listener": l.opts.Name,
						"error":    e.Group,
					}
					f.promRequestsTotal.With(promLabels).Inc()
				}
				done = true
				break
			}
			atomic.AddInt64(&f.activeConnCount, 1)
			f.promActiveConnections.With(promLabels).Inc()
			reqDesc := &httpReqDesc{
				reqIdx: reqIdx,
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
			f.promActiveConnections.With(promLabels).Dec()
			if (f.opts.MaxIdleConn > 0 && f.idleConnCount >= int64(f.opts.MaxIdleConn)) || (f.opts.MaxKeepAliveReqs >= 0 && reqIdx >= f.opts.MaxKeepAliveReqs) {
				done = true
			}
		case <-ctx.Done():
			done = true
		}

		ctxCancel()
	}

	return
}
