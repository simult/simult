package lb

import (
	"bufio"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

type bufConn struct {
	*bufio.Reader
	*bufio.Writer
	conn            net.Conn
	timeToFirstByte *time.Time
	sr              *statsReader
	sw              *statsWriter
	pr              *io.PipeReader
	pw              *io.PipeWriter
	pe              error
	peMu            sync.Mutex
}

const (
	bufConnBufferSize = 32 * 1024
)

func newBufConn(conn net.Conn) (bc *bufConn) {
	bc = &bufConn{
		conn: conn,
		sr:   &statsReader{R: conn},
		sw:   &statsWriter{W: conn},
	}
	bc.pr, bc.pw = io.Pipe()
	bc.Reader, bc.Writer = bufio.NewReaderSize(bc.pr, bufConnBufferSize), bufio.NewWriterSize(bc.sw, bufConnBufferSize)
	go bc.pipeRead()
	return
}

func (bc *bufConn) pipeRead() {
	var err error
	buf := make([]byte, bufConnBufferSize)
	for err == nil {
		var n int
		n, err = bc.sr.Read(buf)
		if n > 0 {
			if bc.timeToFirstByte == nil {
				now := time.Now()
				atomic.CompareAndSwapPointer((*unsafe.Pointer)(unsafe.Pointer(&bc.timeToFirstByte)), nil, unsafe.Pointer(&now))
			}
			bc.pw.Write(buf[:n])
		}
	}
	bc.pw.CloseWithError(err)
	bc.peMu.Lock()
	bc.pe = err
	bc.peMu.Unlock()
}

func (bc *bufConn) Close() error {
	bc.peMu.Lock()
	if bc.pe == nil {
		bc.pe = io.EOF
	}
	bc.peMu.Unlock()
	return bc.conn.Close()
}

func (bc *bufConn) LocalAddr() net.Addr {
	return bc.conn.LocalAddr()
}

func (bc *bufConn) RemoteAddr() net.Addr {
	return bc.conn.RemoteAddr()
}

func (bc *bufConn) Conn() net.Conn {
	return bc.conn
}

func (bc *bufConn) TimeToFirstByte() time.Time {
	timeToFirstByte := (*time.Time)(atomic.SwapPointer((*unsafe.Pointer)(unsafe.Pointer(&bc.timeToFirstByte)), nil))
	if timeToFirstByte == nil {
		return time.Time{}
	}
	return *timeToFirstByte
}

func (bc *bufConn) Stats() (nr, nw int64) {
	return atomic.SwapInt64(&bc.sr.N, 0), atomic.SwapInt64(&bc.sw.N, 0)
}

func (bc *bufConn) Check() bool {
	bc.peMu.Lock()
	r := bc.pe == nil
	bc.peMu.Unlock()
	return r
}
