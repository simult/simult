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
	promHTTPFrontendActiveConnections      *prometheus.GaugeVec
	promHTTPBackendReadBytes               *prometheus.CounterVec
	promHTTPBackendWriteBytes              *prometheus.CounterVec
	promHTTPBackendRequestsTotal           *prometheus.CounterVec
	promHTTPBackendRequestDurationSeconds  *prometheus.HistogramVec
	promHTTPBackendTimeToFirstByteSeconds  *prometheus.HistogramVec
	promHTTPBackendActiveConnections       *prometheus.GaugeVec
)

func PromInitialize(namespace string) {
	if !atomic.CompareAndSwapUint32(&promInitialized, 0, 1) {
		panic("prometheus already set")
	}

	histogramBuckets := prometheus.LinearBuckets(0.05, 0.05, 20)
	for i := range histogramBuckets {
		x := &histogramBuckets[i]
		*x = roundP(*x, 2)
	}
	histogramBuckets = append([]float64{.005, .01, .025}, append(histogramBuckets, []float64{2.5, 5, 10}...)...)

	promHTTPFrontendReadBytes = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "http_frontend",
		Name:      "read_bytes",
	}, []string{"name", "address", "host", "path", "backend", "method", "code"})

	promHTTPFrontendWriteBytes = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "http_frontend",
		Name:      "write_bytes",
	}, []string{"name", "address", "host", "path", "backend", "method", "code"})

	promHTTPFrontendRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "http_frontend",
		Name:      "requests_total",
	}, []string{"name", "address", "host", "path", "backend", "method", "code", "error"})

	promHTTPFrontendRequestDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: "http_frontend",
		Name:      "request_duration_seconds",
		Buckets:   histogramBuckets,
	}, []string{"name", "address", "host", "path", "backend", "method", "code"})

	promHTTPFrontendActiveConnections = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: "http_frontend",
		Name:      "active_connections",
	}, []string{"name"})

	promHTTPBackendReadBytes = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "http_backend",
		Name:      "read_bytes",
	}, []string{"name", "server", "frontend", "method", "code"})

	promHTTPBackendWriteBytes = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "http_backend",
		Name:      "write_bytes",
	}, []string{"name", "server", "frontend", "method", "code"})

	promHTTPBackendRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "http_backend",
		Name:      "requests_total",
	}, []string{"name", "server", "frontend", "method", "code", "error"})

	promHTTPBackendRequestDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: "http_backend",
		Name:      "request_duration_seconds",
		Buckets:   histogramBuckets,
	}, []string{"name", "server", "frontend", "method", "code"})

	promHTTPBackendTimeToFirstByteSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: "http_backend",
		Name:      "time_to_first_byte_seconds",
		Buckets:   histogramBuckets,
	}, []string{"name", "server", "frontend", "method", "code"})

	promHTTPBackendActiveConnections = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: "http_backend",
		Name:      "active_connections",
	}, []string{"name", "server"})
}

func PromReset() {
	promHTTPFrontendReadBytes.Reset()
	promHTTPFrontendWriteBytes.Reset()
	promHTTPFrontendRequestsTotal.Reset()
	promHTTPFrontendRequestDurationSeconds.Reset()
	//promHTTPFrontendActiveConnections.Reset()
	promHTTPBackendReadBytes.Reset()
	promHTTPBackendWriteBytes.Reset()
	promHTTPBackendRequestsTotal.Reset()
	promHTTPBackendRequestDurationSeconds.Reset()
	promHTTPBackendTimeToFirstByteSeconds.Reset()
	//promHTTPBackendActiveConnections.Reset()
}
