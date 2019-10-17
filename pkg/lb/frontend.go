package lb

import (
	"context"
	"net"
)

type Frontend interface {
	Serve(ctx context.Context, le *Listener, conn net.Conn)
}
