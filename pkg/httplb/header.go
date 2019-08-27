package httplb

import (
	"bufio"
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/pkg/errors"
)

type headerLine struct {
	Line  []byte
	Name  string
	Value string
}

func splitHeader(rd *bufio.Reader) (hls []headerLine, hdr http.Header, err error) {
	hls, hdr = make([]headerLine, 0, 16), make(http.Header)
	line := make([]byte, 0, maxLineLen)
	for {
		var ln []byte
		ln, err = rd.ReadSlice('\n')
		if err != nil {
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
		hl := headerLine{
			Line: make([]byte, m),
		}
		copy(hl.Line, line)
		n = 1
		if m >= 2 && line[m-2] == '\r' {
			n = 2
		}
		line = line[:m-n]
		if len(hls) > 0 {
			idx := bytes.IndexByte(line, ':')
			if idx < 0 {
				hl.Name = string(line)
			} else {
				hl.Name = string(line[:idx])
				hl.Value = string(bytes.TrimLeft(line[idx+1:], " "))
			}
			if len(line) > 0 {
				hdr.Add(hl.Name, hl.Value)
			}
		} else {
			bb := bytes.SplitN(line, []byte{' '}, 3)
			if len(bb) > 0 && strings.HasPrefix(strings.ToUpper(string(bb[0])), "HTTP") {
				if len(bb) > 1 {
					hl.Name = string(bb[1])
				}
				if len(bb) > 2 {
					hl.Value = string(bb[2])
				}
			} else {
				if len(bb) > 0 {
					hl.Name = string(bb[0])
				}
				if len(bb) > 1 {
					hl.Value = string(bb[1])
				}
			}
		}
		hls = append(hls, hl)
		if len(line) == 0 {
			break
		}
		line = line[:0]
	}
	return
}

func writeHeaderLines(dst io.Writer, srcHls []headerLine) (nn int64, err error) {
	for i := range srcHls {
		hl := &srcHls[i]
		var n int
		n, err = dst.Write(hl.Line)
		nn += int64(n)
		if err != nil {
			err = errors.WithStack(err)
			break
		}
	}
	if dstWr, ok := dst.(*bufio.Writer); ok {
		if e := dstWr.Flush(); err == nil {
			err = errors.WithStack(e)
		}
	}
	return
}
