package httpcheck

import (
	"bytes"
	"context"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"time"
)

type HTTPCheck struct {
	C <-chan bool

	c                             chan bool
	url, hostHeader               string
	interval, timeout             time.Duration
	fallThreshold, riseThreshold  int
	respBody                      []byte
	healthy                       bool
	lastCheck                     bool
	falls, rises                  int
	client                        *http.Client
	tmr                           *time.Timer
	workerCloseCh, workerClosedCh chan struct{}
}

func New(url, hostHeader string, interval, timeout time.Duration, fallThreshold, riseThreshold int, respBody []byte) (hc *HTTPCheck) {
	c := make(chan bool, 1)
	hc = &HTTPCheck{
		C:             c,
		c:             c,
		url:           url,
		hostHeader:    hostHeader,
		interval:      interval,
		timeout:       timeout,
		fallThreshold: fallThreshold,
		riseThreshold: riseThreshold,
		respBody:      respBody,
	}
	hc.client = &http.Client{
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   timeout,
				KeepAlive: timeout / 2,
				DualStack: true,
			}).DialContext,
			DisableKeepAlives:     true,
			TLSHandshakeTimeout:   timeout,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
	hc.tmr = time.NewTimer(interval)
	hc.workerCloseCh = make(chan struct{}, 1)
	hc.workerClosedCh = make(chan struct{})
	go hc.worker()
	return
}

func (hc *HTTPCheck) Stop() {
	hc.tmr.Stop()
	select {
	case hc.workerCloseCh <- struct{}{}:
	default:
	}
	<-hc.workerClosedCh
}

func (hc *HTTPCheck) check() (ok bool, err error) {
	req, err := http.NewRequest(http.MethodGet, hc.url, nil)
	if err != nil {
		return
	}
	if hc.hostHeader != "" {
		req.Host = hc.hostHeader
	}
	ctx, cancel := context.WithTimeout(context.Background(), hc.timeout)
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
	if hc.respBody == nil {
		ok = true
		return
	}
	body, err := ioutil.ReadAll(io.LimitReader(resp.Body, int64(len(hc.respBody)+1)))
	if err != nil {
		return
	}
	ok = bytes.Equal(hc.respBody, body)
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
				if hc.fallThreshold > 0 && hc.falls >= hc.fallThreshold {
					hc.healthy = false
					select {
					case hc.c <- hc.healthy:
					default:
					}
				}
			} else {
				if hc.rises >= hc.riseThreshold {
					hc.healthy = true
					select {
					case hc.c <- hc.healthy:
					default:
					}
				}
			}
			hc.lastCheck = ok
			hc.tmr.Reset(hc.interval)
		case <-hc.workerCloseCh:
			done = true
		}
	}
	close(hc.workerClosedCh)
}
