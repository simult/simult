package httplb

import (
	"context"
	"math/rand"
	"sync"
	"time"

	"github.com/pkg/errors"
)

type Backend struct {
	bss       []*backendServer
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
	for _, bs := range b.bss {
		bs.Close()
	}
	b.bssMu.Unlock()
}

func (b *Backend) Set(rawurls []string) (err error) {
	bss := make([]*backendServer, 0, len(rawurls))
	for _, rawurl := range rawurls {
		var bs *backendServer
		bs, err = newBackendServer(rawurl)
		if err != nil {
			for _, bs := range bss {
				bs.Close()
			}
			return
		}
		bss = append(bss, bs)
	}
	b.bssMu.Lock()
	for _, bs := range b.bss {
		bs.Close()
	}
	b.bss = nil
	select {
	case <-b.ctx.Done():
		for _, bs := range bss {
			bs.Close()
		}
		err = errors.New("backend closed")
	default:
		b.bss = bss
	}
	b.bssMu.Unlock()
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
