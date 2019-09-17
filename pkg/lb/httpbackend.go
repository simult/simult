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
		if !bsr.SetForked(false) {
			bsr.Close()
		}
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

func (b *HTTPBackend) FindServer(ctx context.Context) (bs *backendServer) {
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
			debugLogger.Printf("write header to backend server %q on backend %q from listener %q: %v", reqDesc.beConn.RemoteAddr().String(), b.opts.Name, reqDesc.feConn.RemoteAddr().String(), err)
			return
		}
		_, err = writeHTTPBody(reqDesc.beConn.Writer, reqDesc.feConn.Reader, reqDesc.feHdr, true)
		if err != nil {
			if errors.Cause(err) != errExpectedEOF {
				debugLogger.Printf("write body to backend server %q on backend %q from listener %q: %v", reqDesc.beConn.RemoteAddr().String(), b.opts.Name, reqDesc.feConn.RemoteAddr().String(), err)
			}
			return
		}
	}()

	reqDesc.beStatusLine, reqDesc.beHdr, _, reqDesc.err = splitHTTPHeader(reqDesc.beConn.Reader)
	if reqDesc.err != nil {
		debugLogger.Printf("read header from backend server %q on backend %q: %v", reqDesc.beConn.RemoteAddr().String(), b.opts.Name, reqDesc.err)
		return
	}
	reqDesc.beStatusLineParts = strings.SplitN(reqDesc.beStatusLine, " ", 3)

	_, reqDesc.err = writeHTTPHeader(reqDesc.feConn.Writer, reqDesc.beStatusLine, reqDesc.beHdr)
	if reqDesc.err != nil {
		debugLogger.Printf("write header to listener %q from backend server %q on backend %q: %v", reqDesc.feConn.RemoteAddr().String(), reqDesc.beConn.RemoteAddr().String(), b.opts.Name, reqDesc.err)
		return
	}

	_, reqDesc.err = writeHTTPBody(reqDesc.feConn.Writer, reqDesc.beConn.Reader, reqDesc.beHdr, false)
	if reqDesc.err != nil {
		if errors.Cause(reqDesc.err) != errExpectedEOF {
			debugLogger.Printf("write body to listener %q from backend server %q on backend %q: %v", reqDesc.feConn.RemoteAddr().String(), reqDesc.beConn.RemoteAddr().String(), b.opts.Name, reqDesc.err)
		}
		return
	}

	reqDesc.err = <-ingressErrCh
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
		reqDesc.err = errBufferOrder
		debugLogger.Printf("buffer order error on backend server %q on backend %q", reqDesc.beConn.RemoteAddr().String(), b.opts.Name)
		return
	}

	ok = true
}

func (b *HTTPBackend) serve(ctx context.Context, reqDesc *httpReqDesc) (ok bool) {
	var err error

	bs := b.FindServer(ctx)
	if bs == nil {
		reqDesc.feConn.Write([]byte("HTTP/1.1 503 Service Unavailable\r\n\r\n"))
		return
	}

	reqDesc.beConn, err = bs.ConnAcquire(ctx)
	if err != nil {
		reqDesc.feConn.Write([]byte("HTTP/1.1 503 Service Unavailable\r\n\r\n"))
		return
	}
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
	reqDesc.feHdr.Set("X-Forwarded-For", reqDesc.feHdr.Get("X-Forwarded-For")+", "+reqDesc.feConn.RemoteAddr().String())

	// monitoring start

	asyncOK := false
	asyncOKCh := make(chan bool, 1)
	go b.serveAsync(asyncCtx, asyncOKCh, reqDesc)
	select {
	case <-asyncCtx.Done():
		reqDesc.beConn.Close()
	case asyncOK = <-asyncOKCh:
	}

	// monitoring end

	if !asyncOK {
		return
	}

	bs.ConnRelease(reqDesc.beConn)

	ok = true
	return
}
