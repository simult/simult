package lb

import (
	"context"
	"net"
)

type Frontend interface {
	Serve(ctx context.Context, l *Listener, conn net.Conn)
}
