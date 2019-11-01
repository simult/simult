package lb

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
)

var (
	httpBadRequest          = "HTTP/1.0 400 Bad Request\r\n\r\nBad Request\r\n"
	httpForbidden           = "HTTP/1.0 403 Forbidden\r\n\r\nForbidden\r\n"
	httpRequestTimeout      = "HTTP/1.0 408 Request Timeout\r\n\r\nRequest Timeout\r\n"
	httpBadGateway          = "HTTP/1.0 502 Bad Gateway\r\n\r\nBad Gateway\r\n"
	httpServiceUnavailable  = "HTTP/1.0 503 Service Unavailable\r\n\r\nService Unavailable\r\n"
	httpGatewayTimeout      = "HTTP/1.0 504 Gateway Timeout\r\n\r\nGateway Timeout\r\n"
	httpVersionNotSupported = "HTTP/1.0 505 HTTP Version Not Supported\r\n\r\nHTTP Version Not Supported\r\n"
)

var (
	httpErrGroupProtocol               = "protocol"
	httpErrGroupCommunication          = "communication"
	httpErrGroupRestricted             = "restricted"
	httpErrGroupFrontendTimeout        = "frontend timeout"
	httpErrGroupFrontendRequestTimeout = "frontend request timeout"
	httpErrGroupBackendTimeout         = "backend timeout"
	httpErrGroupBackendFind            = "backend find"
	httpErrGroupBackendConnect         = "backend connect"
	httpErrGroupBackendConnectTimeout  = "backend connect timeout"
	httpErrGroupFrontendExhausted      = "frontend exhausted"
	httpErrGroupBackendExhausted       = "backend exhausted"
	httpErrGroupBackendServerExhausted = "backend server exhausted"
)

var (
	errHTTPChunkedTransferEncoding     = newHTTPError(httpErrGroupProtocol, "chunked transfer encoding error")
	errHTTPUnsupportedTransferEncoding = newHTTPError(httpErrGroupProtocol, "unsupported transfer encoding")
	errHTTPStatusLineFormat            = newHTTPError(httpErrGroupProtocol, "status line format error")
	errHTTPStatusLineURI               = newHTTPError(httpErrGroupProtocol, "status line URI error")
	errHTTPStatusLineVersion           = newHTTPError(httpErrGroupProtocol, "status line HTTP version error")
	errHTTPRestrictedRequest           = newHTTPError(httpErrGroupRestricted, "restricted request")
	errHTTPBufferOrder                 = newHTTPError(httpErrGroupProtocol, "buffer order error")
	errHTTPFrontendTimeout             = newHTTPError(httpErrGroupFrontendTimeout, "timeout exceeded")
	errHTTPFrontendRequestTimeout      = newHTTPError(httpErrGroupFrontendRequestTimeout, "request timeout exceeded")
	errHTTPBackendTimeout              = newHTTPError(httpErrGroupBackendTimeout, "timeout exceeded")
	errHTTPBackendFind                 = newHTTPError(httpErrGroupBackendFind, "unable to find backend server")
	errHTTPFrontendExhausted           = newHTTPError(httpErrGroupFrontendExhausted, "frontend maximum connection exceeded")
	errHTTPBackendExhausted            = newHTTPError(httpErrGroupBackendExhausted, "backend maximum connection exceeded")
	errHTTPBackendServerExhausted      = newHTTPError(httpErrGroupBackendServerExhausted, "backend server maximum connection exceeded")
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
	leName                string
	leTLS                 bool
	feName                string
	feConn                *bufConn
	feStatusLine          string
	feStatusMethod        string
	feStatusURI           string
	feStatusVersion       string
	feStatusMethodGrouped string
	feHdr                 http.Header
	feURL                 *url.URL
	feCookies             []*http.Cookie
	feRemoteIP            string
	feRealIP              string
	feHost                string
	fePath                string
	beName                string
	beServer              string
	beConn                *bufConn
	beStatusLine          string
	beStatusVersion       string
	beStatusCode          string
	beStatusMsg           string
	beStatusCodeGrouped   string
	beHdr                 http.Header
	isTransferErrLogged   uint32
}

func (r *httpReqDesc) FrontendSummary() string {
	return fmt.Sprintf("frontend=%q host=%q path=%q method=%q listener=%q remoteaddr=%q",
		r.feName,
		r.feHost,
		r.fePath,
		r.feStatusMethod,
		r.leName,
		r.feConn.RemoteAddr().String(),
	)
}

func (r *httpReqDesc) BackendSummary() string {
	return fmt.Sprintf("backend=%q server=%q code=%q frontend=%q host=%q path=%q method=%q listener=%q remoteaddr=%q",
		r.beName,
		r.beServer,
		r.beStatusCode,
		r.feName,
		r.feHost,
		r.fePath,
		r.feStatusMethod,
		r.leName,
		r.feConn.RemoteAddr().String(),
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
			err = newHTTPError(httpErrGroupProtocol, "max headers length exceeded")
			break
		}
		if err != nil && err != bufio.ErrBufferFull {
			err = wrapHTTPError(httpErrGroupCommunication, err)
			break
		}
		n := len(line)
		m := n + len(ln)
		if m > maxHTTPHeaderLineLen {
			err = newHTTPError(httpErrGroupProtocol, "max header line length exceeded")
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
		err = wrapHTTPError(httpErrGroupCommunication, err)
		return
	}
	err = srcHdr.Write(dstSW)
	if err != nil {
		nw = dstSW.N
		err = wrapHTTPError(httpErrGroupCommunication, err)
		return
	}
	_, err = dstSW.Write([]byte("\r\n"))
	if err != nil {
		nw = dstSW.N
		return
	}
	if dstWr, ok := dst.(*bufio.Writer); ok {
		if e := dstWr.Flush(); e != nil && err == nil {
			err = wrapHTTPError(httpErrGroupCommunication, e)
		}
	}
	nw = dstSW.N
	return
}

func writeHTTPBody(dst io.Writer, src *bufio.Reader, contentLength int64, transferEncoding string) (nw int64, err error) {
	if contentLength == 0 {
		return
	}
	switch transferEncoding {
	case "":
		if contentLength < 0 {
			nw, err = io.Copy(dst, src)
			if err == nil {
				err = errExpectedEOF
			}
			err = wrapHTTPError(httpErrGroupCommunication, err)
		} else {
			nw, err = io.CopyN(dst, src, contentLength)
			if err != nil {
				err = wrapHTTPError(httpErrGroupCommunication, err)
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
			err = wrapHTTPError(httpErrGroupCommunication, err)
			break
		}
		err = dstCk.Close()
		if err != nil {
			nw = dstSW.N
			err = wrapHTTPError(httpErrGroupCommunication, err)
			break
		}
		var crlfBuf [2]byte
		var n int
		n, err = src.Read(crlfBuf[:])
		if err != nil {
			nw = dstSW.N
			err = wrapHTTPError(httpErrGroupCommunication, err)
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
			err = wrapHTTPError(httpErrGroupCommunication, err)
			break
		}
		nw = dstSW.N
	default:
		err = errHTTPUnsupportedTransferEncoding
	}
	if dstWr, ok := dst.(*bufio.Writer); ok {
		if e := dstWr.Flush(); e != nil && err == nil {
			err = wrapHTTPError(httpErrGroupCommunication, e)
		}
	}
	return
}

func normalizePath(path string) string {
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

func uriToPath(uri string) string {
	return normalizePath(strings.SplitN(uri, "?", 2)[0])
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
		err = newfHTTPError(httpErrGroupProtocol, "content-length parse error: %w", err)
		return
	}
	contentLength = int64(ui64)
	if contentLength < 0 {
		contentLength = -1
		err = newfHTTPError(httpErrGroupProtocol, "content-length out of range")
		return
	}
	return
}

func groupHTTPStatusCode(code string) string {
	if len(code) != 3 {
		return "xxx"
	}
	c := code[2]
	if !(c >= '0' && c <= '9') {
		return "xxx"
	}
	return string(c) + "xx"
}

func groupHTTPStatusMethod(method string) string {
	groupped := method
	switch groupped {
	case "GET":
	case "HEAD":
	case "POST":
	case "PUT":
	case "DELETE":
	case "CONNECT":
	case "OPTIONS":
	case "TRACE":
	case "PATCH":
	default:
		groupped = "unknown"
	}
	return groupped
}

func validOptionalPort(port string) bool {
	if port == "" {
		return true
	}
	if port[0] != ':' {
		return false
	}
	for _, b := range port[1:] {
		if b < '0' || b > '9' {
			return false
		}
	}
	return true
}

func splitHostPort(hostport string) (host, port string) {
	host = hostport

	colon := strings.LastIndexByte(host, ':')
	if colon != -1 && validOptionalPort(host[colon:]) {
		host, port = host[:colon], host[colon+1:]
	}

	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}

	return
}
