package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testAdminServer(t *testing.T) (addr string, reg *MemoryRegistry, router *PortRouter, cl *ClientListener) {
	t.Helper()
	reg = NewMemoryRegistry(slog.Default())
	router = NewPortRouter(reg, slog.Default())
	cl = NewClientListener(ClientListenerConfig{Router: router, Logger: slog.Default()})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr = ln.Addr().String()
	ln.Close()

	ctx, cancelFn := context.WithCancel(context.Background())

	admin := NewAdminServer(AdminConfig{
		Addr:           addr,
		Registry:       reg,
		Router:         router,
		ClientListener: cl,
		Logger:         slog.Default(),
	})

	go func() {
		admin.Start(ctx) //nolint:errcheck // stopped via ctx cancel
	}()
	time.Sleep(100 * time.Millisecond)

	t.Cleanup(cancelFn)
	return addr, reg, router, cl
}

func TestAdmin_HealthCheck(t *testing.T) {
	addr, reg, _, _ := testAdminServer(t)

	conn, agentMux := testConnectionPair("customer-001")
	defer conn.Close()
	defer agentMux.Close()
	require.NoError(t, reg.Register(context.Background(), "customer-001", conn))

	resp, err := http.Get("http://" + addr + "/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var health HealthResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&health))
	assert.Equal(t, "ok", health.Status)
	assert.Equal(t, 1, health.Agents)
}

func TestAdmin_Metrics(t *testing.T) {
	addr, _, _, _ := testAdminServer(t)

	resp, err := http.Get("http://" + addr + "/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestAdmin_Stats(t *testing.T) {
	addr, _, _, _ := testAdminServer(t)

	resp, err := http.Get("http://" + addr + "/stats")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var stats StatsResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&stats))
	assert.Equal(t, "ok", stats.Status)
	assert.Greater(t, stats.UptimeSeconds, 0.0)
}

func TestAdmin_ListPorts_Empty(t *testing.T) {
	addr, _, _, _ := testAdminServer(t)

	resp, err := http.Get("http://" + addr + "/ports")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var ports []PortResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&ports))
	assert.Empty(t, ports)
}

func TestAdmin_CreateAndListPort(t *testing.T) {
	addr, _, _, _ := testAdminServer(t)

	// Create
	body := `{"port":18080,"customer_id":"customer-001","service":"http"}`
	resp, err := http.Post("http://"+addr+"/ports", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var created PortResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	assert.Equal(t, 18080, created.Port)
	assert.Equal(t, "customer-001", created.CustomerID)

	// List
	resp2, err := http.Get("http://" + addr + "/ports")
	require.NoError(t, err)
	defer resp2.Body.Close()

	var ports []PortResponse
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&ports))
	assert.Len(t, ports, 1)
	assert.Equal(t, "http", ports[0].Service)
}

func TestAdmin_CreatePort_InvalidJSON(t *testing.T) {
	addr, _, _, _ := testAdminServer(t)

	resp, err := http.Post("http://"+addr+"/ports", "application/json", bytes.NewBufferString("{bad"))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestAdmin_CreatePort_MissingFields(t *testing.T) {
	addr, _, _, _ := testAdminServer(t)

	resp, err := http.Post("http://"+addr+"/ports", "application/json", bytes.NewBufferString(`{"port":8080}`))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestAdmin_DeletePort(t *testing.T) {
	addr, _, router, _ := testAdminServer(t)

	router.AddPortMapping("customer-001", 18080, "http", 0) //nolint:errcheck // test setup

	req, err := http.NewRequest(http.MethodDelete, "http://"+addr+"/ports/18080", http.NoBody)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Verify deleted
	_, _, ok := router.LookupPort(18080)
	assert.False(t, ok)
}

func TestAdmin_DeletePort_NotFound(t *testing.T) {
	addr, _, _, _ := testAdminServer(t)

	req, err := http.NewRequest(http.MethodDelete, "http://"+addr+"/ports/99999", http.NoBody)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestAdmin_ListAgents(t *testing.T) {
	addr, reg, _, _ := testAdminServer(t)

	conn, agentMux := testConnectionPair("customer-001")
	defer conn.Close()
	defer agentMux.Close()
	require.NoError(t, reg.Register(context.Background(), "customer-001", conn))

	resp, err := http.Get("http://" + addr + "/agents")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var agents []AgentResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&agents))
	assert.Len(t, agents, 1)
	assert.Equal(t, "customer-001", agents[0].CustomerID)
}

func TestAdmin_DeleteAgent(t *testing.T) {
	addr, reg, _, _ := testAdminServer(t)

	conn, agentMux := testConnectionPair("customer-001")
	defer agentMux.Close()
	require.NoError(t, reg.Register(context.Background(), "customer-001", conn))

	req, err := http.NewRequest(http.MethodDelete, "http://"+addr+"/agents/customer-001", http.NoBody)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Verify disconnected
	_, lookupErr := reg.Lookup(context.Background(), "customer-001")
	assert.ErrorIs(t, lookupErr, ErrAgentNotFound)
}

// --- Port lifecycle tests (Step 5a) ---

func TestAdmin_CreatePort_StartsListener(t *testing.T) {
	addr, _, _, cl := testAdminServer(t)

	body := `{"port":19080,"customer_id":"customer-001","service":"http","listen_addr":"127.0.0.1"}`
	resp, err := http.Post("http://"+addr+"/ports", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	// Verify a TCP listener was actually started.
	listenerAddr := cl.Addr(19080)
	require.NotNil(t, listenerAddr, "listener should be running on port 19080")

	// Verify we can TCP connect to it.
	conn, dialErr := net.DialTimeout("tcp", listenerAddr.String(), 2*time.Second)
	require.NoError(t, dialErr)
	conn.Close()
}

func TestAdmin_DeletePort_StopsListener(t *testing.T) {
	addr, _, _, cl := testAdminServer(t)

	// Create a port with listener.
	body := `{"port":19081,"customer_id":"customer-001","service":"http","listen_addr":"127.0.0.1"}`
	resp, err := http.Post("http://"+addr+"/ports", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.NotNil(t, cl.Addr(19081))

	// Delete it.
	req, err := http.NewRequest(http.MethodDelete, "http://"+addr+"/ports/19081", http.NoBody)
	require.NoError(t, err)
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp2.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp2.StatusCode)

	// Verify listener is gone.
	assert.Nil(t, cl.Addr(19081), "listener should be stopped after DELETE")
}

func TestAdmin_CreatePort_DefaultListenAddr(t *testing.T) {
	addr, _, _, cl := testAdminServer(t)

	// No listen_addr field -- should default to 0.0.0.0.
	body := `{"port":19082,"customer_id":"customer-001","service":"http"}`
	resp, err := http.Post("http://"+addr+"/ports", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	require.NotNil(t, cl.Addr(19082), "listener should start with default listen addr")
}

func TestAdmin_CreatePort_DuplicateReturnsConflict(t *testing.T) {
	addr, _, _, cl := testAdminServer(t)

	body := `{"port":19083,"customer_id":"customer-001","service":"http","listen_addr":"127.0.0.1"}`
	resp, err := http.Post("http://"+addr+"/ports", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.NotNil(t, cl.Addr(19083))

	// Second POST with same port should fail (address already in use).
	resp2, err := http.Post("http://"+addr+"/ports", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusConflict, resp2.StatusCode)
}

// --- Admin error path coverage tests (Step 5b) ---

func TestAdmin_Stats_MethodNotAllowed(t *testing.T) {
	addr, _, _, _ := testAdminServer(t)

	resp, err := http.Post("http://"+addr+"/stats", "application/json", http.NoBody)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

func TestAdmin_Ports_MethodNotAllowed(t *testing.T) {
	addr, _, _, _ := testAdminServer(t)

	req, err := http.NewRequest(http.MethodPut, "http://"+addr+"/ports", http.NoBody)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

func TestAdmin_Agents_MethodNotAllowed(t *testing.T) {
	addr, _, _, _ := testAdminServer(t)

	resp, err := http.Post("http://"+addr+"/agents", "application/json", http.NoBody)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

func TestAdmin_AgentByID_MethodNotAllowed(t *testing.T) {
	addr, _, _, _ := testAdminServer(t)

	resp, err := http.Get("http://" + addr + "/agents/customer-001")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

func TestAdmin_AgentByID_EmptyID(t *testing.T) {
	addr, _, _, _ := testAdminServer(t)

	req, err := http.NewRequest(http.MethodDelete, "http://"+addr+"/agents/", http.NoBody)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestAdmin_PortByID_InvalidPort(t *testing.T) {
	addr, _, _, _ := testAdminServer(t)

	req, err := http.NewRequest(http.MethodDelete, "http://"+addr+"/ports/abc", http.NoBody)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestAdmin_PortByID_MethodNotAllowed(t *testing.T) {
	addr, _, _, _ := testAdminServer(t)

	resp, err := http.Get("http://" + addr + "/ports/12345")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

func TestAdmin_StartUnixSocket(t *testing.T) {
	reg := NewMemoryRegistry(slog.Default())
	router := NewPortRouter(reg, slog.Default())
	cl := NewClientListener(ClientListenerConfig{Router: router, Logger: slog.Default()})

	// Use /tmp to avoid macOS 104-char unix socket path limit.
	socketPath := fmt.Sprintf("/tmp/atlax-test-%d.sock", time.Now().UnixNano())
	t.Cleanup(func() { os.Remove(socketPath) })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	admin := NewAdminServer(AdminConfig{
		SocketPath:     socketPath,
		Registry:       reg,
		Router:         router,
		ClientListener: cl,
		Logger:         slog.Default(),
	})

	go func() {
		admin.Start(ctx) //nolint:errcheck // stopped via ctx cancel
	}()
	time.Sleep(150 * time.Millisecond)

	// HTTP request over unix socket.
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", socketPath)
			},
		},
	}

	resp, err := client.Get("http://unix/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestAdmin_StartBothTCPAndUnix(t *testing.T) {
	reg := NewMemoryRegistry(slog.Default())
	router := NewPortRouter(reg, slog.Default())
	cl := NewClientListener(ClientListenerConfig{Router: router, Logger: slog.Default()})

	socketPath := fmt.Sprintf("/tmp/atlax-test-%d.sock", time.Now().UnixNano())
	t.Cleanup(func() { os.Remove(socketPath) })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	tcpAddr := ln.Addr().String()
	ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	admin := NewAdminServer(AdminConfig{
		Addr:           tcpAddr,
		SocketPath:     socketPath,
		Registry:       reg,
		Router:         router,
		ClientListener: cl,
		Logger:         slog.Default(),
	})

	go func() {
		admin.Start(ctx) //nolint:errcheck // stopped via ctx cancel
	}()
	time.Sleep(150 * time.Millisecond)

	// TCP works.
	resp1, err := http.Get("http://" + tcpAddr + "/healthz")
	require.NoError(t, err)
	defer resp1.Body.Close()
	assert.Equal(t, http.StatusOK, resp1.StatusCode)

	// Unix socket works.
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", socketPath)
			},
		},
	}
	resp2, err := client.Get("http://unix/healthz")
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode)
}

// --- Socket failure resilience tests (#87) ---

func TestAdmin_SocketFailureFallsBackToTCP(t *testing.T) {
	reg := NewMemoryRegistry(slog.Default())
	router := NewPortRouter(reg, slog.Default())
	cl := NewClientListener(ClientListenerConfig{Router: router, Logger: slog.Default()})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	tcpAddr := ln.Addr().String()
	ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// Unwritable socket path -- should warn and fall back to TCP.
	admin := NewAdminServer(AdminConfig{
		Addr:           tcpAddr,
		SocketPath:     "/nonexistent/dir/atlax.sock",
		Registry:       reg,
		Router:         router,
		ClientListener: cl,
		Logger:         slog.Default(),
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- admin.Start(ctx)
	}()
	time.Sleep(150 * time.Millisecond)

	// TCP listener should be reachable despite socket failure.
	resp, err := http.Get("http://" + tcpAddr + "/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Start should not have returned an error.
	select {
	case startErr := <-errCh:
		t.Fatalf("Start() returned unexpectedly: %v", startErr)
	default:
		// Still running -- correct.
	}
}

func TestAdmin_SocketFailureOnlySocket(t *testing.T) {
	reg := NewMemoryRegistry(slog.Default())
	router := NewPortRouter(reg, slog.Default())
	cl := NewClientListener(ClientListenerConfig{Router: router, Logger: slog.Default()})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// Socket-only mode (no Addr) with unwritable path -- should fail.
	admin := NewAdminServer(AdminConfig{
		SocketPath:     "/nonexistent/dir/atlax.sock",
		Registry:       reg,
		Router:         router,
		ClientListener: cl,
		Logger:         slog.Default(),
	})

	err := admin.Start(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unix socket")
}

func TestAdmin_EmptySocketPath(t *testing.T) {
	reg := NewMemoryRegistry(slog.Default())
	router := NewPortRouter(reg, slog.Default())
	cl := NewClientListener(ClientListenerConfig{Router: router, Logger: slog.Default()})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	tcpAddr := ln.Addr().String()
	ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// Empty socket path -- TCP only, no socket attempted.
	admin := NewAdminServer(AdminConfig{
		Addr:           tcpAddr,
		SocketPath:     "",
		Registry:       reg,
		Router:         router,
		ClientListener: cl,
		Logger:         slog.Default(),
	})

	go func() {
		admin.Start(ctx) //nolint:errcheck // stopped via ctx cancel
	}()
	time.Sleep(150 * time.Millisecond)

	resp, err := http.Get("http://" + tcpAddr + "/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
