package lb

import (
	"context"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
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

	bn.updateBssList()

	promLabels := map[string]string{
		"name": bn.opts.Name,
	}
	bn.promReadBytes = promHTTPBackendReadBytes.MustCurryWith(promLabels)
	bn.promWriteBytes = promHTTPBackendWriteBytes.MustCurryWith(promLabels)
	bn.promRequestsTotal = promHTTPBackendRequestsTotal.MustCurryWith(promLabels)
	bn.promRequestDurationSeconds = promHTTPBackendRequestDurationSeconds.MustCurryWith(promLabels)
	bn.promTimeToFirstByteSeconds = promHTTPBackendTimeToFirstByteSeconds.MustCurryWith(promLabels)
	bn.promActiveConnections = promHTTPBackendActiveConnections.MustCurryWith(promLabels)

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
			err = errors.Errorf("backendserver %s already defined", bs.server)
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
			continue
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

func (b *HTTPBackend) serveAsync(ctx context.Context, okCh chan<- bool, reqDesc *httpReqDesc) {
	ok := false
	defer func() {
		if !ok {
			reqDesc.beConn.Flush()
			reqDesc.beConn.Close()
		}
		okCh <- ok
	}()

	ingressErrCh := make(chan error, 1)
	go func() {
		var err error
		defer func() { ingressErrCh <- err }()

		_, err = writeHTTPHeader(reqDesc.beConn.Writer, reqDesc.feStatusLine, reqDesc.feHdr)
		if err != nil {
			debugLogger.Printf("write header to backend server %q on backend %q from listener %q: %v", reqDesc.beConn.RemoteAddr().String(), b.opts.Name, reqDesc.feConn.LocalAddr().String(), err)
			return
		}

		_, err = writeHTTPBody(reqDesc.beConn.Writer, reqDesc.feConn.Reader, reqDesc.feHdr, true)
		if err != nil {
			if e := errors.Cause(err); e != errExpectedEOF {
				debugLogger.Printf("write body to backend server %q on backend %q from listener %q: %v", reqDesc.beConn.RemoteAddr().String(), b.opts.Name, reqDesc.feConn.LocalAddr().String(), err)
			}
			return
		}
	}()

	engressErrCh := make(chan error, 1)
	go func() {
		var err error
		defer func() { engressErrCh <- err }()

		reqDesc.beStatusLine, reqDesc.beHdr, _, err = splitHTTPHeader(reqDesc.beConn.Reader)
		if err != nil {
			debugLogger.Printf("read header from backend server %q on backend %q: %v", reqDesc.beConn.RemoteAddr().String(), b.opts.Name, err)
			return
		}
		beStatusLineParts := strings.SplitN(reqDesc.beStatusLine, " ", 3)
		if len(beStatusLineParts) > 0 {
			reqDesc.beStatusVersion = strings.ToUpper(beStatusLineParts[0])
		}
		if len(beStatusLineParts) > 1 {
			reqDesc.beStatusCode = beStatusLineParts[1]
		}
		if len(beStatusLineParts) > 2 {
			reqDesc.beStatusMsg = beStatusLineParts[2]
		}

		_, err = writeHTTPHeader(reqDesc.feConn.Writer, reqDesc.beStatusLine, reqDesc.beHdr)
		if err != nil {
			debugLogger.Printf("write header to listener %q from backend server %q on backend %q: %v", reqDesc.feConn.LocalAddr().String(), reqDesc.beConn.RemoteAddr().String(), b.opts.Name, err)
			return
		}

		if reqDesc.feStatusMethod == "HEAD" {
			return
		}
		_, err = writeHTTPBody(reqDesc.feConn.Writer, reqDesc.beConn.Reader, reqDesc.beHdr, false)
		if err != nil {
			if e := errors.Cause(err); e != errExpectedEOF {
				debugLogger.Printf("write body to listener %q from backend server %q on backend %q: %v", reqDesc.feConn.LocalAddr().String(), reqDesc.beConn.RemoteAddr().String(), b.opts.Name, err)
			}
			return
		}
	}()

	reqDesc.err = <-ingressErrCh
	if reqDesc.err != nil {
		return
	}
	reqDesc.err = <-engressErrCh
	if reqDesc.err != nil {
		return
	}

	switch strings.ToLower(reqDesc.beHdr.Get("Connection")) {
	case "keep-alive":
	case "close":
		fallthrough
	default:
		return
	}

	if reqDesc.beConn.Reader.Buffered() != 0 {
		reqDesc.err = errors.WithStack(errCommunication)
		debugLogger.Printf("%v: buffer order error on backend server %q on backend %q", reqDesc.err, reqDesc.beConn.RemoteAddr().String(), b.opts.Name)
		return
	}

	ok = true
}

func (b *HTTPBackend) serve(ctx context.Context, reqDesc *httpReqDesc) (ok bool) {
	reqDesc.beName = b.opts.Name

	bs := b.findServer(ctx)
	if bs == nil {
		reqDesc.err = errors.WithStack(errBackend)
		debugLogger.Printf("%v on %q: %v", reqDesc.err, b.opts.Name, "unable to find backend server")
		reqDesc.feConn.Write([]byte("HTTP/1.0 503 Service Unavailable\r\n\r\n"))
		return
	}

	reqDesc.beConn, reqDesc.err = bs.ConnAcquire(ctx)
	if reqDesc.err != nil {
		e := reqDesc.err
		reqDesc.err = errors.WithStack(errBackend)
		debugLogger.Printf("%v on %q: %v", reqDesc.err, b.opts.Name, e)
		reqDesc.feConn.Write([]byte("HTTP/1.0 503 Service Unavailable\r\n\r\n"))
		return
	}
	defer bs.ConnRelease(reqDesc.beConn)

	b.promActiveConnections.With(prometheus.Labels{"server": bs.server}).Inc()
	defer b.promActiveConnections.With(prometheus.Labels{"server": bs.server}).Dec()

	if tcpConn, ok := reqDesc.beConn.Conn().(*net.TCPConn); ok {
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(1 * time.Second)
	}

	asyncCtx, asyncCtxCancel := ctx, context.CancelFunc(func() {})
	if b.opts.Timeout > 0 {
		asyncCtx, asyncCtxCancel = context.WithTimeout(asyncCtx, b.opts.Timeout)
	}
	defer asyncCtxCancel()

	for k, v := range b.opts.ReqHeader {
		for ks, vs := range v {
			if ks == 0 {
				reqDesc.feHdr.Set(k, vs)
				continue
			}
			reqDesc.feHdr.Add(k, vs)
		}
	}
	xff := reqDesc.feHdr.Get("X-Forwarded-For")
	if xff != "" {
		xff += ", "
	}
	reqDesc.feHdr.Set("X-Forwarded-For", xff+reqDesc.feConn.LocalAddr().String())

	// monitoring start
	startTime := time.Now()
	reqDesc.beConn.TimeToFirstByte()

	asyncOK := false
	asyncOKCh := make(chan bool, 1)
	go b.serveAsync(asyncCtx, asyncOKCh, reqDesc)
	select {
	case <-asyncCtx.Done():
		reqDesc.beConn.Flush()
		reqDesc.beConn.Close()
		<-asyncOKCh
		reqDesc.err = errors.WithStack(errBackendTimeout)
	case asyncOK = <-asyncOKCh:
	}

	// monitoring end
	promLabels := prometheus.Labels{
		"server":   bs.server,
		"frontend": reqDesc.feName,
		"method":   reqDesc.feStatusMethod,
		"code":     reqDesc.beStatusCode,
	}
	r, w := reqDesc.beConn.Stats()
	b.promReadBytes.With(promLabels).Add(float64(r))
	b.promWriteBytes.With(promLabels).Add(float64(w))
	if e := errors.Cause(reqDesc.err); e != errGracefulTermination {
		errDesc := ""
		if e != nil && e != errExpectedEOF {
			switch e {
			case errCommunication:
			case errFrontendTimeout:
			case errBackendTimeout:
			case errBackend:
			default:
				e = errCommunication
			}
			errDesc = e.Error()
		} else {
			b.promRequestDurationSeconds.With(promLabels).Observe(time.Now().Sub(startTime).Seconds())
		}
		b.promRequestsTotal.MustCurryWith(promLabels).With(prometheus.Labels{"error": errDesc}).Inc()
		if tm := reqDesc.beConn.TimeToFirstByte(); !tm.IsZero() {
			b.promTimeToFirstByteSeconds.With(promLabels).Observe(tm.Sub(startTime).Seconds())
		}
	}

	if !asyncOK {
		return
	}

	ok = true
	return
}
