package lb

import (
	"sync/atomic"

	"github.com/goinsane/xmath"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	promInitialized                         uint32
	promHTTPFrontendReadBytes               *prometheus.CounterVec
	promHTTPFrontendWriteBytes              *prometheus.CounterVec
	promHTTPFrontendRequestsTotal           *prometheus.CounterVec
	promHTTPFrontendRequestDurationSeconds  *prometheus.HistogramVec
	promHTTPFrontendConnectionsTotal        *prometheus.CounterVec
	promHTTPFrontendDroppedConnectionsTotal *prometheus.CounterVec
	promHTTPFrontendActiveConnections       *prometheus.GaugeVec
	promHTTPFrontendIdleConnections         *prometheus.GaugeVec
	promHTTPBackendReadBytes                *prometheus.CounterVec
	promHTTPBackendWriteBytes               *prometheus.CounterVec
	promHTTPBackendRequestsTotal            *prometheus.CounterVec
	promHTTPBackendRequestDurationSeconds   *prometheus.HistogramVec
	promHTTPBackendTimeToFirstByteSeconds   *prometheus.HistogramVec
	promHTTPBackendActiveConnections        *prometheus.GaugeVec
	promHTTPBackendIdleConnections          *prometheus.GaugeVec
	promHTTPBackendServerHealth             *prometheus.GaugeVec
)

// PromInitialize initializes prometheus metrics with given namespace. If metrics is initialized, it panics.
func PromInitialize(namespace string) {
	if !atomic.CompareAndSwapUint32(&promInitialized, 0, 1) {
		panic("prometheus already set")
	}

	histogramBuckets := prometheus.LinearBuckets(0.05, 0.05, 20)
	for i := range histogramBuckets {
		x := &histogramBuckets[i]
		*x = xmath.RoundP(*x, 2)
	}
	histogramBuckets = append([]float64{.005, .01, .025}, append(histogramBuckets, []float64{2.5, 5, 10, 25, 50, 100}...)...)

	promHTTPFrontendReadBytes = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "http_frontend",
		Name:      "read_bytes",
	}, []string{"frontend", "host", "path", "method", "backend", "server", "code", "listener"})

	promHTTPFrontendWriteBytes = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "http_frontend",
		Name:      "write_bytes",
	}, []string{"frontend", "host", "path", "method", "backend", "server", "code", "listener"})

	promHTTPFrontendRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "http_frontend",
		Name:      "requests_total",
	}, []string{"frontend", "host", "path", "method", "backend", "server", "code", "listener", "error"})

	promHTTPFrontendRequestDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: "http_frontend",
		Name:      "request_duration_seconds",
		Buckets:   histogramBuckets,
	}, []string{"frontend", "host", "path", "method", "backend", "server", "code", "listener"})

	promHTTPFrontendConnectionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "http_frontend",
		Name:      "connections_total",
	}, []string{"frontend", "listener"})

	promHTTPFrontendDroppedConnectionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "http_frontend",
		Name:      "dropped_connections_total",
	}, []string{"frontend", "listener"})

	promHTTPFrontendActiveConnections = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: "http_frontend",
		Name:      "active_connections",
	}, []string{"frontend", "listener"})

	promHTTPFrontendIdleConnections = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: "http_frontend",
		Name:      "idle_connections",
	}, []string{"frontend", "listener"})

	promHTTPBackendReadBytes = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "http_backend",
		Name:      "read_bytes",
	}, []string{"backend", "server", "code", "frontend", "host", "path", "method", "listener"})

	promHTTPBackendWriteBytes = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "http_backend",
		Name:      "write_bytes",
	}, []string{"backend", "server", "code", "frontend", "host", "path", "method", "listener"})

	promHTTPBackendRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "http_backend",
		Name:      "requests_total",
	}, []string{"backend", "server", "code", "frontend", "host", "path", "method", "listener", "error"})

	promHTTPBackendRequestDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: "http_backend",
		Name:      "request_duration_seconds",
		Buckets:   histogramBuckets,
	}, []string{"backend", "server", "code", "frontend", "host", "path", "method", "listener"})

	promHTTPBackendTimeToFirstByteSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: "http_backend",
		Name:      "time_to_first_byte_seconds",
		Buckets:   histogramBuckets,
	}, []string{"backend", "server", "code", "frontend", "host", "path", "method", "listener"})

	promHTTPBackendActiveConnections = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: "http_backend",
		Name:      "active_connections",
	}, []string{"backend", "server"})

	promHTTPBackendIdleConnections = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: "http_backend",
		Name:      "idle_connections",
	}, []string{"backend", "server"})

	promHTTPBackendServerHealth = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: "http_backend",
		Name:      "server_health",
	}, []string{"backend", "server"})
}

// PromReset resets prometheus metrics other than gauge metrics
func PromReset() {
	promHTTPFrontendReadBytes.Reset()
	promHTTPFrontendWriteBytes.Reset()
	promHTTPFrontendRequestsTotal.Reset()
	promHTTPFrontendRequestDurationSeconds.Reset()
	promHTTPFrontendConnectionsTotal.Reset()
	promHTTPFrontendDroppedConnectionsTotal.Reset()
	//promHTTPFrontendActiveConnections.Reset()
	//promHTTPFrontendIdleConnections.Reset()
	promHTTPBackendReadBytes.Reset()
	promHTTPBackendWriteBytes.Reset()
	promHTTPBackendRequestsTotal.Reset()
	promHTTPBackendRequestDurationSeconds.Reset()
	promHTTPBackendTimeToFirstByteSeconds.Reset()
	//promHTTPBackendActiveConnections.Reset()
	//promHTTPBackendIdleConnections.Reset()
	//promHTTPBackendServerHealth.Reset()
}
