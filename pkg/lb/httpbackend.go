package lb

import (
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/goinsane/wrh"
	"github.com/goinsane/xlog"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/simult/simult/pkg/hc"
)

// HTTPBackendMode is type of HTTP backend modes
type HTTPBackendMode int

const (
	// HTTPBackendModeRoundRobin defines roundrobin backend mode
	HTTPBackendModeRoundRobin = HTTPBackendMode(iota)

	// HTTPBackendModeLeastConn defines leastconn backend mode
	HTTPBackendModeLeastConn

	// HTTPBackendModeAffinityKey defines affinitykey backend mode
	HTTPBackendModeAffinityKey
)

// HTTPBackendAffinityKeyKind is type of affinity-key kinds to use in affinity-key backend mode
type HTTPBackendAffinityKeyKind int

const (
	// HTTPBackendAffinityKeyKindRemoteIP defines remoteip affinity-key kind
	HTTPBackendAffinityKeyKindRemoteIP = HTTPBackendAffinityKeyKind(iota)

	// HTTPBackendAffinityKeyKindRealIP defines realip affinity-key kind
	HTTPBackendAffinityKeyKindRealIP

	// HTTPBackendAffinityKeyKindHTTPHeader defines httpheader affinity-key kind
	HTTPBackendAffinityKeyKindHTTPHeader

	// HTTPBackendAffinityKeyKindHTTPCookie defines httpcookie affinity-key kind
	HTTPBackendAffinityKeyKindHTTPCookie
)

// HTTPBackendOptions holds HTTPBackend options
type HTTPBackendOptions struct {
	Name                string
	MaxConn             int
	ServerMaxConn       int
	ServerMaxIdleConn   int
	Timeout             time.Duration
	ConnectTimeout      time.Duration
	ReqHeader           http.Header
	ServerHashSecret    string
	HealthCheckHTTPOpts *hc.HTTPCheckOptions
	Mode                HTTPBackendMode
	AffinityKey         struct {
		Kind       HTTPBackendAffinityKeyKind
		Key        string
		MaxServers int
		Threshold  int
	}
	OverrideErrors string
	Servers        []string
}

// CopyFrom sets the underlying HTTPBackendOptions by given HTTPBackendOptions
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

// HTTPBackend implements a backend for HTTP
type HTTPBackend struct {
	opts      HTTPBackendOptions
	bss       map[string]*backendServer
	bssMu     sync.RWMutex
	connCount int64

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
	promServerHealth           *prometheus.GaugeVec

	bssNodes   wrh.Nodes
	bssNodesMu sync.RWMutex
}

// NewHTTPBackend creates a new HTTPBackend by given options
func NewHTTPBackend(opts HTTPBackendOptions) (b *HTTPBackend, err error) {
	b, err = b.Fork(opts)
	return
}

// Fork forkes a HTTPBackend and its own members by given options
func (b *HTTPBackend) Fork(opts HTTPBackendOptions) (bn *HTTPBackend, err error) {
	bn = &HTTPBackend{}
	bn.opts.CopyFrom(&opts)
	bn.bss = make(map[string]*backendServer, len(opts.Servers))
	bn.workerTkr = time.NewTicker(100 * time.Millisecond)
	bn.ctx, bn.ctxCancel = context.WithCancel(context.Background())

	promLabels := prometheus.Labels{
		"backend": bn.opts.Name,
	}
	bn.promReadBytes = promHTTPBackendReadBytes.MustCurryWith(promLabels)
	bn.promWriteBytes = promHTTPBackendWriteBytes.MustCurryWith(promLabels)
	bn.promRequestsTotal = promHTTPBackendRequestsTotal.MustCurryWith(promLabels)
	bn.promRequestDurationSeconds = promHTTPBackendRequestDurationSeconds.MustCurryWith(promLabels)
	bn.promTimeToFirstByteSeconds = promHTTPBackendTimeToFirstByteSeconds.MustCurryWith(promLabels)
	bn.promActiveConnections = promHTTPBackendActiveConnections.MustCurryWith(promLabels)
	bn.promIdleConnections = promHTTPBackendIdleConnections.MustCurryWith(promLabels)
	bn.promServerHealth = promHTTPBackendServerHealth.MustCurryWith(promLabels)

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

	for _, serverLine := range opts.Servers {
		values := strings.Split(serverLine, " ")
		server := values[0]
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
		if bs.serverURL.Scheme != "http" && bs.serverURL.Scheme != "https" {
			err = fmt.Errorf("backendserver %s has wrong scheme", bs.server)
			bs.Close()
			return
		}
		bs.weight = 1.0
		if len(values) > 1 {
			var x uint64
			x, err = strconv.ParseUint(values[1], 10, 8)
			if err != nil {
				err = fmt.Errorf("backendserver %s has wrong weight: %w", bs.server, err)
				bs.Close()
				return
			}
			bs.weight = float64(x)
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

	bn.updateBssNodes()

	bn.workerWg.Add(1)
	go bn.worker()

	return
}

// Close closes the HTTPBackend and its own members
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

// GetOpts returns a copy of underlying HTTPBackend's options
func (b *HTTPBackend) GetOpts() (opts HTTPBackendOptions) {
	opts.CopyFrom(&b.opts)
	return
}

// Activate activates HTTPBackend after Fork
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
	serverList := make([]string, 0, len(b.bss))
	healthyMap := make(map[string]*backendServer, len(b.bss))
	for _, bsr := range b.bss {
		serverList = append(serverList, bsr.server)
		b.promActiveConnections.With(prometheus.Labels{"server": bsr.server}).Set(float64(bsr.activeConnCount))
		b.promIdleConnections.With(prometheus.Labels{"server": bsr.server}).Set(float64(bsr.idleConnCount))
		if !bsr.Healthy() {
			if !bsr.IsShared() {
				b.promServerHealth.With(prometheus.Labels{"server": bsr.server}).Set(0)
			}
			continue
		}
		if !bsr.IsShared() {
			b.promServerHealth.With(prometheus.Labels{"server": bsr.server}).Set(1)
		}
		healthyMap[bsr.server] = bsr
	}
	sort.Sort(sort.StringSlice(serverList))
	nodes := make(wrh.Nodes, 0, len(b.bss))
	seed := uint32(0)
	for _, server := range serverList {
		weight := 0.0
		if bsr, ok := healthyMap[server]; ok {
			weight = bsr.weight
		}
		nodes = append(nodes, wrh.Node{
			Seed:   seed,
			Weight: weight,
			Data:   b.bss[server],
		})
		seed++
	}
	b.bssMu.RUnlock()
	b.bssNodesMu.Lock()
	b.bssNodes = nodes
	b.bssNodesMu.Unlock()
}

func (b *HTTPBackend) findServer(reqDesc *httpReqDesc) (bs *backendServer) {
	b.bssNodesMu.RLock()
	switch b.opts.Mode {
	case HTTPBackendModeRoundRobin:
		bval := genRandByteSlice(8)
		respNodes := wrh.ResponsibleNodes2(b.bssNodes, bval, 1)
		node := &respNodes[0]
		if node.Weight <= 0 {
			break
		}
		bs = node.Data.(*backendServer)
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
		if val == "" {
			bval := genRandByteSlice(8)
			val = string(bval)
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
		if maxServers > 1 {
			sort.Sort(respNodes)
		}
		for i := range respNodes {
			node := &respNodes[i]
			if node.Weight <= 0 {
				break
			}
			bsr := node.Data.(*backendServer)
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
		if atomic.CompareAndSwapUint32(&reqDesc.isTransferErrLogged, 0, 1) {
			xlog.V(100).Debugf("serve error on %s: write header to backend: %v", reqDesc.BackendSummary(), err)
		}
		return
	}

	reqDesc.beConn.TimeToFirstByte()

	var contentLength int64
	contentLength, err = httpContentLength(reqDesc.feHdr)
	if err != nil {
		if atomic.CompareAndSwapUint32(&reqDesc.isTransferErrLogged, 0, 1) {
			xlog.V(100).Debugf("serve error on %s: write body to backend: %v", reqDesc.BackendSummary(), err)
		}
		return
	}
	if contentLength < 0 {
		contentLength = 0
	}
	_, err = writeHTTPBody(reqDesc.beConn.Writer, reqDesc.feConn.Reader, contentLength, reqDesc.feHdr.Get("Transfer-Encoding"))
	if err != nil {
		if atomic.CompareAndSwapUint32(&reqDesc.isTransferErrLogged, 0, 1) && !errors.Is(err, errExpectedEOF) {
			xlog.V(100).Debugf("serve error on %s: write body to backend: %v", reqDesc.BackendSummary(), err)
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
			if atomic.CompareAndSwapUint32(&reqDesc.isTransferErrLogged, 0, 1) {
				xlog.V(100).Debugf("serve error on %s: read header from backend: %v", reqDesc.BackendSummary(), err)
			}
			return
		}

		beStatusLineParts := strings.SplitN(reqDesc.beStatusLine, " ", 3)
		if len(beStatusLineParts) < 3 {
			err = errHTTPStatusLine
			if atomic.CompareAndSwapUint32(&reqDesc.isTransferErrLogged, 0, 1) {
				xlog.V(100).Debugf("serve error on %s: %v", reqDesc.BackendSummary(), err)
			}
			return
		}

		reqDesc.beStatusVersion = strings.ToUpper(beStatusLineParts[0])
		if reqDesc.beStatusVersion != "HTTP/1.0" && reqDesc.beStatusVersion != "HTTP/1.1" {
			err = errHTTPStatusVersion
			if atomic.CompareAndSwapUint32(&reqDesc.isTransferErrLogged, 0, 1) {
				xlog.V(100).Debugf("serve error on %s: %v", reqDesc.BackendSummary(), err)
			}
			return
		}

		reqDesc.beStatusCode = beStatusLineParts[1]

		reqDesc.beStatusMsg = beStatusLineParts[2]

		reqDesc.beStatusCodeGrouped = groupHTTPStatusCode(reqDesc.beStatusCode)

		if b.opts.ServerHashSecret != "" && reqDesc.beHdr.Get("X-Server-Name") == "" {
			h := md5.New()
			io.WriteString(h, b.opts.ServerHashSecret)
			io.WriteString(h, reqDesc.beServer)
			s := fmt.Sprintf("%x", h.Sum(nil))
			reqDesc.beHdr.Set("X-Server-Name", s)
		}

		reqDesc.beHdr.Del("Keep-Alive")

		_, err = writeHTTPHeader(reqDesc.feConn.Writer, reqDesc.beStatusLine, reqDesc.beHdr)
		if err != nil {
			if atomic.CompareAndSwapUint32(&reqDesc.isTransferErrLogged, 0, 1) {
				xlog.V(100).Debugf("serve error on %s: write header to frontend: %v", reqDesc.BackendSummary(), err)
			}
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
		if atomic.CompareAndSwapUint32(&reqDesc.isTransferErrLogged, 0, 1) {
			xlog.V(100).Debugf("serve error on %s: write body to frontend: %v", reqDesc.BackendSummary(), err)
		}
		return
	}
	_, err = writeHTTPBody(reqDesc.feConn.Writer, reqDesc.beConn.Reader, contentLength, reqDesc.beHdr.Get("Transfer-Encoding"))
	if err != nil {
		if atomic.CompareAndSwapUint32(&reqDesc.isTransferErrLogged, 0, 1) && !errors.Is(err, errExpectedEOF) {
			xlog.V(100).Debugf("serve error on %s: write body to frontend: %v", reqDesc.BackendSummary(), err)
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
		xlog.V(100).Debugf("serve error on %s: %v", reqDesc.BackendSummary(), err)
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
	feWr := io.Writer(reqDesc.feConn)
	if !reqDesc.beFinal {
		feWr = &nopWriter{}
	}
	if b.opts.MaxConn > 0 && b.connCount >= int64(b.opts.MaxConn) {
		err = errHTTPBackendExhausted
		xlog.V(100).Debugf("serve error on %s: %v", reqDesc.BackendSummary(), err)
		if b.opts.OverrideErrors != "" {
			feWr.Write([]byte(b.opts.OverrideErrors))
			return
		}
		feWr.Write([]byte(httpServiceUnavailable))
		return
	}
	atomic.AddInt64(&b.connCount, 1)
	defer atomic.AddInt64(&b.connCount, -1)

	bs := b.findServer(reqDesc)
	if bs == nil {
		err = errHTTPBackendFind
		xlog.V(100).Debugf("serve error on %s: %v", reqDesc.BackendSummary(), err)
		if b.opts.OverrideErrors != "" {
			feWr.Write([]byte(b.opts.OverrideErrors))
			return
		}
		feWr.Write([]byte(httpServiceUnavailable))
		return
	}
	reqDesc.beServer = bs.server

	if b.opts.ServerMaxConn > 0 && bs.activeConnCount >= int64(b.opts.ServerMaxConn) {
		err = errHTTPBackendServerExhausted
		xlog.V(100).Debugf("serve error on %s: %v", reqDesc.BackendSummary(), err)
		if b.opts.OverrideErrors != "" {
			feWr.Write([]byte(b.opts.OverrideErrors))
			return
		}
		feWr.Write([]byte(httpServiceUnavailable))
		return
	}

	connectCtx := ctx
	if b.opts.ConnectTimeout > 0 {
		var connectCtxCancel context.CancelFunc
		connectCtx, connectCtxCancel = context.WithTimeout(ctx, b.opts.ConnectTimeout)
		defer connectCtxCancel()
	}
	reqDesc.beConn, err = bs.ConnAcquire(connectCtx)
	if err != nil {
		if e := (*net.OpError)(nil); errors.As(err, &e) && e.Timeout() {
			err = newfHTTPError(httpErrGroupBackendConnectTimeout, "timeout exceeded while connecting to backend server: %w", err)
			xlog.V(100).Debugf("serve error on %s: %v", reqDesc.BackendSummary(), err)
			if b.opts.OverrideErrors != "" {
				feWr.Write([]byte(b.opts.OverrideErrors))
				return
			}
			feWr.Write([]byte(httpGatewayTimeout))
			return
		}
		err = newfHTTPError(httpErrGroupBackendConnect, "could not connect to backend server: %w", err)
		xlog.V(100).Debugf("serve error on %s: %v", reqDesc.BackendSummary(), err)
		if b.opts.OverrideErrors != "" {
			feWr.Write([]byte(b.opts.OverrideErrors))
			return
		}
		feWr.Write([]byte(httpBadGateway))
		return
	}
	defer func() {
		if b.opts.ServerMaxIdleConn > 0 && bs.idleConnCount >= int64(b.opts.ServerMaxIdleConn) {
			reqDesc.beConn.Close()
		}
		bs.ConnRelease(reqDesc.beConn)
	}()
	reqDesc.beFinal = true

	/*if tcpConn, ok := reqDesc.beConn.Conn().(*net.TCPConn); ok {
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(1 * time.Second)
	}*/

	if b.opts.Timeout > 0 {
		var ctxCancel context.CancelFunc
		ctx, ctxCancel = context.WithTimeout(ctx, b.opts.Timeout)
		defer ctxCancel()
	}

	xff := reqDesc.feHdr.Get("X-Forwarded-For")
	if xff != "" {
		xff += ", "
	}
	reqDesc.feHdr.Set("X-Forwarded-For", xff+reqDesc.feRemoteIP)

	if reqDesc.feHdr.Get("X-Forwarded-Proto") == "" {
		reqDesc.feHdr.Set("X-Forwarded-Proto", reqDesc.feURL.Scheme)
	}

	if reqDesc.feHdr.Get("X-Forwarded-Host") == "" {
		reqDesc.feHdr.Set("X-Forwarded-Host", reqDesc.feURL.Host)
	}

	if reqDesc.feHdr.Get("X-Forwarded-Port") == "" {
		reqDesc.feHdr.Set("X-Forwarded-Port", reqDesc.lePort)
	}

	if reqDesc.feHdr.Get("X-Real-IP") == "" {
		reqDesc.feHdr.Set("X-Real-IP", reqDesc.feRemoteIP)
	}

	reqDesc.feHdr.Del("Keep-Alive")

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
	go b.serveAsync(ctx, asyncErrCh, reqDesc)
	select {
	case <-ctx.Done():
		atomic.CompareAndSwapUint32(&reqDesc.isTransferErrLogged, 0, 1)
		err = errHTTPBackendTimeout
		xlog.V(100).Debugf("serve error on %s: %v", reqDesc.BackendSummary(), err)
		reqDesc.feConn.Flush()
		reqDesc.feConn.Close()
		reqDesc.beConn.Flush()
		reqDesc.beConn.Close()
		<-asyncErrCh
	case err = <-asyncErrCh:
		if err != nil {
			reqDesc.feConn.Flush()
			reqDesc.feConn.Close()
			reqDesc.beConn.Flush()
			reqDesc.beConn.Close()
		}
	}
	// resetting and reading stats before bs.ConnRelease(...)
	r, w := reqDesc.beConn.Stats()

	// monitoring end
	promLabels := prometheus.Labels{
		"server":   reqDesc.beServer,
		"code":     reqDesc.beStatusCodeGrouped,
		"frontend": reqDesc.feName,
		"host":     reqDesc.feHost,
		"path":     reqDesc.fePath,
		"method":   reqDesc.feStatusMethodGrouped,
		"listener": reqDesc.leName,
	}
	b.promReadBytes.With(promLabels).Add(float64(r))
	b.promWriteBytes.With(promLabels).Add(float64(w))
	//errDesc := ""
	if err != nil && !errors.Is(err, errExpectedEOF) {
		if e := (*httpError)(nil); errors.As(err, &e) {
			//errDesc = e.Group
		} else {
			//errDesc = "unknown"
			xlog.V(100).Debugf("unknown error on backend server %q on backend %q. may be it is a bug: %v", reqDesc.beServer, reqDesc.beName, err)
		}
	} else {
		//b.promRequestDurationSeconds.With(promLabels).Observe(time.Now().Sub(startTime).Seconds())
		if tm := reqDesc.beConn.TimeToFirstByte(); !tm.IsZero() {
			b.promTimeToFirstByteSeconds.With(promLabels).Observe(tm.Sub(startTime).Seconds())
		}
	}
	//b.promRequestsTotal.MustCurryWith(promLabels).With(prometheus.Labels{"error": errDesc}).Inc()

	return
}
