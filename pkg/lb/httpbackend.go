package lb

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/simult/server/pkg/hc"
)

type HTTPBackendOptions struct {
	Name                string
	Timeout             time.Duration
	ReqHeader           http.Header
	HealthCheckHTTPOpts *hc.HTTPCheckOptions
	Servers             []string
}

func (o *HTTPBackendOptions) CopyFrom(src *HTTPBackendOptions) {
	*o = *src
	o.ReqHeader = make(http.Header, len(src.ReqHeader))
	for k, v := range src.ReqHeader {
		nv := make([]string, len(v))
		copy(nv, v)
		o.ReqHeader[k] = nv
	}
	o.Servers = make([]string, len(src.Servers))
	copy(o.Servers, src.Servers)
}

type HTTPBackend struct {
	opts            HTTPBackendOptions
	workerTkr       *time.Ticker
	workerCtx       context.Context
	workerCtxCancel context.CancelFunc
	workerWg        sync.WaitGroup
	bss             map[string]*backendServer
	bssMu           sync.RWMutex
	rnd             *rand.Rand

	bssList   []*backendServer
	bssListMu sync.RWMutex

	promReadBytes              *prometheus.CounterVec
	promWriteBytes             *prometheus.CounterVec
	promRequestsTotal          *prometheus.CounterVec
	promRequestDurationSeconds prometheus.ObserverVec
	promTimeToFirstByteSeconds prometheus.ObserverVec
	promActiveConnections      *prometheus.GaugeVec
	promServerHealthy          *prometheus.GaugeVec
}

func NewHTTPBackend(opts HTTPBackendOptions) (b *HTTPBackend, err error) {
	b, err = b.Fork(opts)
	return
}

func (b *HTTPBackend) Fork(opts HTTPBackendOptions) (bn *HTTPBackend, err error) {
	bn = &HTTPBackend{}
	bn.opts.CopyFrom(&opts)
	bn.workerTkr = time.NewTicker(100 * time.Millisecond)
	bn.workerCtx, bn.workerCtxCancel = context.WithCancel(context.Background())
	bn.workerWg.Add(1)
	go bn.worker(bn.workerCtx)
	bn.bss = make(map[string]*backendServer, len(opts.Servers))
	bn.rnd = rand.New(rand.NewSource(time.Now().Unix()))

	promLabels := map[string]string{
		"backend": bn.opts.Name,
	}
	promLabels2 := prometheus.Labels{
		"server":   "",
		"code":     "",
		"frontend": "",
		"address":  "",
		"host":     "",
		"path":     "",
		"method":   "",
	}

	bn.promReadBytes = promHTTPBackendReadBytes.MustCurryWith(promLabels)
	bn.promReadBytes.With(promLabels2).Add(0)

	bn.promWriteBytes = promHTTPBackendWriteBytes.MustCurryWith(promLabels)
	bn.promWriteBytes.With(promLabels2).Add(0)

	bn.promRequestsTotal = promHTTPBackendRequestsTotal.MustCurryWith(promLabels)
	bn.promRequestsTotal.MustCurryWith(prometheus.Labels{"error": ""}).With(promLabels2).Add(0)

	bn.promRequestDurationSeconds = promHTTPBackendRequestDurationSeconds.MustCurryWith(promLabels)
	bn.promTimeToFirstByteSeconds = promHTTPBackendTimeToFirstByteSeconds.MustCurryWith(promLabels)
	bn.promActiveConnections = promHTTPBackendActiveConnections.MustCurryWith(promLabels)
	bn.promServerHealthy = promHTTPBackendServerHealthy.MustCurryWith(promLabels)

	bn.updateBssList()

	defer func() {
		if err == nil {
			return
		}
		bn.Close()
		bn = nil
	}()

	if b != nil {
		b.bssMu.Lock()
		defer b.bssMu.Unlock()
	}

	for _, server := range opts.Servers {
		var bs *backendServer
		bs, err = newBackendServer(server)
		if err != nil {
			return
		}
		if _, ok := bn.bss[bs.server]; ok {
			err = fmt.Errorf("backendserver %s already defined", bs.server)
			bs.Close()
			return
		}
		if b != nil {
			if bsr, ok := b.bss[bs.server]; ok {
				if !bsr.SetShared(true) {
					bs.Close()
					bs = bsr
				}
			}
		}
		bn.bss[bs.server] = bs
	}

	bn.opts.Servers = make([]string, 0, len(bn.bss))
	for _, bsr := range bn.bss {
		bn.opts.Servers = append(bn.opts.Servers, bsr.server)
	}
	return
}

func (b *HTTPBackend) Close() {
	b.workerTkr.Stop()
	b.workerCtxCancel()
	b.workerWg.Wait()
	b.bssMu.Lock()
	for _, bsr := range b.bss {
		bsr.Close()
	}
	b.bss = nil
	b.bssMu.Unlock()
}

func (b *HTTPBackend) GetOpts() (opts HTTPBackendOptions) {
	opts.CopyFrom(&b.opts)
	return
}

func (b *HTTPBackend) Activate() {
	for _, bsr := range b.bss {
		var h hc.HealthCheck
		if h == nil && b.opts.HealthCheckHTTPOpts != nil {
			h = hc.NewHTTPCheck(bsr.server, *b.opts.HealthCheckHTTPOpts)
		}
		if h != nil {
			go func(bsr *backendServer) {
				<-h.Check()
				bsr.SetHealthCheck(h)
			}(bsr)
			continue
		}
		bsr.SetHealthCheck(nil)
	}
}

func (b *HTTPBackend) worker(ctx context.Context) {
	for done := false; !done; {
		select {
		case <-b.workerTkr.C:
			b.updateBssList()
		case <-ctx.Done():
			done = true
		}
	}
	b.workerWg.Done()
}

func (b *HTTPBackend) updateBssList() {
	b.bssMu.RLock()
	serverList := make([]*backendServer, 0, len(b.bss))
	for _, bsr := range b.bss {
		if !bsr.Healthy() {
			if !bsr.IsShared() {
				b.promServerHealthy.With(prometheus.Labels{"server": bsr.server}).Set(0)
			}
			continue
		}
		if !bsr.IsShared() {
			b.promServerHealthy.With(prometheus.Labels{"server": bsr.server}).Set(1)
		}
		serverList = append(serverList, bsr)
	}
	b.bssMu.RUnlock()
	b.bssListMu.Lock()
	b.bssList = serverList
	b.bssListMu.Unlock()
}

func (b *HTTPBackend) findServer(ctx context.Context) (bs *backendServer) {
	b.bssListMu.RLock()
	n := len(b.bssList)
	if n > 0 {
		bs = b.bssList[b.rnd.Intn(n)]
	}
	b.bssListMu.RUnlock()
	return
}

func (b *HTTPBackend) serveIngress(ctx context.Context, errCh chan<- error, reqDesc *httpReqDesc) {
	var err error
	defer func() { errCh <- err }()

	_, err = writeHTTPHeader(reqDesc.beConn.Writer, reqDesc.feStatusLine, reqDesc.feHdr)
	if err != nil {
		debugLogger.Printf("serve error on %s: write header to backend: %v", reqDesc.BackendSummary(), err)
		return
	}

	reqDesc.beConn.TimeToFirstByte()

	var contentLength int64
	contentLength, err = httpContentLength(reqDesc.feHdr)
	if err != nil {
		debugLogger.Printf("serve error on %s: write body to backend: %v", reqDesc.BackendSummary(), err)
		return
	}
	if contentLength < 0 {
		contentLength = 0
	}
	_, err = writeHTTPBody(reqDesc.beConn.Writer, reqDesc.feConn.Reader, contentLength, reqDesc.feHdr.Get("Transfer-Encoding"))
	if err != nil {
		if !errors.Is(err, errExpectedEOF) {
			debugLogger.Printf("serve error on %s: write body to backend: %v", reqDesc.BackendSummary(), err)
		}
		return
	}
}

func (b *HTTPBackend) serveEngress(ctx context.Context, errCh chan<- error, reqDesc *httpReqDesc) {
	var err error
	defer func() { errCh <- err }()

	for i := 0; ; i++ {
		reqDesc.beStatusLine, reqDesc.beHdr, _, err = splitHTTPHeader(reqDesc.beConn.Reader)
		if err != nil {
			debugLogger.Printf("serve error on %s: read header from backend: %v", reqDesc.BackendSummary(), err)
			return
		}
		beStatusLineParts := strings.SplitN(reqDesc.beStatusLine, " ", 3)
		if len(beStatusLineParts) < 3 {
			err = wrapHTTPError("protocol", errHTTPStatusLineFormat)
			debugLogger.Printf("serve error on %s: read header from backend: %v", reqDesc.BackendSummary(), err)
			return
		}
		reqDesc.beStatusVersion = strings.ToUpper(beStatusLineParts[0])
		reqDesc.beStatusCode = beStatusLineParts[1]
		reqDesc.beStatusMsg = beStatusLineParts[2]
		if reqDesc.beStatusVersion != "HTTP/1.0" && reqDesc.beStatusVersion != "HTTP/1.1" {
			err = wrapHTTPError("protocol", errHTTPVersion)
			debugLogger.Printf("serve error on %s: read header from backend: %v", reqDesc.BackendSummary(), err)
			return
		}

		_, err = writeHTTPHeader(reqDesc.feConn.Writer, reqDesc.beStatusLine, reqDesc.beHdr)
		if err != nil {
			debugLogger.Printf("serve error on %s: write header to frontend: %v", reqDesc.BackendSummary(), err)
			return
		}

		if expect := reqDesc.feHdr.Get("Expect"); expect != "" && i == 0 {
			expectCode := strings.SplitN(expect, "-", 2)[0]
			if expectCode == reqDesc.beStatusCode {
				continue
			}
		}

		break
	}

	if reqDesc.feStatusMethod == "HEAD" {
		return
	}

	var contentLength int64
	contentLength, err = httpContentLength(reqDesc.beHdr)
	if err != nil {
		debugLogger.Printf("serve error on %s: write body to frontend: %v", reqDesc.BackendSummary(), err)
		return
	}
	_, err = writeHTTPBody(reqDesc.feConn.Writer, reqDesc.beConn.Reader, contentLength, reqDesc.beHdr.Get("Transfer-Encoding"))
	if err != nil {
		if !errors.Is(err, errExpectedEOF) {
			debugLogger.Printf("serve error on %s: write body to frontend: %v", reqDesc.BackendSummary(), err)
		}
		return
	}
}

func (b *HTTPBackend) serveAsync(ctx context.Context, errCh chan<- error, reqDesc *httpReqDesc) {
	var err error
	defer func() { errCh <- err }()

	ingressErrCh := make(chan error, 1)
	go b.serveIngress(ctx, ingressErrCh, reqDesc)

	engressErrCh := make(chan error, 1)
	go b.serveEngress(ctx, engressErrCh, reqDesc)

	err = <-ingressErrCh
	if err != nil {
		return
	}
	err = <-engressErrCh
	if err != nil {
		return
	}

	if reqDesc.beConn.Reader.Buffered() != 0 {
		err = wrapHTTPError("protocol", errHTTPBufferOrder)
		debugLogger.Printf("serve error on %s: %v", reqDesc.BackendSummary(), err)
		return
	}

	switch connection := strings.ToLower(reqDesc.beHdr.Get("Connection")); {
	case connection == "keep-alive" && reqDesc.beStatusVersion == "HTTP/1.1":
	case connection == "close":
		fallthrough
	default:
		err = wrapHTTPError("communication", errExpectedEOF)
		return
	}
}

func (b *HTTPBackend) serve(ctx context.Context, reqDesc *httpReqDesc) (err error) {
	bs := b.findServer(ctx)
	if bs == nil {
		err = wrapHTTPError("backend", errUnableToFindBackendServer)
		debugLogger.Printf("serve error on %s: %v", reqDesc.BackendSummary(), err)
		reqDesc.feConn.Write([]byte(httpServiceUnavailable))
		return
	}
	reqDesc.beServer = bs
	reqDesc.beServerName = bs.server

	reqDesc.beConn, err = bs.ConnAcquire(ctx)
	if err != nil {
		err = wrapHTTPError("backend", errCouldNotConnectToBackendServer)
		debugLogger.Printf("serve error on %s: %v", reqDesc.BackendSummary(), err)
		reqDesc.feConn.Write([]byte(httpBadGateway))
		return
	}
	defer func() {
		if err == nil {
			bs.ConnRelease(reqDesc.beConn)
		}
	}()

	b.promActiveConnections.With(prometheus.Labels{"server": bs.server}).Inc()
	defer b.promActiveConnections.With(prometheus.Labels{"server": bs.server}).Dec()

	if tcpConn, ok := reqDesc.beConn.Conn().(*net.TCPConn); ok {
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(1 * time.Second)
	}

	asyncCtx, asyncCtxCancel := ctx, context.CancelFunc(func() { /* null function */ })
	if b.opts.Timeout > 0 {
		asyncCtx, asyncCtxCancel = context.WithTimeout(asyncCtx, b.opts.Timeout)
	}
	defer asyncCtxCancel()

	xff := reqDesc.feHdr.Get("X-Forwarded-For")
	if xff != "" {
		xff += ", "
	}
	reqDesc.feHdr.Set("X-Forwarded-For", xff+reqDesc.feConn.LocalAddr().String())

	xfp := reqDesc.feHdr.Get("X-Forwarded-Proto")
	if xfp == "" {
		xfp = "http"
		if _, ok := reqDesc.feConn.Conn().(*tls.Conn); ok {
			xfp = "https"
		}
	}
	reqDesc.feHdr.Set("X-Forwarded-Proto", xfp)

	xfh := reqDesc.feHdr.Get("X-Forwarded-Host")
	if xfh == "" {
		xfh = reqDesc.feHdr.Get("Host")
	}
	reqDesc.feHdr.Set("X-Forwarded-Host", xfh)

	xri := reqDesc.feHdr.Get("X-Real-IP")
	if xri == "" {
		xri = reqDesc.feConn.LocalAddr().String()
	}
	reqDesc.feHdr.Set("X-Real-IP", xri)

	for k, v := range b.opts.ReqHeader {
		for ks, vs := range v {
			if ks == 0 {
				reqDesc.feHdr.Set(k, vs)
				continue
			}
			reqDesc.feHdr.Add(k, vs)
		}
	}

	// monitoring start
	startTime := time.Now()
	reqDesc.beConn.TimeToFirstByte()

	asyncErrCh := make(chan error, 1)
	go b.serveAsync(asyncCtx, asyncErrCh, reqDesc)
	select {
	case <-asyncCtx.Done():
		reqDesc.feConn.Flush()
		reqDesc.feConn.Close()
		reqDesc.beConn.Flush()
		reqDesc.beConn.Close()
		<-asyncErrCh
		err = wrapHTTPError("backend timeout", errHTTPTimeout)
		debugLogger.Printf("serve error on %s: %v", reqDesc.BackendSummary(), err)
	case err = <-asyncErrCh:
		if err != nil {
			reqDesc.feConn.Flush()
			reqDesc.feConn.Close()
			reqDesc.beConn.Flush()
			reqDesc.beConn.Close()
		}
	}

	// monitoring end
	promLabels := prometheus.Labels{
		"server":   bs.server,
		"code":     reqDesc.beStatusCode,
		"frontend": reqDesc.feName,
		"address":  reqDesc.feConn.LocalAddr().String(),
		"host":     reqDesc.feHost,
		"path":     reqDesc.fePath,
		"method":   reqDesc.feStatusMethod,
	}
	r, w := reqDesc.beConn.Stats()
	b.promReadBytes.With(promLabels).Add(float64(r))
	b.promWriteBytes.With(promLabels).Add(float64(w))
	if !errors.Is(err, errGracefulTermination) {
		//errDesc := ""
		if err != nil && !errors.Is(err, errExpectedEOF) {
			/*if e, ok := err.(*httpError); ok {
				errDesc = e.Group
			} else {
				errDesc = "unknown"
				debugLogger.Printf("unknown error on backend server %q on backend %q. may be it is a bug: %v", bs.server, b.opts.Name, err)
			}*/
		} else {
			//b.promRequestDurationSeconds.With(promLabels).Observe(time.Now().Sub(startTime).Seconds())
			if tm := reqDesc.beConn.TimeToFirstByte(); !tm.IsZero() {
				b.promTimeToFirstByteSeconds.With(promLabels).Observe(tm.Sub(startTime).Seconds())
			}
		}
		//b.promRequestsTotal.MustCurryWith(promLabels).With(prometheus.Labels{"error": errDesc}).Inc()
	}

	return
}
