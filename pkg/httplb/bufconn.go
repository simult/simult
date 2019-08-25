package httplb

import (
	"bufio"
	"net"
	"time"
)

type bufConn struct {
	*bufio.Reader
	*bufio.Writer
	conn net.Conn
	tm   time.Time
}

func newBufConn(conn net.Conn) (bc *bufConn) {
	bc = &bufConn{
		bufio.NewReader(conn),
		bufio.NewWriter(conn),
		conn,
		time.Now(),
	}
	return
}

func (bc *bufConn) Close() error {
	return bc.conn.Close()
}

func (bc *bufConn) Check() bool {
	return connCheck(bc.conn) == nil
}
