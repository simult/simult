package httplb

import (
	"context"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/simult/server/pkg/hc"
)

type BackendOptions struct {
	Timeout             time.Duration
	ReqHeader           http.Header
	HealthCheckHTTPOpts *hc.HTTPCheckOptions
	Servers             []string
}

func (o *BackendOptions) CopyFrom(src *BackendOptions) {
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

type Backend struct {
	opts      BackendOptions
	bss       map[string]*backendServer
	bssMu     sync.RWMutex
	rnd       *rand.Rand
	ctx       context.Context
	ctxCancel context.CancelFunc
}

func NewBackend(opts BackendOptions) (b *Backend, err error) {
	b, err = b.Fork(opts)
	return
}

func (b *Backend) Fork(opts BackendOptions) (bn *Backend, err error) {
	if b != nil {
		b.bssMu.Lock()
		defer b.bssMu.Unlock()
		select {
		case <-b.ctx.Done():
			err = errors.New("backend closed")
			return
		default:
		}
	}

	bn = &Backend{}
	bn.opts.CopyFrom(&opts)
	bn.bss = make(map[string]*backendServer, len(opts.Servers))
	bn.rnd = rand.New(rand.NewSource(time.Now().Unix()))
	bn.ctx, bn.ctxCancel = context.WithCancel(context.Background())
	defer func() {
		if err == nil {
			return
		}
		bn.Close()
		bn = nil
	}()

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

func (b *Backend) Close() {
	b.ctxCancel()

	b.bssMu.Lock()
	for _, bsr := range b.bss {
		if bsr.SetForked(false) {
			continue
		}
		bsr.Close()
	}
	b.bss = nil
	b.bssMu.Unlock()
}

func (b *Backend) GetOpts() (opts BackendOptions) {
	opts.CopyFrom(&b.opts)
	return
}

func (b *Backend) Activate() {
	for _, bsr := range b.bss {
		if b.opts.HealthCheckHTTPOpts == nil {
			bsr.SetHealthCheck(nil)
			continue
		}
		h := hc.NewHTTPCheck(bsr.server, *b.opts.HealthCheckHTTPOpts)
		go func(bsr *backendServer) {
			<-h.Check()
			bsr.SetHealthCheck(h)
		}(bsr)
	}
}

func (b *Backend) FindServer(ctx context.Context) (bs *backendServer) {
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
