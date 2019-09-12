package lb

import (
	"context"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/simult/server/pkg/hc"
)

type HTTPBackendOptions struct {
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
