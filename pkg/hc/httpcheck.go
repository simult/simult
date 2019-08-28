package hc

import (
	"bytes"
	"context"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"sync"
	"time"
)

type HTTPCheckOptions struct {
	Server, HeaderHost           string
	Interval, Timeout            time.Duration
	FallThreshold, RiseThreshold int
	RespBody                     []byte
}

type HTTPCheck struct {
	C <-chan bool

	c               chan bool
	opts            HTTPCheckOptions
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

func New(opts HTTPCheckOptions) (h *HTTPCheck) {
	c := make(chan bool, 1)
	h = &HTTPCheck{
		C:    c,
		c:    c,
		opts: opts,
	}
	h.client = &http.Client{
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   h.opts.Timeout,
				KeepAlive: h.opts.Timeout / 2,
				DualStack: true,
			}).DialContext,
			DisableKeepAlives:     true,
			TLSHandshakeTimeout:   h.opts.Timeout,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
	h.tmr = time.NewTimer(h.opts.Interval)
	h.workerCtx, h.workerCtxCancel = context.WithCancel(context.Background())
	h.workerWg.Add(1)
	go h.worker()
	return
}

func (h *HTTPCheck) Close() {
	h.tmr.Stop()
	h.workerCtxCancel()
	h.workerWg.Wait()
}

func (h *HTTPCheck) Healthy() bool {
	h.healthyMu.RLock()
	r := h.healthy
	h.healthyMu.RUnlock()
	return r
}

func (h *HTTPCheck) check() (ok bool, err error) {
	req, err := http.NewRequest(http.MethodGet, h.opts.Server, nil)
	if err != nil {
		return
	}
	if h.opts.HeaderHost != "" {
		req.Host = h.opts.HeaderHost
	}
	ctx, cancel := context.WithTimeout(context.Background(), h.opts.Timeout)
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

func (h *HTTPCheck) worker() {
	select {
	case h.c <- h.healthy:
	default:
	}
	for done := false; !done; {
		select {
		case <-h.tmr.C:
			ok, _ := h.check()
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
		case <-h.workerCtx.Done():
			done = true
		}
	}
	h.workerWg.Done()
}
