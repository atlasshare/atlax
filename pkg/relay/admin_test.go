package relay

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminServer_HealthCheck(t *testing.T) {
	reg := NewMemoryRegistry(slog.Default())

	// Register one agent
	conn, agentMux := testConnectionPair("customer-001")
	defer conn.Close()
	defer agentMux.Close()
	require.NoError(t, reg.Register(context.Background(), "customer-001", conn))

	// Find a free port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	ln.Close()

	admin := NewAdminServer(addr, reg, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		admin.Start(ctx) //nolint:errcheck // stopped via ctx cancel
	}()
	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://" + addr + "/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var health HealthResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&health))
	assert.Equal(t, "ok", health.Status)
	assert.Equal(t, 1, health.Agents)
}

func TestAdminServer_MetricsEndpoint(t *testing.T) {
	reg := NewMemoryRegistry(slog.Default())

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	ln.Close()

	admin := NewAdminServer(addr, reg, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		admin.Start(ctx) //nolint:errcheck // stopped via ctx cancel
	}()
	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://" + addr + "/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
