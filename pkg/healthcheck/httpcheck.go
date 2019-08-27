package healthcheck

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

type HTTPCheck struct {
	C <-chan bool

	c               chan bool
	opts            HTTPCheckOptions
	client          *http.Client
	tmr             *time.Timer
	workerCtx       context.Context
	workerCtxCancel context.CancelFunc
	workerClosedCh  chan struct{}
	healthy         bool
	healthyMu       sync.RWMutex
	lastCheck       bool
	falls, rises    int
}

func New(opts HTTPCheckOptions) (hc *HTTPCheck) {
	c := make(chan bool, 1)
	hc = &HTTPCheck{
		C:    c,
		c:    c,
		opts: opts,
	}
	hc.client = &http.Client{
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   hc.opts.Timeout,
				KeepAlive: hc.opts.Timeout / 2,
				DualStack: true,
			}).DialContext,
			DisableKeepAlives:     true,
			TLSHandshakeTimeout:   hc.opts.Timeout,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
	hc.tmr = time.NewTimer(hc.opts.Interval)
	hc.workerCtx, hc.workerCtxCancel = context.WithCancel(context.Background())
	hc.workerClosedCh = make(chan struct{})
	go hc.worker()
	return
}

func (hc *HTTPCheck) Stop() {
	hc.tmr.Stop()
	hc.workerCtxCancel()
	<-hc.workerClosedCh
}

func (hc *HTTPCheck) Healthy() bool {
	hc.healthyMu.RLock()
	r := hc.healthy
	hc.healthyMu.RUnlock()
	return r
}

func (hc *HTTPCheck) check() (ok bool, err error) {
	req, err := http.NewRequest(http.MethodGet, hc.opts.URL, nil)
	if err != nil {
		return
	}
	if hc.opts.HostHeader != "" {
		req.Host = hc.opts.HostHeader
	}
	ctx, cancel := context.WithTimeout(context.Background(), hc.opts.Timeout)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := hc.client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return
	}
	if hc.opts.RespBody == nil {
		ok = true
		return
	}
	body, err := ioutil.ReadAll(io.LimitReader(resp.Body, int64(len(hc.opts.RespBody)+1)))
	if err != nil {
		return
	}
	ok = bytes.Equal(hc.opts.RespBody, body)
	return
}

func (hc *HTTPCheck) worker() {
	select {
	case hc.c <- hc.healthy:
	default:
	}
	for done := false; !done; {
		select {
		case <-hc.tmr.C:
			ok, _ := hc.check()
			if ok != hc.lastCheck {
				hc.falls = 0
				hc.rises = 0
			}
			if !ok {
				hc.falls++
			} else {
				hc.rises++
			}
			if hc.healthy {
				if hc.opts.FallThreshold > 0 && hc.falls >= hc.opts.FallThreshold {
					hc.healthyMu.Lock()
					hc.healthy = false
					hc.healthyMu.Unlock()
					select {
					case hc.c <- hc.healthy:
					default:
					}
				}
			} else {
				if hc.rises >= hc.opts.RiseThreshold {
					hc.healthyMu.Lock()
					hc.healthy = true
					hc.healthyMu.Unlock()
					select {
					case hc.c <- hc.healthy:
					default:
					}
				}
			}
			hc.lastCheck = ok
			hc.tmr.Reset(hc.opts.Interval)
		case <-hc.workerCtx.Done():
			done = true
		}
	}
	close(hc.workerClosedCh)
}
