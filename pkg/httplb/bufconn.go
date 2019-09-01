package httplb

import (
	"bufio"
	"io"
	"net"
	"sync"
	"time"
)

type bufConn struct {
	*bufio.Reader
	*bufio.Writer
	conn    net.Conn
	tm      time.Time
	rpRd    *io.PipeReader
	rpWr    *io.PipeWriter
	rpErr   error
	rpErrMu sync.Mutex
}

func newBufConn(conn net.Conn) (bc *bufConn) {
	bc = &bufConn{
		conn: conn,
		tm:   time.Now(),
	}
	bc.rpRd, bc.rpWr = io.Pipe()
	bc.Reader, bc.Writer = bufio.NewReader(bc.rpRd), bufio.NewWriter(conn)
	go bc.pipeRead()
	return
}

func (bc *bufConn) pipeRead() {
	buf := make([]byte, 64*1024)
	for {
		n, err := bc.conn.Read(buf)
		if n > 0 {
			bc.rpWr.Write(buf[:n])
		}
		if err != nil {
			bc.rpWr.CloseWithError(err)
			bc.rpErrMu.Lock()
			bc.rpErr = err
			bc.rpErrMu.Unlock()
			break
		}
	}
}

func (bc *bufConn) Conn() net.Conn {
	return bc.conn
}

func (bc *bufConn) Tm() time.Time {
	return bc.tm
}

func (bc *bufConn) Close() error {
	return bc.conn.Close()
}

func (bc *bufConn) LocalAddr() net.Addr {
	return bc.conn.LocalAddr()
}

func (bc *bufConn) RemoteAddr() net.Addr {
	return bc.conn.RemoteAddr()
}

func (bc *bufConn) Check() bool {
	bc.rpErrMu.Lock()
	defer bc.rpErrMu.Unlock()
	return bc.rpErr == nil
}
