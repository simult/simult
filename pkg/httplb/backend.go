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
	opts = b.opts
	opts.CustomReqHeader = make(http.Header, 16)
	for k, v := range b.opts.CustomReqHeader {
		nv := make([]string, len(v))
		copy(nv, v)
		opts.CustomReqHeader[k] = nv
	}
	opts.Servers = make([]string, len(b.opts.Servers))
	copy(opts.Servers, b.opts.Servers)
	return

}

func (b *Backend) SetOpts(opts BackendOptions) (err error) {
	b.optsMu.Lock()
	defer b.optsMu.Unlock()

	b.bssMu.Lock()
	defer b.bssMu.Unlock()

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

	select {
	case <-b.ctx.Done():
		for _, bsr := range bss {
			bsr.Close()
		}
		err = errors.New("backend closed")
	default:
		for srv, bsr := range b.bss {
			if _, ok := bss[srv]; !ok {
				bsr.Close()
			}
		}
		b.bss = bss
	}

	b.opts = opts
	b.opts.CustomReqHeader = make(http.Header, 16)
	for k, v := range opts.CustomReqHeader {
		nv := make([]string, len(v))
		copy(nv, v)
		b.opts.CustomReqHeader[k] = nv
	}
	b.opts.Servers = make([]string, 0, len(opts.Servers))
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
