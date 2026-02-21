// Package metrics provides Prometheus metrics for the platform agent.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds all Prometheus metrics for the agent.
type Metrics struct {
	RequestsTotal      *prometheus.CounterVec
	RequestDuration    *prometheus.HistogramVec
	ApprovalsTotal     *prometheus.CounterVec
	GitHubTokensActive prometheus.Gauge
	ErrorsTotal        *prometheus.CounterVec

	registry *prometheus.Registry
}

// New creates and registers all metrics.
func New() *Metrics {
	reg := prometheus.NewRegistry()

	m := &Metrics{
		RequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "agent_requests_total",
				Help: "Total number of agent requests by intent and status.",
			},
			[]string{"intent", "status"},
		),
		RequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "agent_request_duration_seconds",
				Help:    "Request processing duration by intent.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"intent"},
		),
		ApprovalsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "agent_approvals_total",
				Help: "Total number of approval decisions by action and result.",
			},
			[]string{"action", "result"},
		),
		GitHubTokensActive: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "agent_github_tokens_active",
				Help: "Number of active GitHub installation tokens.",
			},
		),
		ErrorsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "agent_errors_total",
				Help: "Total errors by module and type.",
			},
			[]string{"module", "type"},
		),
		registry: reg,
	}

	reg.MustRegister(m.RequestsTotal)
	reg.MustRegister(m.RequestDuration)
	reg.MustRegister(m.ApprovalsTotal)
	reg.MustRegister(m.GitHubTokensActive)
	reg.MustRegister(m.ErrorsTotal)

	return m
}

// Handler returns an http.Handler for the /metrics endpoint.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// RecordRequest increments the request counter.
func (m *Metrics) RecordRequest(intent, status string) {
	m.RequestsTotal.WithLabelValues(intent, status).Inc()
}

// RecordError increments the error counter.
func (m *Metrics) RecordError(module, errType string) {
	m.ErrorsTotal.WithLabelValues(module, errType).Inc()
}

// RecordApproval increments the approval counter.
func (m *Metrics) RecordApproval(action, result string) {
	m.ApprovalsTotal.WithLabelValues(action, result).Inc()
}

// ObserveDuration records request duration.
func (m *Metrics) ObserveDuration(intent string, seconds float64) {
	m.RequestDuration.WithLabelValues(intent).Observe(seconds)
}

// SetGitHubTokens sets the active token count.
func (m *Metrics) SetGitHubTokens(count float64) {
	m.GitHubTokensActive.Set(count)
}
