package lb

import (
	"bufio"
	"bytes"
	"io"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"

	"github.com/pkg/errors"
)

var (
	httpBadRequest         = "HTTP/1.0 400 Bad Request\r\n\r\nBad Request\r\n"
	httpForbidden          = "HTTP/1.0 403 Forbidden\r\n\r\nForbidden\r\n"
	httpBadGateway         = "HTTP/1.0 502 Bad Gateway\r\n\r\nBad Gateway\r\n"
	httpServiceUnavailable = "HTTP/1.0 503 Service Unavailable\r\n\r\nService Unavailable\r\n"
)

type httpError struct {
	Cause error
	Group string
	Msg   string
}

func (e *httpError) Error() string {
	return e.Msg
}

func (e *httpError) PrintDebugLog() {
	debugLogger.Printf("%s error: %s", e.Group, e.Msg)
}

type httpReqDesc struct {
	feName          string
	feConn          *bufConn
	feStatusLine    string
	feStatusMethod  string
	feStatusURI     string
	feStatusVersion string
	feHdr           http.Header
	feHost          string
	fePath          string
	beName          string
	beServer        *backendServer
	beServerName    string
	beConn          *bufConn
	beStatusLine    string
	beStatusVersion string
	beStatusCode    string
	beStatusMsg     string
	beHdr           http.Header
}

func splitHTTPHeader(rd *bufio.Reader) (statusLine string, hdr http.Header, nr int64, err error) {
	hdr = make(http.Header, 16)
	line := []byte(nil)
	for {
		var ln []byte
		ln, err = rd.ReadSlice('\n')
		nr += int64(len(ln))
		if nr > maxHTTPHeadersLen {
			err = errors.Wrap(errProtocol, "max headers length exceeded")
			break
		}
		if err != nil && err != bufio.ErrBufferFull {
			err = errors.WithStack(err)
			break
		}
		n := len(line)
		m := n + len(ln)
		if m > maxHTTPHeaderLineLen {
			err = errors.Wrap(errProtocol, "max header line length exceeded")
			break
		}
		line = append(line, ln...)
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

func writeHTTPHeader(dst io.Writer, srcStatusLine string, srcHdr http.Header) (nw int64, err error) {
	dstSW := &statsWriter{
		W: dst,
	}
	_, err = dstSW.Write([]byte(srcStatusLine + "\r\n"))
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

func writeHTTPBody(dst io.Writer, src *bufio.Reader, contentLength int64, transferEncoding string) (nw int64, err error) {
	switch transferEncoding {
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

func uriToPath(uri string) string {
	pathAndQuery := strings.SplitN(uri, "?", 2)
	path := ""
	if len(pathAndQuery) > 0 {
		path = pathAndQuery[0]
	}
	for {
		pathFirst := path
		path = doubleslashRgx.ReplaceAllLiteralString(path, `/`)
		path = slashDotRgx.ReplaceAllLiteralString(path, `/`)
		if path == pathFirst {
			break
		}
		pathFirst = path
	}
	return path
}

func httpContentLength(hdr http.Header) (contentLength int64, err error) {
	contentLength = -1
	s := hdr.Get("Content-Length")
	if s == "" {
		return
	}
	var ui64 uint64
	ui64, err = strconv.ParseUint(s, 10, 63)
	if err != nil {
		err = errors.WithStack(err)
		return
	}
	contentLength = int64(ui64)
	if contentLength < 0 {
		contentLength = -1
		err = errors.WithStack(strconv.ErrRange)
		return
	}
	return
}
