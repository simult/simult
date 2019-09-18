package lb

import (
	"bufio"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type bufConn struct {
	*bufio.Reader
	*bufio.Writer
	conn net.Conn
	tm   time.Time
	sr   *statsReader
	sw   *statsWriter
	pr   *io.PipeReader
	pw   *io.PipeWriter
	pe   error
	peMu sync.Mutex
}

func newBufConn(conn net.Conn) (bc *bufConn) {
	bc = &bufConn{
		conn: conn,
		tm:   time.Now(),
		sr:   &statsReader{R: conn},
		sw:   &statsWriter{W: conn},
	}
	bc.pr, bc.pw = io.Pipe()
	bc.Reader, bc.Writer = bufio.NewReader(bc.pr), bufio.NewWriter(bc.sw)
	go bc.pipeRead()
	return
}

func (bc *bufConn) pipeRead() {
	var err error
	buf := make([]byte, 64*1024)
	for err == nil {
		var n int
		n, err = bc.sr.Read(buf)
		if n > 0 {
			bc.pw.Write(buf[:n])
		}
	}
	bc.pw.CloseWithError(err)
	bc.peMu.Lock()
	bc.pe = err
	bc.peMu.Unlock()
}

func (bc *bufConn) Conn() net.Conn {
	return bc.conn
}

func (bc *bufConn) Tm() time.Time {
	return bc.tm
}

func (bc *bufConn) Stats() (nr, nw int64) {
	return atomic.SwapInt64(&bc.sr.N, 0), atomic.SwapInt64(&bc.sw.N, 0)
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
	bc.peMu.Lock()
	r := bc.pe == nil
	bc.peMu.Unlock()
	return r
}
