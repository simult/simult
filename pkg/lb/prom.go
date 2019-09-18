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
	promHTTPBackendReadBytes               *prometheus.CounterVec
	promHTTPBackendWriteBytes              *prometheus.CounterVec
	promHTTPBackendRequestsTotal           *prometheus.CounterVec
	promHTTPBackendRequestDurationSeconds  *prometheus.HistogramVec
)

func PromInitialize(namespace string) {
	if !atomic.CompareAndSwapUint32(&promInitialized, 0, 1) {
		panic("prometheus already set")
	}

	promHTTPFrontendReadBytes = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "http_frontend",
		Name:      "read_bytes",
	}, []string{"name", "address"})

	promHTTPFrontendWriteBytes = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "http_frontend",
		Name:      "write_bytes",
	}, []string{"name", "address"})

	promHTTPFrontendRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "http_frontend",
		Name:      "requests_total",
	}, []string{"name", "address", "method", "code"})

	promHTTPFrontendRequestDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: "http_frontend",
		Name:      "request_duration_seconds",
		Buckets:   prometheus.LinearBuckets(0.02, 0.02, 50),
	}, []string{"name", "address", "method", "code"})

	promHTTPBackendReadBytes = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "http_backend",
		Name:      "read_bytes",
	}, []string{"name", "server"})

	promHTTPBackendWriteBytes = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "http_backend",
		Name:      "write_bytes",
	}, []string{"name", "server"})

	promHTTPBackendRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "http_backend",
		Name:      "requests_total",
	}, []string{"name", "server", "method", "code"})

	promHTTPBackendRequestDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: "http_backend",
		Name:      "request_duration_seconds",
		Buckets:   prometheus.LinearBuckets(0.02, 0.02, 50),
	}, []string{"name", "server", "method", "code"})
}

func PromReset() {
	promHTTPFrontendReadBytes.Reset()
	promHTTPFrontendWriteBytes.Reset()
	promHTTPFrontendRequestsTotal.Reset()
	promHTTPFrontendRequestDurationSeconds.Reset()
	promHTTPBackendReadBytes.Reset()
	promHTTPBackendWriteBytes.Reset()
	promHTTPBackendRequestsTotal.Reset()
	promHTTPBackendRequestDurationSeconds.Reset()
}
