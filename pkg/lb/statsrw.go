package lb

import (
	"io"
	"sync/atomic"
)

type statsReader struct {
	R io.Reader
	N int64
}

func (sr *statsReader) Read(p []byte) (n int, err error) {
	n, err = sr.R.Read(p)
	if n > 0 {
		atomic.AddInt64(&sr.N, int64(n))
	}
	return
}

func (sr *statsReader) Reset() {
	atomic.StoreInt64(&sr.N, 0)
}

type statsWriter struct {
	W io.Writer
	N int64
}

func (sw *statsWriter) Write(p []byte) (n int, err error) {
	n, err = sw.W.Write(p)
	if n > 0 {
		atomic.AddInt64(&sw.N, int64(n))
	}
	return
}

func (sw *statsWriter) Reset() {
	atomic.StoreInt64(&sw.N, 0)
}
