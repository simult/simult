package hc

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"sync"
	"time"
)

type HTTPCheckOptions struct {
	Path, HeaderHost             string
	Interval, Timeout            time.Duration
	FallThreshold, RiseThreshold int
	RespBody                     []byte
}

func (o *HTTPCheckOptions) CopyFrom(src *HTTPCheckOptions) {
	if src == nil {
		src = &HTTPCheckOptions{}
	}
	*o = *src
	if o.Path == "" {
		o.Path = "/"
	}
	if o.Path[0] != '/' {
		o.Path = "/" + o.Path
	}
	if o.Interval <= 0 {
		o.Interval = 10 * time.Second
	}
	if o.Timeout <= 0 {
		o.Timeout = 5 * time.Second
	}
	if o.FallThreshold <= 0 {
		o.FallThreshold = 3
	}
	if o.RiseThreshold <= 0 {
		o.RiseThreshold = 2
	}
	if src.RespBody != nil {
		o.RespBody = make([]byte, len(src.RespBody))
		copy(o.RespBody, src.RespBody)
	}
}

type HTTPCheck struct {
	server          string
	opts            HTTPCheckOptions
	client          *http.Client
	c               chan bool
	tmr             *time.Timer
	workerCtx       context.Context
	workerCtxCancel context.CancelFunc
	workerWg        sync.WaitGroup
	healthy         bool
	healthyMu       sync.RWMutex
	lastCheck       bool
	falls, rises    int
}

func NewHTTPCheck(server string, opts HTTPCheckOptions) (h *HTTPCheck) {
	h = &HTTPCheck{
		server: server,
	}
	h.opts.CopyFrom(&opts)
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
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	h.c = make(chan bool, 1)
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
	early := 0
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
					early = -1
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
					early = -1
				} else {
					if early >= 0 {
						early++
						if h.opts.FallThreshold <= 0 || early >= h.opts.FallThreshold {
							select {
							case h.c <- h.healthy:
							default:
							}
							early = -1
						}
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
