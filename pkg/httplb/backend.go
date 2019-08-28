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
	Servers         []string
}

type Backend struct {
	opts      BackendOptions
	optsMu    sync.RWMutex
	bss       []*backendServer
	bssMu     sync.RWMutex
	rnd       *rand.Rand
	ctx       context.Context
	ctxCancel context.CancelFunc
}

func NewBackend(opts BackendOptions) (b *Backend) {
	b = &Backend{
		opts: opts,
	}
	b.rnd = rand.New(rand.NewSource(time.Now().Unix()))
	b.ctx, b.ctxCancel = context.WithCancel(context.Background())
	return
}

func (b *Backend) Close() {
	b.ctxCancel()
	b.bssMu.Lock()
	for _, bs := range b.bss {
		bs.Close()
	}
	b.bss = nil
	b.bssMu.Unlock()
}

func (b *Backend) GetOpts() (opts BackendOptions) {
	b.optsMu.RLock()
	defer b.optsMu.RUnlock()
	opts = b.opts
	opts.Servers = make([]string, len(b.opts.Servers))
	copy(opts.Servers, b.opts.Servers)
	return

}

func (b *Backend) SetOpts(opts BackendOptions) (err error) {
	b.optsMu.Lock()
	defer b.optsMu.Unlock()

	b.bssMu.Lock()
	defer b.bssMu.Unlock()

	bss := make([]*backendServer, 0, len(opts.Servers))
	bssMap := make(map[*backendServer]struct{}, len(opts.Servers))
	for _, server := range opts.Servers {
		var bs *backendServer
		for _, bsr := range b.bss {
			if bsr.server == server {
				bs = bsr
				break
			}
		}
		if bs == nil {
			bs, err = newBackendServer(server)
			if err != nil {
				for _, bs := range bss {
					bs.Close()
				}
				return
			}
		}
		bss = append(bss, bs)
		bssMap[bs] = struct{}{}
	}

	select {
	case <-b.ctx.Done():
		for _, bs := range bss {
			bs.Close()
		}
		err = errors.New("backend closed")
	default:
		for _, bs := range b.bss {
			if _, ok := bssMap[bs]; !ok {
				bs.Close()
			}
		}
		b.bss = bss
	}

	b.opts = opts
	b.opts.Servers = make([]string, len(opts.Servers))
	copy(b.opts.Servers, opts.Servers)
	return
}

func (b *Backend) FindServer(ctx context.Context) (bs *backendServer) {
	b.bssMu.RLock()
	n := len(b.bss)
	if n > 0 {
		bs = b.bss[b.rnd.Intn(n)]
	}
	b.bssMu.RUnlock()
	return
}
