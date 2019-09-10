package app

import (
	"context"
	"crypto/tls"
	"net"
	"sync"

	accepter "github.com/orkunkaraduman/go-accepter"
)

type accepterHandler struct {
	mu        sync.RWMutex
	handler   accepter.Handler
	tlsConfig *tls.Config
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
	ah.mu.RUnlock()
	if handler != nil {
		if ah.tlsConfig != nil {
			tlsConn := tls.Server(conn, ah.tlsConfig)
			conn = tlsConn
		}
		handler.Serve(ctx, conn)
	}
}
