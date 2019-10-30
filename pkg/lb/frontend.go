package lb

import (
	"context"
	"net"
)

// Frontend is general interface of all frontend implementations
type Frontend interface {
	Serve(ctx context.Context, l *Listener, conn net.Conn)
}
