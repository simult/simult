package httplb

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httputil"
	"strconv"

	"github.com/pkg/errors"
)

func copyBody(dst io.Writer, src *bufio.Reader, srcHdr http.Header, zeroContentLength bool) (nn int64, err error) {
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
			nn, err = io.Copy(dst, src)
			err = errors.WithStack(err)
		} else {
			nn, err = io.CopyN(dst, src, contentLength)
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
			nn = dstSW.N
			break
		}
		err = dstCk.Close()
		if err != nil {
			err = errors.WithStack(err)
			nn = dstSW.N
			break
		}
		_, err = dstSW.Write([]byte("\r\n"))
		if err != nil {
			err = errors.WithStack(err)
			nn = dstSW.N
			break
		}
		nn = dstSW.N
		var buf [2]byte
		var n int
		n, err = src.Read(buf[:])
		if err != nil {
			err = errors.WithStack(err)
			break
		}
		if n <= 0 || string(buf[:n]) != "\r\n" {
			err = errors.New("chunked transfer encoding error")
			break
		}
	default:
		err = errors.New("unsupported transfer encoding")
		return
	}
	if dstWr, ok := dst.(*bufio.Writer); ok {
		if e := dstWr.Flush(); err == nil {
			err = errors.WithStack(e)
		}
	}
	return
}
