package lb

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
)

var (
	httpBadRequest          = "HTTP/1.0 400 Bad Request\r\n\r\nBad Request\r\n"
	httpForbidden           = "HTTP/1.0 403 Forbidden\r\n\r\nForbidden\r\n"
	httpBadGateway          = "HTTP/1.0 502 Bad Gateway\r\n\r\nBad Gateway\r\n"
	httpServiceUnavailable  = "HTTP/1.0 503 Service Unavailable\r\n\r\nService Unavailable\r\n"
	httpVersionNotSupported = "HTTP/1.0 505 HTTP Version Not Supported\r\n\r\nHTTP Version Not Supported\r\n"
)

var (
	errHTTPChunkedTransferEncoding        = newHTTPError("protocol", "chunked transfer encoding error")
	errHTTPUnsupportedTransferEncoding    = newHTTPError("protocol", "unsupported transfer encoding")
	errHTTPStatusLineFormat               = newHTTPError("protocol", "status line format error")
	errHTTPVersion                        = newHTTPError("protocol", "HTTP version error")
	errHTTPRestrictedRequest              = newHTTPError("restricted", "restricted request")
	errHTTPBufferOrder                    = newHTTPError("protocol", "buffer order error")
	errHTTPFrontendTimeout                = newHTTPError("frontend timeout", "timeout exceeded")
	errHTTPBackendTimeout                 = newHTTPError("backend timeout", "timeout exceeded")
	errHTTPUnableToFindBackendServer      = newHTTPError("backend find", "unable to find backend server")
	errHTTPCouldNotConnectToBackendServer = newHTTPError("backend connect", "could not connect to backend server")
)

type httpError struct {
	Group string
	Err   error
}

func wrapHTTPError(group string, err error) error {
	return &httpError{
		Group: group,
		Err:   err,
	}
}

func newHTTPError(group string, str string) error {
	return &httpError{
		Group: group,
		Err:   errors.New(str),
	}
}

func newfHTTPError(group string, format string, args ...interface{}) error {
	return &httpError{
		Group: group,
		Err:   fmt.Errorf(format, args...),
	}
}

func (e *httpError) Error() string {
	if e.Group == "" {
		return fmt.Sprintf("%v", e.Err)
	}
	return fmt.Sprintf("%s error: %v", e.Group, e.Err)
}

func (e *httpError) Unwrap() error {
	return e.Err
}

type httpReqDesc struct {
	leName          string
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
	beServer        string
	beConn          *bufConn
	beStatusLine    string
	beStatusVersion string
	beStatusCode    string
	beStatusMsg     string
	beHdr           http.Header
}

func (r *httpReqDesc) FrontendSummary() string {
	return fmt.Sprintf("frontend=%q host=%q path=%q method=%q listener=%q",
		r.feName,
		r.feHost,
		r.fePath,
		r.feStatusMethod,
		r.leName,
	)
}

func (r *httpReqDesc) BackendSummary() string {
	return fmt.Sprintf("backend=%q server=%q code=%q frontend=%q host=%q path=%q method=%q listener=%q",
		r.beName,
		r.beServer,
		r.beStatusCode,
		r.feName,
		r.feHost,
		r.fePath,
		r.feStatusMethod,
		r.leName,
	)
}

func splitHTTPHeader(rd *bufio.Reader) (statusLine string, hdr http.Header, nr int64, err error) {
	hdr = make(http.Header, 16)
	line := []byte(nil)
	for {
		var ln []byte
		ln, err = rd.ReadSlice('\n')
		nr += int64(len(ln))
		if nr > maxHTTPHeadersLen {
			err = newHTTPError("protocol", "max headers length exceeded")
			break
		}
		if err != nil && err != bufio.ErrBufferFull {
			err = wrapHTTPError("communication", err)
			break
		}
		n := len(line)
		m := n + len(ln)
		if m > maxHTTPHeaderLineLen {
			err = newHTTPError("protocol", "max header line length exceeded")
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
			hdr.Add(name, value)
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
		err = wrapHTTPError("communication", err)
		return
	}
	err = srcHdr.Write(dstSW)
	if err != nil {
		nw = dstSW.N
		err = wrapHTTPError("communication", err)
		return
	}
	_, err = dstSW.Write([]byte("\r\n"))
	if err != nil {
		nw = dstSW.N
		return
	}
	if dstWr, ok := dst.(*bufio.Writer); ok {
		if e := dstWr.Flush(); e != nil && err == nil {
			err = wrapHTTPError("communication", e)
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
			err = wrapHTTPError("communication", err)
		} else {
			nw, err = io.CopyN(dst, src, contentLength)
			if err != nil {
				err = wrapHTTPError("communication", err)
			}
		}
	case "chunked":
		srcCk := httputil.NewChunkedReader(src)
		dstSW := &statsWriter{
			W: dst,
		}
		dstCk := httputil.NewChunkedWriter(dstSW)
		_, err = io.Copy(dstCk, srcCk)
		if err != nil {
			nw = dstSW.N
			err = wrapHTTPError("communication", err)
			break
		}
		err = dstCk.Close()
		if err != nil {
			nw = dstSW.N
			err = wrapHTTPError("communication", err)
			break
		}
		var crlfBuf [2]byte
		var n int
		n, err = src.Read(crlfBuf[:])
		if err != nil {
			nw = dstSW.N
			err = wrapHTTPError("communication", err)
			break
		}
		if n <= 0 || string(crlfBuf[:n]) != "\r\n" {
			nw = dstSW.N
			err = errHTTPChunkedTransferEncoding
			break
		}
		_, err = dstSW.Write(crlfBuf[:n])
		if err != nil {
			nw = dstSW.N
			err = wrapHTTPError("communication", err)
			break
		}
		nw = dstSW.N
	default:
		err = errHTTPUnsupportedTransferEncoding
	}
	if dstWr, ok := dst.(*bufio.Writer); ok {
		if e := dstWr.Flush(); e != nil && err == nil {
			err = wrapHTTPError("communication", e)
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
		err = newfHTTPError("protocol", "content-length parse error: %w", err)
		return
	}
	contentLength = int64(ui64)
	if contentLength < 0 {
		contentLength = -1
		err = newfHTTPError("protocol", "content-length out of range")
		return
	}
	return
}
