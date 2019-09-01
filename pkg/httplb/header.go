package httplb

import (
	"bufio"
	"bytes"
	"io"
	"net/http"

	"github.com/pkg/errors"
)

const (
	maxHeaderLineLen = 1 * 1024 * 1024
)

func splitHeader(rd *bufio.Reader) (statusLine string, hdr http.Header, err error) {
	hdr = make(http.Header, 16)
	line := make([]byte, 0, maxHeaderLineLen)
	for {
		var ln []byte
		ln, err = rd.ReadSlice('\n')
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
		if m < 1 || line[m-1] != '\n' {
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

func writeHeader(dst io.Writer, srcSl string, srcHdr http.Header) (nn int64, err error) {
	dstSW := &statsWriter{
		W: dst,
	}
	_, err = dstSW.Write([]byte(srcSl + "\r\n"))
	if err != nil {
		nn = dstSW.N
		return
	}
	err = srcHdr.Write(dstSW)
	if err != nil {
		nn = dstSW.N
		return
	}
	_, err = dstSW.Write([]byte("\r\n"))
	if err != nil {
		nn = dstSW.N
		return
	}
	if dstWr, ok := dst.(*bufio.Writer); ok {
		if e := dstWr.Flush(); e != nil && err == nil {
			err = errors.WithStack(e)
		}
	}
	nn = dstSW.N
	return
}
