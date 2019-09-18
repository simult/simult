package lb

import (
	"bufio"
	"bytes"
	"io"
	"net/http"
	"net/http/httputil"
	"strconv"

	"github.com/pkg/errors"
)

const (
	maxHeaderLineLen = 1 * 1024 * 1024
)

var (
	errExpectedEOF          = errors.New("expected EOF")
	errGracefulTermination  = errors.New("graceful termination")
	errBufferOrder          = errors.New("buffer order error")
	errFindBackendServer    = errors.New("can not find backend server")
	errConnectBackendServer = errors.New("can not connect backend server")
)

func splitHTTPHeader(rd *bufio.Reader) (statusLine string, hdr http.Header, nr int64, err error) {
	hdr = make(http.Header, 16)
	line := make([]byte, 0, maxHeaderLineLen)
	for {
		var ln []byte
		ln, err = rd.ReadSlice('\n')
		nr += int64(len(ln))
		if err != nil && err != bufio.ErrBufferFull {
			err = errors.WithStack(err)
			return
		}
		n := len(line)
		m := n + len(ln)
		if m > cap(line) {
			err = errors.WithStack(bufio.ErrBufferFull)
			return
		}
		line = line[:m]
		copy(line[n:], ln)
		if err == bufio.ErrBufferFull || m < 1 || line[m-1] != '\n' {
			continue
		}
		n = 1
		if m >= 2 && line[m-2] == '\r' {
			n = 2
		}
		line = line[:m-n]
		if len(line) == 0 {
			break
		}
		if statusLine != "" {
			idx := bytes.IndexByte(line, ':')
			name, value := "", ""
			if idx < 0 {
				name = string(line)
			} else {
				name = string(line[:idx])
				value = string(bytes.TrimLeft(line[idx+1:], " "))
			}
			if len(line) > 0 {
				hdr.Add(name, value)
			}
		} else {
			statusLine = string(line)
		}
		line = line[:0]
	}
	return
}

func writeHTTPHeader(dst io.Writer, srcSl string, srcHdr http.Header) (nw int64, err error) {
	dstSW := &statsWriter{
		W: dst,
	}
	_, err = dstSW.Write([]byte(srcSl + "\r\n"))
	if err != nil {
		nw = dstSW.N
		return
	}
	err = srcHdr.Write(dstSW)
	if err != nil {
		nw = dstSW.N
		return
	}
	_, err = dstSW.Write([]byte("\r\n"))
	if err != nil {
		nw = dstSW.N
		return
	}
	if dstWr, ok := dst.(*bufio.Writer); ok {
		if e := dstWr.Flush(); e != nil && err == nil {
			err = errors.WithStack(e)
		}
	}
	nw = dstSW.N
	return
}

func writeHTTPBody(dst io.Writer, src *bufio.Reader, srcHdr http.Header, zeroContentLength bool) (nw int64, err error) {
	var contentLength int64
	if !zeroContentLength {
		contentLength = -1
	}
	s := srcHdr.Get("Content-Length")
	if s != "" {
		var ui64 uint64
		ui64, err = strconv.ParseUint(s, 10, 63)
		if err != nil {
			err = errors.WithStack(err)
			return
		}
		contentLength = int64(ui64)
		if contentLength < 0 {
			err = errors.WithStack(strconv.ErrRange)
			return
		}
	}
	switch srcHdr.Get("Transfer-Encoding") {
	case "":
		if contentLength < 0 {
			nw, err = io.Copy(dst, src)
			if err == nil {
				err = errExpectedEOF
			}
			err = errors.WithStack(err)
		} else {
			nw, err = io.CopyN(dst, src, contentLength)
			err = errors.WithStack(err)
		}
	case "chunked":
		srcCk := httputil.NewChunkedReader(src)
		dstSW := &statsWriter{
			W: dst,
		}
		dstCk := httputil.NewChunkedWriter(dstSW)
		_, err = io.Copy(dstCk, srcCk)
		if err != nil {
			err = errors.WithStack(err)
			nw = dstSW.N
			break
		}
		err = dstCk.Close()
		if err != nil {
			err = errors.WithStack(err)
			nw = dstSW.N
			break
		}
		var crlfBuf [2]byte
		var n int
		n, err = src.Read(crlfBuf[:])
		if err != nil {
			err = errors.WithStack(err)
			nw = dstSW.N
			break
		}
		if n <= 0 || string(crlfBuf[:n]) != "\r\n" {
			err = errors.New("chunked transfer encoding error")
			nw = dstSW.N
			break
		}
		_, err = dstSW.Write(crlfBuf[:n])
		if err != nil {
			err = errors.WithStack(err)
			nw = dstSW.N
			break
		}
		nw = dstSW.N
	default:
		err = errors.New("unsupported transfer encoding")
	}
	if dstWr, ok := dst.(*bufio.Writer); ok {
		if e := dstWr.Flush(); e != nil && err == nil {
			err = errors.WithStack(e)
		}
	}
	return
}