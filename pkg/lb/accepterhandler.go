package lb

import (
	"context"
	"crypto/tls"
	"net"
	"sync"
)

type accepterHandler struct {
	mu        sync.RWMutex
	le        *Listener
	fe        Frontend
	tlsConfig *tls.Config

	shared   bool
	sharedMu sync.Mutex
}

func (ah *accepterHandler) Set(le *Listener, fe Frontend, tlsConfig *tls.Config) {
	ah.mu.Lock()
	ah.le = le
	ah.fe = fe
	ah.tlsConfig = tlsConfig
	ah.mu.Unlock()
}

func (ah *accepterHandler) Serve(ctx context.Context, conn net.Conn) {
	ah.mu.RLock()
	le := ah.le
	fe := ah.fe
	tlsConfig := ah.tlsConfig
	ah.mu.RUnlock()
	if fe != nil {
		if tlsConfig != nil {
			conn = tls.Server(conn, tlsConfig)
		}
		fe.Serve(ctx, le, conn)
	}
}

func (ah *accepterHandler) SetShared(status bool) bool {
	ah.sharedMu.Lock()
	r := ah.shared
	ah.shared = status
	ah.sharedMu.Unlock()
	return r
}
