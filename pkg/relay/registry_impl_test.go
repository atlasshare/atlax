package relay

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/atlasshare/atlax/pkg/protocol"
)

func testRegistry() *MemoryRegistry {
	return NewMemoryRegistry(slog.Default())
}

// testConnectionPair creates a LiveConnection and returns both the
// connection and the agent-side MuxSession for verification.
func testConnectionPair(customerID string) (*LiveConnection, *protocol.MuxSession) {
	c1, c2 := net.Pipe()
	cfg := protocol.MuxConfig{
		MaxConcurrentStreams: 16,
		InitialStreamWindow:  262144,
		ConnectionWindow:     1048576,
		PingInterval:         30 * time.Second,
		PingTimeout:          5 * time.Second,
		IdleTimeout:          60 * time.Second,
	}
	relayMux := protocol.NewMuxSession(c1, protocol.RoleRelay, cfg)
	agentMux := protocol.NewMuxSession(c2, protocol.RoleAgent, cfg)
	live := NewLiveConnection(customerID, relayMux, c1.RemoteAddr())
	return live, agentMux
}

func TestMemoryRegistry_RegisterAndLookup(t *testing.T) {
	reg := testRegistry()
	conn, agentMux := testConnectionPair("customer-001")
	defer conn.Close()
	defer agentMux.Close()

	ctx := context.Background()
	require.NoError(t, reg.Register(ctx, "customer-001", conn))

	found, err := reg.Lookup(ctx, "customer-001")
	require.NoError(t, err)
	assert.Equal(t, "customer-001", found.CustomerID())
}

func TestMemoryRegistry_LookupNotFound(t *testing.T) {
	reg := testRegistry()
	_, err := reg.Lookup(context.Background(), "nonexistent")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAgentNotFound)
}

func TestMemoryRegistry_Unregister(t *testing.T) {
	reg := testRegistry()
	conn, agentMux := testConnectionPair("customer-001")
	defer agentMux.Close()

	ctx := context.Background()
	require.NoError(t, reg.Register(ctx, "customer-001", conn))
	require.NoError(t, reg.Unregister(ctx, "customer-001"))

	_, err := reg.Lookup(ctx, "customer-001")
	assert.ErrorIs(t, err, ErrAgentNotFound)
}

func TestMemoryRegistry_RegisterReplacesExisting(t *testing.T) {
	reg := testRegistry()
	conn1, agentMux1 := testConnectionPair("customer-001")
	conn2, agentMux2 := testConnectionPair("customer-001")
	defer agentMux1.Close()
	defer agentMux2.Close()

	ctx := context.Background()
	require.NoError(t, reg.Register(ctx, "customer-001", conn1))
	require.NoError(t, reg.Register(ctx, "customer-001", conn2))

	// Should find conn2, not conn1
	found, err := reg.Lookup(ctx, "customer-001")
	require.NoError(t, err)
	assert.Equal(t, conn2.ConnectedAt(), found.ConnectedAt())
}

func TestMemoryRegistry_Heartbeat(t *testing.T) {
	reg := testRegistry()
	conn, agentMux := testConnectionPair("customer-001")
	defer conn.Close()
	defer agentMux.Close()

	ctx := context.Background()
	require.NoError(t, reg.Register(ctx, "customer-001", conn))

	before := conn.LastSeen()
	time.Sleep(10 * time.Millisecond)
	require.NoError(t, reg.Heartbeat(ctx, "customer-001"))
	after := conn.LastSeen()
	assert.True(t, after.After(before))
}

func TestMemoryRegistry_HeartbeatNotFound(t *testing.T) {
	reg := testRegistry()
	err := reg.Heartbeat(context.Background(), "nonexistent")
	assert.ErrorIs(t, err, ErrAgentNotFound)
}

func TestMemoryRegistry_ListConnectedAgents(t *testing.T) {
	reg := testRegistry()
	conn1, agentMux1 := testConnectionPair("customer-001")
	conn2, agentMux2 := testConnectionPair("customer-002")
	defer conn1.Close()
	defer conn2.Close()
	defer agentMux1.Close()
	defer agentMux2.Close()

	ctx := context.Background()
	require.NoError(t, reg.Register(ctx, "customer-001", conn1))
	require.NoError(t, reg.Register(ctx, "customer-002", conn2))

	agents, err := reg.ListConnectedAgents(ctx)
	require.NoError(t, err)
	assert.Len(t, agents, 2)
}

func TestMemoryRegistry_ConcurrentAccess(t *testing.T) {
	reg := testRegistry()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			cid := fmt.Sprintf("customer-%03d", id)
			conn, agentMux := testConnectionPair(cid)
			defer agentMux.Close()

			reg.Register(ctx, cid, conn) //nolint:errcheck // race test
			reg.Lookup(ctx, cid)         //nolint:errcheck // race test
			reg.Heartbeat(ctx, cid)      //nolint:errcheck // race test
			reg.ListConnectedAgents(ctx) //nolint:errcheck // race test
			reg.Unregister(ctx, cid)     //nolint:errcheck // race test
		}(i)
	}
	wg.Wait()
}

func TestMemoryRegistry_SetMetrics(t *testing.T) {
	reg := testRegistry()
	m := NewMetrics("test", prometheus.NewRegistry())
	reg.SetMetrics(m)
	assert.NotNil(t, reg.metrics)
}

func TestMemoryRegistry_ReplaceDecrementsConnectionGauge(t *testing.T) {
	promReg := prometheus.NewRegistry()
	m := NewMetrics("test_replace", promReg)

	reg := testRegistry()
	reg.SetMetrics(m)

	conn1, agentMux1 := testConnectionPair("customer-001")
	conn2, agentMux2 := testConnectionPair("customer-001")
	conn3, agentMux3 := testConnectionPair("customer-001")
	defer agentMux1.Close()
	defer agentMux2.Close()
	defer agentMux3.Close()

	ctx := context.Background()

	// Register first connection: gauge = 1
	require.NoError(t, reg.Register(ctx, "customer-001", conn1))

	// Replace with second: gauge should still be 1 (not 2)
	require.NoError(t, reg.Register(ctx, "customer-001", conn2))

	// Replace with third: gauge should still be 1 (not 3)
	require.NoError(t, reg.Register(ctx, "customer-001", conn3))

	// Collect the gauge value
	metrics, err := promReg.Gather()
	require.NoError(t, err)

	var gaugeValue float64
	for _, mf := range metrics {
		if mf.GetName() == "test_replace_connections_active" {
			gaugeValue = mf.GetMetric()[0].GetGauge().GetValue()
		}
	}
	assert.Equal(t, float64(1), gaugeValue,
		"connections_active gauge should be 1 after two replacements, not %v", gaugeValue)
}

func TestMemoryRegistry_SetCustomerLimit(t *testing.T) {
	reg := testRegistry()
	reg.SetCustomerLimit("customer-001", 5)

	reg.mu.RLock()
	limit := reg.customerLimits["customer-001"]
	reg.mu.RUnlock()
	assert.Equal(t, 5, limit)
}
