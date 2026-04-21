// Package metrics exposes the Prometheus counters and histograms that the
// proxy emits on the hot path. Keep the label cardinality bounded: model and
// provider are controlled (config file); status bucketed by class; no
// raw-user labels ever.
package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	RequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "frugal_requests_total",
		Help: "Chat completion requests by selected model, provider, quality tier, and status class (2xx/4xx/5xx).",
	}, []string{"model", "provider", "quality", "status_class"})

	RequestDurationSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "frugal_request_duration_seconds",
		Help:    "Wall-clock time for each chat completion request, labeled by model/provider/stream-vs-nonstream.",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30, 60, 120},
	}, []string{"model", "provider", "stream"})

	TokensTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "frugal_tokens_total",
		Help: "Tokens routed through the proxy, split by direction (prompt/completion).",
	}, []string{"model", "provider", "direction"})

	CostUSDTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "frugal_cost_usd_total",
		Help: "Estimated dollar cost of served requests (based on the routing-time estimate).",
	}, []string{"model", "provider"})

	RoutingRelaxedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "frugal_routing_relaxed_total",
		Help: "Count of requests where the router relaxed the requested quality tier to land a model.",
	}, []string{"from", "to"})
)

// Register wires every Frugal metric into the default registry. Safe to call
// more than once: duplicate registrations become a noop so tests can reuse.
func Register() {
	for _, c := range []prometheus.Collector{
		RequestsTotal, RequestDurationSeconds, TokensTotal, CostUSDTotal, RoutingRelaxedTotal,
	} {
		_ = prometheus.Register(c) // ignore AlreadyRegisteredError
	}
}

// Handler returns the /metrics HTTP handler.
func Handler() http.Handler {
	return promhttp.Handler()
}

// StatusClass bucketizes an HTTP status into 2xx/4xx/5xx so label cardinality
// stays sane (full status would explode the series count on misbehaving
// upstreams).
func StatusClass(status int) string {
	switch {
	case status >= 500:
		return "5xx"
	case status >= 400:
		return "4xx"
	case status >= 300:
		return "3xx"
	case status >= 200:
		return "2xx"
	default:
		return "unknown"
	}
}

// ObserveDuration records a duration in seconds.
func ObserveDuration(h *prometheus.HistogramVec, model, provider, stream string, d time.Duration) {
	h.WithLabelValues(model, provider, stream).Observe(d.Seconds())
}
