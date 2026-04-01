package relay

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds per-customer Prometheus counters and gauges.
type Metrics struct {
	StreamsTotal      *prometheus.CounterVec
	StreamsActive     *prometheus.GaugeVec
	ConnectionsTotal  *prometheus.CounterVec
	ConnectionsActive *prometheus.GaugeVec
	ClientsRejected   *prometheus.CounterVec
}

// NewMetrics registers and returns relay metrics with the given prefix.
func NewMetrics(prefix string, reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		StreamsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: prefix,
				Name:      "streams_total",
				Help:      "Total streams opened per customer.",
			},
			[]string{"customer_id"},
		),
		StreamsActive: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: prefix,
				Name:      "streams_active",
				Help:      "Currently active streams per customer.",
			},
			[]string{"customer_id"},
		),
		ConnectionsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: prefix,
				Name:      "connections_total",
				Help:      "Total agent connections per customer.",
			},
			[]string{"customer_id"},
		),
		ConnectionsActive: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: prefix,
				Name:      "connections_active",
				Help:      "Currently connected agents per customer.",
			},
			[]string{"customer_id"},
		),
		ClientsRejected: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: prefix,
				Name:      "clients_rejected_total",
				Help:      "Client connections rejected per customer and reason.",
			},
			[]string{"customer_id", "reason"},
		),
	}

	reg.MustRegister(
		m.StreamsTotal,
		m.StreamsActive,
		m.ConnectionsTotal,
		m.ConnectionsActive,
		m.ClientsRejected,
	)

	return m
}

// StreamOpened increments stream counters for a customer.
func (m *Metrics) StreamOpened(customerID string) {
	m.StreamsTotal.WithLabelValues(customerID).Inc()
	m.StreamsActive.WithLabelValues(customerID).Inc()
}

// StreamClosed decrements the active stream gauge for a customer.
func (m *Metrics) StreamClosed(customerID string) {
	m.StreamsActive.WithLabelValues(customerID).Dec()
}

// ConnectionRegistered increments connection counters for a customer.
func (m *Metrics) ConnectionRegistered(customerID string) {
	m.ConnectionsTotal.WithLabelValues(customerID).Inc()
	m.ConnectionsActive.WithLabelValues(customerID).Inc()
}

// ConnectionUnregistered decrements active connection gauge.
func (m *Metrics) ConnectionUnregistered(customerID string) {
	m.ConnectionsActive.WithLabelValues(customerID).Dec()
}

// ClientRejected increments the rejection counter with a reason.
func (m *Metrics) ClientRejected(customerID, reason string) {
	m.ClientsRejected.WithLabelValues(customerID, reason).Inc()
}
