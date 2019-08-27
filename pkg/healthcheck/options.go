package healthcheck

import "time"

type HTTPCheckOptions struct {
	URL, HostHeader              string
	Interval, Timeout            time.Duration
	FallThreshold, RiseThreshold int
	RespBody                     []byte
}
