package httplb

import (
	"errors"
	"io"
	"net"
	"syscall"
)

// connCheck from https://github.com/go-sql-driver/mysql/blob/master/conncheck.go
var errConnCheckUnexpectedRead = errors.New("unexpected read from socket")

// connCheck from https://github.com/go-sql-driver/mysql/blob/master/conncheck.go
func connCheck(c net.Conn) error {
	var (
		n    int
		err  error
		buff [1]byte
	)

	sconn, ok := c.(syscall.Conn)
	if !ok {
		return nil
	}
	rc, err := sconn.SyscallConn()
	if err != nil {
		return err
	}
	rerr := rc.Read(func(fd uintptr) bool {
		n, err = syscall.Read(int(fd), buff[:])
		return true
	})
	switch {
	case rerr != nil:
		return rerr
	case n == 0 && err == nil:
		return io.EOF
	case n > 0:
		return errConnCheckUnexpectedRead
	case err == syscall.EAGAIN || err == syscall.EWOULDBLOCK:
		return nil
	default:
		return err
	}
}
