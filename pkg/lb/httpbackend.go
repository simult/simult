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
	opts  HTTPBackendOptions
	bss   map[string]*backendServer
	bssMu sync.RWMutex
	rnd   *rand.Rand

	promReadBytes              *prometheus.CounterVec
	promWriteBytes             *prometheus.CounterVec
	promRequestsTotal          *prometheus.CounterVec
	promRequestDurationSeconds prometheus.ObserverVec
	promErrorsTotal            *prometheus.CounterVec
	promTimeoutsTotal          *prometheus.CounterVec
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

	promLabels := map[string]string{"name": bn.opts.Name}
	bn.promReadBytes = promHTTPBackendReadBytes.MustCurryWith(promLabels)
	bn.promWriteBytes = promHTTPBackendWriteBytes.MustCurryWith(promLabels)
	bn.promRequestsTotal = promHTTPBackendRequestsTotal.MustCurryWith(promLabels)
	bn.promRequestDurationSeconds = promHTTPBackendRequestDurationSeconds.MustCurryWith(promLabels)
	bn.promErrorsTotal = promHTTPBackendErrorsTotal.MustCurryWith(promLabels)
	bn.promTimeoutsTotal = promHTTPBackendTimeoutsTotal.MustCurryWith(promLabels)

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
				if !bsr.SetForked(true) {
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

func (b *HTTPBackend) findServer(ctx context.Context) (bs *backendServer) {
	b.bssMu.RLock()
	serverList := make([]*backendServer, 0, len(b.bss))
	for _, bsr := range b.bss {
		if !bsr.Healthy() {
			continue
		}
		serverList = append(serverList, bsr)
	}
	n := len(serverList)
	if n > 0 {
		bs = serverList[b.rnd.Intn(n)]
	}
	b.bssMu.RUnlock()
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
		reqDesc.beStatusLineParts = strings.SplitN(reqDesc.beStatusLine, " ", 3)

		_, err = writeHTTPHeader(reqDesc.feConn.Writer, reqDesc.beStatusLine, reqDesc.beHdr)
		if err != nil {
			debugLogger.Printf("write header to listener %q from backend server %q on backend %q: %v", reqDesc.feConn.LocalAddr().String(), reqDesc.beConn.RemoteAddr().String(), b.opts.Name, err)
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
		reqDesc.err = errors.WithStack(errBufferOrder)
		debugLogger.Printf("buffer order error on backend server %q on backend %q", reqDesc.beConn.RemoteAddr().String(), b.opts.Name)
		return
	}

	ok = true
}

func (b *HTTPBackend) serve(ctx context.Context, reqDesc *httpReqDesc) (ok bool) {
	bs := b.findServer(ctx)
	if bs == nil {
		reqDesc.err = errors.WithStack(errFindBackendServer)
		debugLogger.Printf("error on backend %q: %v", b.opts.Name, reqDesc.err)
		reqDesc.feConn.Write([]byte("HTTP/1.0 503 Service Unavailable\r\n\r\n"))
		return
	}

	reqDesc.beConn, reqDesc.err = bs.ConnAcquire(ctx)
	if reqDesc.err != nil {
		reqDesc.err = errors.WithStack(errConnectBackendServer)
		debugLogger.Printf("error on backend %q: %v", b.opts.Name, reqDesc.err)
		reqDesc.feConn.Write([]byte("HTTP/1.0 503 Service Unavailable\r\n\r\n"))
		return
	}
	defer bs.ConnRelease(reqDesc.beConn)
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
	/*if len(reqDesc.feStatusLineParts) > 2 {
		if strings.ToUpper(reqDesc.feStatusLineParts[2]) == "HTTP/1.1" {
			reqDesc.feHdr.Set("Connection", "keep-alive")
		}
	}*/

	// monitoring start
	startTime := time.Now()
	timeouted := false

	asyncOK := false
	asyncOKCh := make(chan bool, 1)
	go b.serveAsync(asyncCtx, asyncOKCh, reqDesc)
	select {
	case <-asyncCtx.Done():
		reqDesc.beConn.Flush()
		reqDesc.beConn.Close()
		timeouted = true
		<-asyncOKCh
	case asyncOK = <-asyncOKCh:
	}

	// monitoring end
	{
		promLabels := prometheus.Labels{"server": bs.server}
		r, w := reqDesc.beConn.Stats()
		b.promReadBytes.With(promLabels).Add(float64(r))
		b.promWriteBytes.With(promLabels).Add(float64(w))
	}
	if e := errors.Cause(reqDesc.err); e != errGracefulTermination {
		promLabels := prometheus.Labels{"server": bs.server, "method": "", "code": ""}
		if len(reqDesc.feStatusLineParts) > 0 {
			promLabels["method"] = reqDesc.feStatusLineParts[0]
		}
		if len(reqDesc.beStatusLineParts) > 1 {
			promLabels["code"] = reqDesc.beStatusLineParts[1]
		}
		b.promRequestsTotal.With(promLabels).Inc()
		b.promRequestDurationSeconds.With(promLabels).Observe(time.Now().Sub(startTime).Seconds())
		if !timeouted {
			if e := errors.Cause(reqDesc.err); e != nil && e != errExpectedEOF {
				b.promErrorsTotal.With(promLabels).Inc()
			}
		} else {
			b.promTimeoutsTotal.With(promLabels).Inc()
		}
	}

	if !asyncOK {
		return
	}

	ok = true
	return
}
