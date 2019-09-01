package httplb

import (
	"context"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/pkg/errors"
)

type BackendOptions struct {
	Timeout         time.Duration
	CustomReqHeader http.Header
	HealthCheckOpts interface{}
	Servers         []string
}

func (o *BackendOptions) CopyFrom(src *BackendOptions) {
	*o = *src
	o.CustomReqHeader = make(http.Header, len(src.CustomReqHeader))
	for k, v := range src.CustomReqHeader {
		nv := make([]string, len(v))
		copy(nv, v)
		o.CustomReqHeader[k] = nv
	}
	o.Servers = make([]string, len(src.Servers))
	copy(o.Servers, src.Servers)
}

type Backend struct {
	opts      BackendOptions
	optsMu    sync.RWMutex
	bss       map[string]*backendServer
	bssMu     sync.RWMutex
	rnd       *rand.Rand
	ctx       context.Context
	ctxCancel context.CancelFunc
}

func NewBackend() (b *Backend) {
	b = &Backend{}
	b.rnd = rand.New(rand.NewSource(time.Now().Unix()))
	b.ctx, b.ctxCancel = context.WithCancel(context.Background())
	return
}

func (b *Backend) Close() {
	b.ctxCancel()
	b.bssMu.Lock()
	for _, bsr := range b.bss {
		bsr.Close()
	}
	b.bss = nil
	b.bssMu.Unlock()
}

func (b *Backend) GetOpts() (opts BackendOptions) {
	b.optsMu.RLock()
	defer b.optsMu.RUnlock()
	opts.CopyFrom(&b.opts)
	return

}

func (b *Backend) SetOpts(opts BackendOptions) (err error) {
	b.bssMu.Lock()
	defer b.bssMu.Unlock()

	select {
	case <-b.ctx.Done():
		err = errors.New("backend closed")
		return
	default:
	}

	b.optsMu.Lock()
	defer b.optsMu.Unlock()

	bss := make(map[string]*backendServer, len(opts.Servers))
	for _, server := range opts.Servers {
		var bs *backendServer
		bs, err = newBackendServer(server)
		if err != nil {
			for srv, bsr := range bss {
				if _, ok := b.bss[srv]; !ok {
					bsr.Close()
				}
			}
			return
		}
		if bsr, ok := b.bss[bs.server]; ok {
			bs.Close()
			bs = bsr
		}
		bss[bs.server] = bs
	}

	for srv, bsr := range b.bss {
		if _, ok := bss[srv]; !ok {
			bsr.Close()
		}
	}
	b.bss = bss

	b.opts.CopyFrom(&opts)
	b.opts.Servers = make([]string, 0, len(b.bss))
	for _, bsr := range b.bss {
		b.opts.Servers = append(b.opts.Servers, bsr.server)
		bsr.SetHealthCheck(opts.HealthCheckOpts)
	}
	return
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
