package lb

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httputil"
	"strconv"

	"github.com/pkg/errors"
)

var eofBody = errors.New("EOF body")

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
				err = eofBody
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
