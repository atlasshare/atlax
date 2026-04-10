package relay

import (
	"context"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testClientListener(t *testing.T) *ClientListener {
	t.Helper()
	reg := NewMemoryRegistry(slog.Default())
	router := NewPortRouter(reg, slog.Default())
	cl := NewClientListener(ClientListenerConfig{Router: router, Logger: slog.Default()})
	return cl
}

func TestClientListener_StartPort_ListensOnAddr(t *testing.T) {
	cl := testClientListener(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		cl.StartPort(ctx, "127.0.0.1:0", 19090) //nolint:errcheck // stopped via ctx cancel
	}()
	time.Sleep(50 * time.Millisecond)

	addr := cl.Addr(19090)
	require.NotNil(t, addr, "listener should be running")

	conn, err := net.DialTimeout("tcp", addr.String(), 2*time.Second)
	require.NoError(t, err)
	conn.Close()
}

func TestClientListener_StopPort_ClosesListener(t *testing.T) {
	cl := testClientListener(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		cl.StartPort(ctx, "127.0.0.1:0", 19091) //nolint:errcheck // stopped via ctx cancel
	}()
	time.Sleep(50 * time.Millisecond)

	addr := cl.Addr(19091)
	require.NotNil(t, addr)

	err := cl.StopPort(19091)
	require.NoError(t, err)

	assert.Nil(t, cl.Addr(19091), "addr should be nil after StopPort")

	// Connection should be refused.
	_, dialErr := net.DialTimeout("tcp", addr.String(), 500*time.Millisecond)
	assert.Error(t, dialErr, "dial should fail after listener stopped")
}

func TestClientListener_StopPort_NotFound(t *testing.T) {
	cl := testClientListener(t)

	err := cl.StopPort(99999)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestClientListener_Stop_ClosesAll(t *testing.T) {
	cl := testClientListener(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start two ports.
	go func() {
		cl.StartPort(ctx, "127.0.0.1:0", 19092) //nolint:errcheck // stopped via ctx cancel
	}()
	go func() {
		cl.StartPort(ctx, "127.0.0.1:0", 19093) //nolint:errcheck // stopped via ctx cancel
	}()
	time.Sleep(50 * time.Millisecond)

	require.NotNil(t, cl.Addr(19092))
	require.NotNil(t, cl.Addr(19093))

	cl.Stop()

	assert.Nil(t, cl.Addr(19092), "port 19092 should be stopped")
	assert.Nil(t, cl.Addr(19093), "port 19093 should be stopped")
}

func TestClientListener_Addr_UnstartedPortReturnsNil(t *testing.T) {
	cl := testClientListener(t)
	assert.Nil(t, cl.Addr(55555))
}

// --- handleClient coverage tests ---

func TestClientListener_HandleClient_NoMapping(t *testing.T) {
	cl := testClientListener(t)

	// handleClient with a port that has no routing entry.
	server, client := net.Pipe()
	defer server.Close()

	cl.handleClient(context.Background(), client, 55555)

	// Client connection should be closed by handleClient.
	buf := make([]byte, 1)
	_, err := client.Read(buf)
	assert.Error(t, err, "connection should be closed after no-mapping rejection")
}

func TestClientListener_HandleClient_RateLimited(t *testing.T) {
	reg := NewMemoryRegistry(slog.Default())
	router := NewPortRouter(reg, slog.Default())
	cl := NewClientListener(ClientListenerConfig{Router: router, Logger: slog.Default()})

	// Add a port mapping so lookup succeeds.
	require.NoError(t, router.AddPortMapping("customer-001", 19094, "http", "", 0))

	// Set a very restrictive rate limit: 1 req/s, burst 1.
	cl.SetRateLimiter("customer-001", 1, 1)

	// First connection consumes the burst.
	c1s, c1c := net.Pipe()
	defer c1s.Close()
	cl.handleClient(context.Background(), c1c, 19094)

	// Second connection should be rate-limited.
	c2s, c2c := net.Pipe()
	defer c2s.Close()
	cl.handleClient(context.Background(), c2c, 19094)

	// The rate-limited connection should be closed.
	buf := make([]byte, 1)
	_, err := c2c.Read(buf)
	assert.Error(t, err, "connection should be closed after rate limit rejection")
}

func TestClientListener_HandleClient_RouteFails(t *testing.T) {
	reg := NewMemoryRegistry(slog.Default())
	router := NewPortRouter(reg, slog.Default())
	cl := NewClientListener(ClientListenerConfig{Router: router, Logger: slog.Default()})

	// Port mapped but no agent registered -- Route will fail.
	require.NoError(t, router.AddPortMapping("customer-001", 19095, "http", "", 0))

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	// This should not panic; Route fails with "agent not found".
	cl.handleClient(context.Background(), client, 19095)
}

func TestClientListener_SetRateLimiter(t *testing.T) {
	cl := testClientListener(t)

	// Zero RPS is a no-op.
	cl.SetRateLimiter("customer-001", 0, 10)
	cl.mu.Lock()
	_, exists := cl.rateLimiters["customer-001"]
	cl.mu.Unlock()
	assert.False(t, exists, "zero rps should not create a limiter")

	// Negative RPS is a no-op.
	cl.SetRateLimiter("customer-002", -1, 10)
	cl.mu.Lock()
	_, exists = cl.rateLimiters["customer-002"]
	cl.mu.Unlock()
	assert.False(t, exists, "negative rps should not create a limiter")

	// Positive RPS creates a limiter.
	cl.SetRateLimiter("customer-003", 100, 50)
	cl.mu.Lock()
	_, exists = cl.rateLimiters["customer-003"]
	cl.mu.Unlock()
	assert.True(t, exists, "positive rps should create a limiter")
}
