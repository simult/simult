package lb

import (
	"context"
	"crypto/tls"
	"net"
	"sync"

	accepter "github.com/orkunkaraduman/go-accepter"
)

type accepterHandler struct {
	Lis net.Listener

	mu        sync.RWMutex
	handler   accepter.Handler
	tlsConfig *tls.Config

	forked   bool
	forkedMu sync.Mutex
}

func (ah *accepterHandler) Set(handler accepter.Handler, tlsConfig *tls.Config) {
	ah.mu.Lock()
	ah.handler = handler
	ah.tlsConfig = tlsConfig
	ah.mu.Unlock()
}

func (ah *accepterHandler) Serve(ctx context.Context, conn net.Conn) {
	ah.mu.RLock()
	handler := ah.handler
	tlsConfig := ah.tlsConfig
	ah.mu.RUnlock()
	if handler != nil {
		if tlsConfig != nil {
			conn = tls.Server(conn, tlsConfig)
		}
		handler.Serve(ctx, conn)
	}
}

func (ah *accepterHandler) SetForked(status bool) bool {
	ah.forkedMu.Lock()
	r := ah.forked
	ah.forked = status
	ah.forkedMu.Unlock()
	return r
}