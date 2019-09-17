package lb

import (
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	promInitialized                        uint32
	promHTTPFrontendReadBytes              *prometheus.CounterVec
	promHTTPFrontendWriteBytes             *prometheus.CounterVec
	promHTTPFrontendRequestsTotal          *prometheus.CounterVec
	promHTTPFrontendRequestDurationSeconds *prometheus.HistogramVec
)

func PromInitialize(namespace string) {
	if !atomic.CompareAndSwapUint32(&promInitialized, 0, 1) {
		panic("prometheus already set")
	}

	promHTTPFrontendReadBytes = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "httpfrontend",
		Name:      "read_bytes",
	}, []string{"name"})

	promHTTPFrontendWriteBytes = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "httpfrontend",
		Name:      "write_bytes",
	}, []string{"name"})

	promHTTPFrontendRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "httpfrontend",
		Name:      "requests_total",
	}, []string{"name", "method", "code"})

	promHTTPFrontendRequestDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: "httpfrontend",
		Name:      "request_duration_seconds",
		Buckets:   prometheus.LinearBuckets(0.02, 0.02, 50),
	}, []string{"name", "method", "code"})
}

func PromReset() {
	promHTTPFrontendReadBytes.Reset()
	promHTTPFrontendWriteBytes.Reset()
	promHTTPFrontendRequestsTotal.Reset()
	promHTTPFrontendRequestDurationSeconds.Reset()
}
