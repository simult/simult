package hc

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/pkg/errors"
)

type HTTPOptions struct {
	Path, HeaderHost             string
	Interval, Timeout            time.Duration
	FallThreshold, RiseThreshold int
	RespBody                     []byte
}

type HTTPCheck struct {
	c               chan bool
	server          string
	opts            HTTPOptions
	client          *http.Client
	tmr             *time.Timer
	workerCtx       context.Context
	workerCtxCancel context.CancelFunc
	workerWg        sync.WaitGroup
	healthy         bool
	healthyMu       sync.RWMutex
	lastCheck       bool
	falls, rises    int
}

func NewHTTPCheck(server string, opts HTTPOptions) (h *HTTPCheck, err error) {
	var serverURL *url.URL
	serverURL, err = url.Parse(server)
	if err != nil {
		err = errors.WithStack(err)
		return
	}
	if serverURL.Host == "" {
		err = errors.New("empty hostport")
		return
	}
	if serverURL.Scheme != "http" && serverURL.Scheme != "https" {
		err = errors.New("wrong scheme")
		return
	}
	h = &HTTPCheck{
		c:      make(chan bool),
		server: serverURL.Scheme + "://" + serverURL.Host,
		opts:   opts,
	}
	if h.opts.Path == "" {
		h.opts.Path = "/"
	}
	if h.opts.RespBody != nil {
		h.opts.RespBody = make([]byte, len(opts.RespBody))
		copy(h.opts.RespBody, opts.RespBody)
	}
	h.client = &http.Client{
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   h.opts.Timeout,
				KeepAlive: h.opts.Timeout / 2,
				DualStack: true,
			}).DialContext,
			DisableKeepAlives:     true,
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
			TLSHandshakeTimeout:   h.opts.Timeout,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
	h.tmr = time.NewTimer(h.opts.Interval)
	h.workerCtx, h.workerCtxCancel = context.WithCancel(context.Background())
	h.workerWg.Add(1)
	go h.worker(h.workerCtx)
	return
}

func (h *HTTPCheck) Close() {
	h.tmr.Stop()
	h.workerCtxCancel()
	h.workerWg.Wait()
	h.healthyMu.Lock()
	h.healthy = false
	h.healthyMu.Unlock()
}

func (h *HTTPCheck) Healthy() bool {
	h.healthyMu.RLock()
	r := h.healthy
	h.healthyMu.RUnlock()
	return r
}

func (h *HTTPCheck) Check() <-chan bool {
	return h.c
}

func (h *HTTPCheck) check(ctx context.Context) (ok bool, err error) {
	req, err := http.NewRequest(http.MethodGet, h.server+h.opts.Path, nil)
	if err != nil {
		return
	}
	if h.opts.HeaderHost != "" {
		req.Host = h.opts.HeaderHost
	}
	ctx, cancel := context.WithTimeout(ctx, h.opts.Timeout)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := h.client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return
	}
	if h.opts.RespBody == nil {
		ok = true
		return
	}
	body, err := ioutil.ReadAll(io.LimitReader(resp.Body, int64(len(h.opts.RespBody)+1)))
	if err != nil {
		return
	}
	ok = bytes.Equal(h.opts.RespBody, body)
	return
}

func (h *HTTPCheck) worker(ctx context.Context) {
	for done := false; !done; {
		select {
		case <-h.tmr.C:
			ok, _ := h.check(ctx)
			if ok != h.lastCheck {
				h.falls = 0
				h.rises = 0
			}
			if !ok {
				h.falls++
			} else {
				h.rises++
			}
			if h.healthy {
				if h.opts.FallThreshold > 0 && h.falls >= h.opts.FallThreshold {
					h.healthyMu.Lock()
					h.healthy = false
					h.healthyMu.Unlock()
					select {
					case h.c <- h.healthy:
					default:
					}
				}
			} else {
				if h.rises >= h.opts.RiseThreshold {
					h.healthyMu.Lock()
					h.healthy = true
					h.healthyMu.Unlock()
					select {
					case h.c <- h.healthy:
					default:
					}
				}
			}
			h.lastCheck = ok
			h.tmr.Reset(h.opts.Interval)
		case <-ctx.Done():
			done = true
		}
	}
	close(h.c)
	h.workerWg.Done()
}
