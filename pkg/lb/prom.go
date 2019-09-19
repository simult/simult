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
	promHTTPFrontendErrorsTotal            *prometheus.CounterVec
	promHTTPFrontendTimeoutsTotal          *prometheus.CounterVec
	promHTTPBackendReadBytes               *prometheus.CounterVec
	promHTTPBackendWriteBytes              *prometheus.CounterVec
	promHTTPBackendRequestsTotal           *prometheus.CounterVec
	promHTTPBackendRequestDurationSeconds  *prometheus.HistogramVec
	promHTTPBackendErrorsTotal             *prometheus.CounterVec
	promHTTPBackendTimeoutsTotal           *prometheus.CounterVec
)

func PromInitialize(namespace string) {
	if !atomic.CompareAndSwapUint32(&promInitialized, 0, 1) {
		panic("prometheus already set")
	}

	histogramBuckets := prometheus.LinearBuckets(0.05, 0.05, 20)
	for i := range histogramBuckets {
		x := &histogramBuckets[i]
		*x = roundP(*x, -2)
	}
	histogramBuckets = append([]float64{.005, .01, .025}, append(histogramBuckets, []float64{2.5, 5, 10}...)...)

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
		Buckets:   histogramBuckets,
	}, []string{"name", "address", "method", "code"})

	promHTTPFrontendErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "http_frontend",
		Name:      "errors_total",
	}, []string{"name", "address", "method", "code"})

	promHTTPFrontendTimeoutsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "http_frontend",
		Name:      "timeouts_total",
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
		Buckets:   histogramBuckets,
	}, []string{"name", "server", "method", "code"})

	promHTTPBackendErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "http_backend",
		Name:      "errors_total",
	}, []string{"name", "server", "method", "code"})

	promHTTPBackendTimeoutsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "http_backend",
		Name:      "timeouts_total",
	}, []string{"name", "server", "method", "code"})
}

func PromReset() {
	promHTTPFrontendReadBytes.Reset()
	promHTTPFrontendWriteBytes.Reset()
	promHTTPFrontendRequestsTotal.Reset()
	promHTTPFrontendRequestDurationSeconds.Reset()
	promHTTPFrontendErrorsTotal.Reset()
	promHTTPFrontendTimeoutsTotal.Reset()
	promHTTPBackendReadBytes.Reset()
	promHTTPBackendWriteBytes.Reset()
	promHTTPBackendRequestsTotal.Reset()
	promHTTPBackendRequestDurationSeconds.Reset()
	promHTTPBackendErrorsTotal.Reset()
	promHTTPBackendTimeoutsTotal.Reset()
}
