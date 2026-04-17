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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/atlasshare/atlax/pkg/audit"
)

// captureEmitter is a test-local audit.Emitter that records all emitted events.
type captureEmitter struct {
	mu     sync.Mutex
	events []audit.Event
}

func (c *captureEmitter) Emit(_ context.Context, event audit.Event) error { //nolint:gocritic // hugeParam: signature must match audit.Emitter interface
	c.mu.Lock()
	c.events = append(c.events, event)
	c.mu.Unlock()
	return nil
}

func (c *captureEmitter) Close() error { return nil }

func (c *captureEmitter) snapshot() []audit.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]audit.Event, len(c.events))
	copy(out, c.events)
	return out
}

// testAdminServerWithEmitter returns an admin server wired to a captureEmitter.
func testAdminServerWithEmitter(t *testing.T) (addr string, reg *MemoryRegistry, router *PortRouter, cl *ClientListener, emitter *captureEmitter) {
	t.Helper()
	reg = NewMemoryRegistry(slog.Default())
	router = NewPortRouter(reg, slog.Default())
	cl = NewClientListener(ClientListenerConfig{Router: router, Logger: slog.Default()})
	emitter = &captureEmitter{}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr = ln.Addr().String()
	ln.Close()

	ctx, cancelFn := context.WithCancel(context.Background())

	admin := NewAdminServer(&AdminConfig{
		Addr:           addr,
		Registry:       reg,
		Router:         router,
		ClientListener: cl,
		Logger:         slog.Default(),
		Emitter:        emitter,
	})

	go func() {
		admin.Start(ctx) //nolint:errcheck // stopped via ctx cancel
	}()
	time.Sleep(100 * time.Millisecond)

	t.Cleanup(cancelFn)
	return addr, reg, router, cl, emitter
}

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

	admin := NewAdminServer(&AdminConfig{
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

	router.AddPortMapping("customer-001", 18080, "http", "", 0) //nolint:errcheck // test setup

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

	// GET and DELETE are now both valid. PUT is not.
	req, err := http.NewRequest(http.MethodPut, "http://"+addr+"/agents/customer-001", http.NoBody)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
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

func TestAdmin_ReadyCheck(t *testing.T) {
	addr, _, _, _ := testAdminServer(t)

	resp, err := http.Get("http://" + addr + "/readyz")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "ready", body["status"])
}

func TestAdmin_GetPort_Found(t *testing.T) {
	addr, _, router, _ := testAdminServer(t)
	require.NoError(t, router.AddPortMapping("customer-001", 19200, "http", "127.0.0.1", 50))

	resp, err := http.Get("http://" + addr + "/ports/19200")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var got PortResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, 19200, got.Port)
	assert.Equal(t, "customer-001", got.CustomerID)
	assert.Equal(t, "http", got.Service)
	assert.Equal(t, "127.0.0.1", got.ListenAddr)
	assert.Equal(t, 50, got.MaxStreams)
}

func TestAdmin_GetPort_NotFound(t *testing.T) {
	addr, _, _, _ := testAdminServer(t)

	resp, err := http.Get("http://" + addr + "/ports/65000")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestAdmin_ListPorts_IncludesListenAddrAndMaxStreams(t *testing.T) {
	addr, _, router, _ := testAdminServer(t)
	require.NoError(t, router.AddPortMapping("customer-001", 19201, "http", "127.0.0.1", 42))

	resp, err := http.Get("http://" + addr + "/ports")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var ports []PortResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&ports))
	require.Len(t, ports, 1)
	assert.Equal(t, "127.0.0.1", ports[0].ListenAddr)
	assert.Equal(t, 42, ports[0].MaxStreams)
}

func TestAdmin_GetAgent_Found(t *testing.T) {
	addr, reg, _, _ := testAdminServer(t)

	conn, agentMux := testConnectionPair("customer-001")
	defer conn.Close()
	defer agentMux.Close()
	require.NoError(t, reg.Register(context.Background(), "customer-001", conn))

	resp, err := http.Get("http://" + addr + "/agents/customer-001")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var got AgentResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, "customer-001", got.CustomerID)
}

func TestAdmin_GetAgent_NotFound(t *testing.T) {
	addr, _, _, _ := testAdminServer(t)

	resp, err := http.Get("http://" + addr + "/agents/customer-unknown")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestAdmin_PortByID_MethodNotAllowed(t *testing.T) {
	addr, _, _, _ := testAdminServer(t)

	// GET, PUT, and DELETE are valid. PATCH is not.
	req, err := http.NewRequest(http.MethodPatch, "http://"+addr+"/ports/12345", http.NoBody)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
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

	admin := NewAdminServer(&AdminConfig{
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

	admin := NewAdminServer(&AdminConfig{
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
	admin := NewAdminServer(&AdminConfig{
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
	admin := NewAdminServer(&AdminConfig{
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
	admin := NewAdminServer(&AdminConfig{
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

// --- Audit emission tests ---

func TestAdmin_AuditEmission_CreatePort(t *testing.T) {
	addr, _, router, cl, emitter := testAdminServerWithEmitter(t)

	// Bind a real listener so StartPort succeeds.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	body, _ := json.Marshal(map[string]any{
		"port":        port,
		"customer_id": "customer-audit-001",
		"service":     "samba",
	})
	resp, err := http.Post("http://"+addr+"/ports", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	// Clean up the started listener.
	cl.StopPort(port)                                    //nolint:errcheck // best-effort cleanup
	router.RemovePortMapping("customer-audit-001", port) //nolint:errcheck // best-effort cleanup

	events := emitter.snapshot()
	require.Len(t, events, 1)
	assert.Equal(t, audit.ActionAdminPortAdded, events[0].Action)
	assert.Equal(t, "admin-api", events[0].Actor)
	assert.Equal(t, fmt.Sprintf("%d", port), events[0].Target)
	assert.Equal(t, "customer-audit-001", events[0].CustomerID)
	assert.Equal(t, "samba", events[0].Metadata["service"])
}

func TestAdmin_AuditEmission_DeletePort(t *testing.T) {
	addr, _, router, cl, emitter := testAdminServerWithEmitter(t)

	// Pre-register a port mapping and a listener.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	require.NoError(t, router.AddPortMapping("customer-audit-002", port, "http", "127.0.0.1", 0))
	ctx := context.Background()
	go cl.StartPort(ctx, fmt.Sprintf("127.0.0.1:%d", port), port) //nolint:errcheck // test listener, error not relevant
	time.Sleep(50 * time.Millisecond)

	req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("http://%s/ports/%d", addr, port), http.NoBody)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	events := emitter.snapshot()
	require.Len(t, events, 1)
	assert.Equal(t, audit.ActionAdminPortRemoved, events[0].Action)
	assert.Equal(t, "admin-api", events[0].Actor)
	assert.Equal(t, fmt.Sprintf("%d", port), events[0].Target)
	assert.Equal(t, "customer-audit-002", events[0].CustomerID)
}

func TestAdmin_AuditEmission_DeleteAgent(t *testing.T) {
	addr, reg, _, _, emitter := testAdminServerWithEmitter(t)

	conn, agentMux := testConnectionPair("customer-audit-003")
	defer conn.Close()
	defer agentMux.Close()
	require.NoError(t, reg.Register(context.Background(), "customer-audit-003", conn))

	req, _ := http.NewRequest(http.MethodDelete, "http://"+addr+"/agents/customer-audit-003", http.NoBody)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	events := emitter.snapshot()
	require.Len(t, events, 1)
	assert.Equal(t, audit.ActionAdminAgentDisconnected, events[0].Action)
	assert.Equal(t, "admin-api", events[0].Actor)
	assert.Equal(t, "customer-audit-003", events[0].Target)
	assert.Equal(t, "customer-audit-003", events[0].CustomerID)
}

// --- UpdatePort tests (Step 3 B3) ---

// adminFixture bundles together the handles a test needs when the admin
// server is wired with both an emitter and a sidecar store.
type adminFixture struct {
	addr     string
	registry *MemoryRegistry
	router   *PortRouter
	listener *ClientListener
	emitter  *captureEmitter
	store    *SidecarStore
}

// newAdminFixture returns an admin server wired to a captureEmitter AND
// a SidecarStore backed by a per-test temp file.
func newAdminFixture(t *testing.T) *adminFixture {
	t.Helper()
	reg := NewMemoryRegistry(slog.Default())
	router := NewPortRouter(reg, slog.Default())
	cl := NewClientListener(ClientListenerConfig{Router: router, Logger: slog.Default()})
	emitter := &captureEmitter{}

	sidecarPath := fmt.Sprintf("%s/atlax-sidecar-%d.json", t.TempDir(), time.Now().UnixNano())
	store := NewSidecarStore(sidecarPath)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	ln.Close()

	ctx, cancelFn := context.WithCancel(context.Background())

	admin := NewAdminServer(&AdminConfig{
		Addr:           addr,
		Registry:       reg,
		Router:         router,
		ClientListener: cl,
		Logger:         slog.Default(),
		Emitter:        emitter,
		Store:          store,
	})

	go func() {
		admin.Start(ctx) //nolint:errcheck // stopped via ctx cancel
	}()
	time.Sleep(100 * time.Millisecond)

	t.Cleanup(cancelFn)
	return &adminFixture{
		addr:     addr,
		registry: reg,
		router:   router,
		listener: cl,
		emitter:  emitter,
		store:    store,
	}
}

func TestAdmin_UpdatePort_Success(t *testing.T) {
	f := newAdminFixture(t)

	require.NoError(t, f.router.AddPortMapping("customer-001", 18080, "http", "127.0.0.1", 10))

	body := `{"service":"https","listen_addr":"0.0.0.0","max_streams":25}`
	req, err := http.NewRequest(http.MethodPut, "http://"+f.addr+"/ports/18080", bytes.NewBufferString(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var updated PortResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&updated))
	assert.Equal(t, 18080, updated.Port)
	assert.Equal(t, "customer-001", updated.CustomerID, "customer_id must be preserved")
	assert.Equal(t, "https", updated.Service)
	assert.Equal(t, "0.0.0.0", updated.ListenAddr)
	assert.Equal(t, 25, updated.MaxStreams)

	// Router state reflects the update.
	info, ok := f.router.GetPort(18080)
	require.True(t, ok)
	assert.Equal(t, "customer-001", info.CustomerID)
	assert.Equal(t, "https", info.Service)
	assert.Equal(t, 25, info.MaxStreams)

	// Audit event emitted.
	events := f.emitter.snapshot()
	require.Len(t, events, 1)
	assert.Equal(t, audit.ActionAdminPortUpdated, events[0].Action)
	assert.Equal(t, "admin-api", events[0].Actor)
	assert.Equal(t, "18080", events[0].Target)
	assert.Equal(t, "customer-001", events[0].CustomerID)

	// Sidecar persisted the new state.
	data, loadErr := f.store.Load()
	require.NoError(t, loadErr)
	require.Len(t, data.Ports, 1)
	assert.Equal(t, 18080, data.Ports[0].Port)
	assert.Equal(t, "customer-001", data.Ports[0].CustomerID)
	assert.Equal(t, "https", data.Ports[0].Service)
	assert.Equal(t, "0.0.0.0", data.Ports[0].ListenAddr)
	assert.Equal(t, 25, data.Ports[0].MaxStreams)
}

func TestAdmin_UpdatePort_NotFound(t *testing.T) {
	f := newAdminFixture(t)

	body := `{"service":"https"}`
	req, err := http.NewRequest(http.MethodPut, "http://"+f.addr+"/ports/65000", bytes.NewBufferString(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestAdmin_UpdatePort_EmptyBody(t *testing.T) {
	f := newAdminFixture(t)

	require.NoError(t, f.router.AddPortMapping("customer-001", 18081, "http", "127.0.0.1", 10))

	// Empty JSON object => no fields set => 400.
	body := `{}`
	req, err := http.NewRequest(http.MethodPut, "http://"+f.addr+"/ports/18081", bytes.NewBufferString(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	// Original state untouched.
	info, ok := f.router.GetPort(18081)
	require.True(t, ok)
	assert.Equal(t, "http", info.Service)
	assert.Equal(t, "127.0.0.1", info.ListenAddr)
	assert.Equal(t, 10, info.MaxStreams)
}

func TestAdmin_UpdatePort_PartialUpdate(t *testing.T) {
	f := newAdminFixture(t)

	require.NoError(t, f.router.AddPortMapping("customer-001", 18082, "http", "127.0.0.1", 10))

	// Only max_streams supplied. service and listen_addr must be preserved.
	body := `{"max_streams":77}`
	req, err := http.NewRequest(http.MethodPut, "http://"+f.addr+"/ports/18082", bytes.NewBufferString(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	info, ok := f.router.GetPort(18082)
	require.True(t, ok)
	assert.Equal(t, "customer-001", info.CustomerID)
	assert.Equal(t, "http", info.Service, "service must be preserved when omitted from PUT")
	assert.Equal(t, "127.0.0.1", info.ListenAddr, "listen_addr must be preserved when omitted from PUT")
	assert.Equal(t, 77, info.MaxStreams)
}

func TestAdmin_UpdatePort_InvalidJSON(t *testing.T) {
	f := newAdminFixture(t)

	require.NoError(t, f.router.AddPortMapping("customer-001", 18083, "http", "127.0.0.1", 10))

	req, err := http.NewRequest(http.MethodPut, "http://"+f.addr+"/ports/18083", bytes.NewBufferString("{bad"))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestAdmin_UpdatePort_InvalidListenAddr(t *testing.T) {
	f := newAdminFixture(t)

	require.NoError(t, f.router.AddPortMapping("customer-001", 18084, "http", "127.0.0.1", 10))

	body := `{"listen_addr":"not-an-ip"}`
	req, err := http.NewRequest(http.MethodPut, "http://"+f.addr+"/ports/18084", bytes.NewBufferString(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestAdmin_UpdatePort_NegativeMaxStreams(t *testing.T) {
	f := newAdminFixture(t)

	require.NoError(t, f.router.AddPortMapping("customer-001", 18085, "http", "127.0.0.1", 10))

	body := `{"max_streams":-1}`
	req, err := http.NewRequest(http.MethodPut, "http://"+f.addr+"/ports/18085", bytes.NewBufferString(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestAdmin_AuditEmission_NilEmitter_NoPanic(t *testing.T) {
	// AdminServer with no Emitter configured must not panic on mutations.
	reg := NewMemoryRegistry(slog.Default())
	router := NewPortRouter(reg, slog.Default())
	cl := NewClientListener(ClientListenerConfig{Router: router, Logger: slog.Default()})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	adminAddr := ln.Addr().String()
	ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	admin := NewAdminServer(&AdminConfig{
		Addr:           adminAddr,
		Registry:       reg,
		Router:         router,
		ClientListener: cl,
		Logger:         slog.Default(),
		// Emitter intentionally nil
	})
	go admin.Start(ctx) //nolint:errcheck // admin server error is logged internally; not relevant to this test
	time.Sleep(100 * time.Millisecond)

	// Register then delete an agent — should not panic.
	conn, agentMux := testConnectionPair("customer-nil-emitter")
	defer conn.Close()
	defer agentMux.Close()
	require.NoError(t, reg.Register(ctx, "customer-nil-emitter", conn))

	req, _ := http.NewRequest(http.MethodDelete, "http://"+adminAddr+"/agents/customer-nil-emitter", http.NoBody)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}
