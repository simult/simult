package lb

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/simult/server/pkg/hc"
	"github.com/simult/server/pkg/wrh"
)

type HTTPBackendMode int

const (
	HTTPBackendModeRoundRobin = HTTPBackendMode(iota)
	HTTPBackendModeLeastConn
	HTTPBackendModeAffinityKey
)

type HTTPBackendAffinityKeyKind int

const (
	HTTPBackendAffinityKeyKindRemoteIP = HTTPBackendAffinityKeyKind(iota)
	HTTPBackendAffinityKeyKindRealIP
	HTTPBackendAffinityKeyKindHTTPHeader
	HTTPBackendAffinityKeyKindHTTPCookie
)

type HTTPBackendOptions struct {
	Name                string
	Timeout             time.Duration
	ReqHeader           http.Header
	HealthCheckHTTPOpts *hc.HTTPCheckOptions
	Mode                HTTPBackendMode
	AffinityKey         struct {
		Kind       HTTPBackendAffinityKeyKind
		Key        string
		MaxServers int
		Threshold  int
	}
	Servers []string
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
	opts  HTTPBackendOptions
	bss   map[string]*backendServer
	bssMu sync.RWMutex
	rnd   *rand.Rand

	workerTkr *time.Ticker
	workerWg  sync.WaitGroup

	ctx       context.Context
	ctxCancel context.CancelFunc

	promReadBytes              *prometheus.CounterVec
	promWriteBytes             *prometheus.CounterVec
	promRequestsTotal          *prometheus.CounterVec
	promRequestDurationSeconds prometheus.ObserverVec
	promTimeToFirstByteSeconds prometheus.ObserverVec
	promActiveConnections      *prometheus.GaugeVec
	promIdleConnections        *prometheus.GaugeVec
	promServerHealthy          *prometheus.GaugeVec

	bssNodes   wrh.Nodes
	bssNodesMu sync.RWMutex
}

func NewHTTPBackend(opts HTTPBackendOptions) (b *HTTPBackend, err error) {
	b, err = b.Fork(opts)
	return
}

func (b *HTTPBackend) Fork(opts HTTPBackendOptions) (bn *HTTPBackend, err error) {
	bn = &HTTPBackend{}
	bn.opts.CopyFrom(&opts)
	bn.bss = make(map[string]*backendServer, len(opts.Servers))
	bn.rnd = rand.New(rand.NewSource(time.Now().Unix()))
	bn.workerTkr = time.NewTicker(100 * time.Millisecond)
	bn.ctx, bn.ctxCancel = context.WithCancel(context.Background())

	promLabels := prometheus.Labels{
		"backend": bn.opts.Name,
	}
	promLabelsEmpty := prometheus.Labels{
		"server":   "",
		"code":     "",
		"frontend": "",
		"host":     "",
		"path":     "",
		"method":   "",
		"listener": "",
	}

	bn.promReadBytes = promHTTPBackendReadBytes.MustCurryWith(promLabels)
	bn.promReadBytes.With(promLabelsEmpty).Add(0)

	bn.promWriteBytes = promHTTPBackendWriteBytes.MustCurryWith(promLabels)
	bn.promWriteBytes.With(promLabelsEmpty).Add(0)

	bn.promRequestsTotal = promHTTPBackendRequestsTotal.MustCurryWith(promLabels)
	bn.promRequestsTotal.MustCurryWith(prometheus.Labels{"error": ""}).With(promLabelsEmpty).Add(0)

	bn.promRequestDurationSeconds = promHTTPBackendRequestDurationSeconds.MustCurryWith(promLabels)
	bn.promTimeToFirstByteSeconds = promHTTPBackendTimeToFirstByteSeconds.MustCurryWith(promLabels)
	bn.promActiveConnections = promHTTPBackendActiveConnections.MustCurryWith(promLabels)
	bn.promIdleConnections = promHTTPBackendIdleConnections.MustCurryWith(promLabels)
	bn.promServerHealthy = promHTTPBackendServerHealthy.MustCurryWith(promLabels)

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

	bn.updateBssNodes()

	bn.workerWg.Add(1)
	go bn.worker()

	return
}

func (b *HTTPBackend) Close() {
	b.ctxCancel()
	b.workerTkr.Stop()
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

func (b *HTTPBackend) worker() {
	for done := false; !done; {
		select {
		case <-b.workerTkr.C:
			b.updateBssNodes()
		case <-b.ctx.Done():
			done = true
		}
	}
	b.workerWg.Done()
}

func (b *HTTPBackend) updateBssNodes() {
	b.bssMu.RLock()
	list := make([]string, 0, len(b.bss))
	for key, bsr := range b.bss {
		b.promActiveConnections.With(prometheus.Labels{"server": bsr.server}).Set(float64(bsr.activeConnCount))
		b.promIdleConnections.With(prometheus.Labels{"server": bsr.server}).Set(float64(bsr.idleConnCount))
		if !bsr.Healthy() {
			if !bsr.IsShared() {
				b.promServerHealthy.With(prometheus.Labels{"server": bsr.server}).Set(0)
			}
			continue
		}
		if !bsr.IsShared() {
			b.promServerHealthy.With(prometheus.Labels{"server": bsr.server}).Set(1)
		}
		list = append(list, key)
	}
	sort.Sort(sort.StringSlice(list))
	nodes := make(wrh.Nodes, 0, len(b.bss))
	seed := uint32(0)
	for _, key := range list {
		nodes = append(nodes, wrh.Node{
			Seed:   seed,
			Weight: 1.0,
			Data:   b.bss[key],
		})
		seed++
	}
	b.bssMu.RUnlock()
	b.bssNodesMu.Lock()
	b.bssNodes = nodes
	b.bssNodesMu.Unlock()
}

func (b *HTTPBackend) findServer(ctx context.Context, reqDesc *httpReqDesc) (bs *backendServer) {
	b.bssNodesMu.RLock()
	switch b.opts.Mode {
	case HTTPBackendModeRoundRobin:
		n := len(b.bssNodes)
		if n > 0 {
			bs = b.bssNodes[b.rnd.Intn(n)].Data.(*backendServer)
		}
	case HTTPBackendModeLeastConn:
		for i := range b.bssNodes {
			bsr := b.bssNodes[i].Data.(*backendServer)
			if bsr.activeConnCount == 0 {
				bs = bsr
				break
			}
			if bs == nil || bs.activeConnCount > bsr.activeConnCount {
				bs = bsr
			}
		}
	case HTTPBackendModeAffinityKey:
		kind := b.opts.AffinityKey.Kind
		key := b.opts.AffinityKey.Key
		val := ""
		switch kind {
		case HTTPBackendAffinityKeyKindRemoteIP:
			val = reqDesc.feRemoteIP
		case HTTPBackendAffinityKeyKindRealIP:
			val = reqDesc.feRealIP
		case HTTPBackendAffinityKeyKindHTTPHeader:
			val = reqDesc.feHdr.Get(key)
		case HTTPBackendAffinityKeyKindHTTPCookie:
			for _, cookie := range reqDesc.feCookies {
				if cookie != nil && cookie.Name == key {
					val = cookie.Value
					break
				}
			}
		}
		bssNodesLen := len(b.bssNodes)
		maxServers := b.opts.AffinityKey.MaxServers
		switch {
		case maxServers == 0:
			maxServers = 1
		case maxServers < 0 || maxServers > bssNodesLen:
			maxServers = bssNodesLen
		}
		threshold := b.opts.AffinityKey.Threshold
		if threshold < 0 {
			threshold = 0
		}
		respNodes := wrh.ResponsibleNodes2(b.bssNodes, []byte(val), maxServers)
		sort.Sort(respNodes)
		for i := range respNodes {
			bsr := respNodes[i].Data.(*backendServer)
			if bs != nil {
				oldCount, newCount := bs.activeConnCount, bsr.activeConnCount
				if newCount <= 0 {
					newCount = 1
				}
				if oldCount <= int64(threshold)*newCount {
					break
				}
			}
			if bs == nil || bs.activeConnCount > bsr.activeConnCount {
				bs = bsr
			}
		}
	}
	b.bssNodesMu.RUnlock()
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
			err = errHTTPStatusLineFormat
			debugLogger.Printf("serve error on %s: read header from backend: %v", reqDesc.BackendSummary(), err)
			return
		}
		reqDesc.beStatusVersion = strings.ToUpper(beStatusLineParts[0])
		reqDesc.beStatusCode = beStatusLineParts[1]
		reqDesc.beStatusMsg = beStatusLineParts[2]
		if reqDesc.beStatusVersion != "HTTP/1.0" && reqDesc.beStatusVersion != "HTTP/1.1" {
			err = errHTTPVersion
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
		err = errHTTPBufferOrder
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
	bs := b.findServer(ctx, reqDesc)
	if bs == nil {
		err = errHTTPUnableToFindBackendServer
		debugLogger.Printf("serve error on %s: %v", reqDesc.BackendSummary(), err)
		reqDesc.feConn.Write([]byte(httpServiceUnavailable))
		return
	}
	reqDesc.beServer = bs.server

	reqDesc.beConn, err = bs.ConnAcquire(ctx)
	if err != nil {
		err = errHTTPCouldNotConnectToBackendServer
		debugLogger.Printf("serve error on %s: %v", reqDesc.BackendSummary(), err)
		reqDesc.feConn.Write([]byte(httpBadGateway))
		return
	}
	defer func() {
		conn := reqDesc.beConn
		if err == nil {
			conn = nil
		}
		bs.ConnRelease(conn)
	}()

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
	reqDesc.feHdr.Set("X-Forwarded-For", xff+reqDesc.feRemoteIP)

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
		xri = reqDesc.feRemoteIP
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
		err = errHTTPBackendTimeout
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
		"server":   reqDesc.beServer,
		"code":     reqDesc.beStatusCode,
		"frontend": reqDesc.feName,
		"host":     reqDesc.feHost,
		"path":     reqDesc.fePath,
		"method":   reqDesc.feStatusMethod,
		"listener": reqDesc.leName,
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
				debugLogger.Printf("unknown error on backend server %q on backend %q. may be it is a bug: %v", reqDesc.beServer, reqDesc.beName, err)
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
