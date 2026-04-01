package relay

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dto "github.com/prometheus/client_model/go"
)

func TestMetrics_StreamOpenClose(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics("atlax", reg)

	m.StreamOpened("customer-001")
	m.StreamOpened("customer-001")
	m.StreamClosed("customer-001")

	// Total should be 2, active should be 1
	assert.Equal(t, 2.0, counterValue(t, m.StreamsTotal, "customer-001"))
	assert.Equal(t, 1.0, gaugeValue(t, m.StreamsActive, "customer-001"))
}

func TestMetrics_ConnectionRegisterUnregister(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics("atlax", reg)

	m.ConnectionRegistered("customer-001")
	m.ConnectionUnregistered("customer-001")

	assert.Equal(t, 1.0, counterValue(t, m.ConnectionsTotal, "customer-001"))
	assert.Equal(t, 0.0, gaugeValue(t, m.ConnectionsActive, "customer-001"))
}

func TestMetrics_ClientRejected(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics("atlax", reg)

	m.ClientRejected("customer-001", "rate_limited")
	m.ClientRejected("customer-001", "rate_limited")
	m.ClientRejected("customer-001", "stream_limit")

	assert.Equal(t, 2.0, rejectedValue(t, m.ClientsRejected, "customer-001", "rate_limited"))
	assert.Equal(t, 1.0, rejectedValue(t, m.ClientsRejected, "customer-001", "stream_limit"))
}

func TestMetrics_IndependentCustomers(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics("atlax", reg)

	m.StreamOpened("customer-a")
	m.StreamOpened("customer-b")
	m.StreamOpened("customer-b")

	assert.Equal(t, 1.0, counterValue(t, m.StreamsTotal, "customer-a"))
	assert.Equal(t, 2.0, counterValue(t, m.StreamsTotal, "customer-b"))
}

// helpers to extract metric values

func counterValue(t *testing.T, vec *prometheus.CounterVec, customerID string) float64 {
	t.Helper()
	var m dto.Metric
	require.NoError(t, vec.WithLabelValues(customerID).Write(&m))
	return m.GetCounter().GetValue()
}

func gaugeValue(t *testing.T, vec *prometheus.GaugeVec, customerID string) float64 {
	t.Helper()
	var m dto.Metric
	require.NoError(t, vec.WithLabelValues(customerID).Write(&m))
	return m.GetGauge().GetValue()
}

func rejectedValue(t *testing.T, vec *prometheus.CounterVec, customerID, reason string) float64 {
	t.Helper()
	var m dto.Metric
	require.NoError(t, vec.WithLabelValues(customerID, reason).Write(&m))
	return m.GetCounter().GetValue()
}
