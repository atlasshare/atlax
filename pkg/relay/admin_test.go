package relay

import (
	"bytes"
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

func testAdminServer(t *testing.T) (addr string, reg *MemoryRegistry, router *PortRouter) {
	t.Helper()
	reg = NewMemoryRegistry(slog.Default())
	router = NewPortRouter(reg, slog.Default())

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr = ln.Addr().String()
	ln.Close()

	ctx, cancelFn := context.WithCancel(context.Background())

	admin := NewAdminServer(AdminConfig{
		Addr:           addr,
		Registry:       reg,
		Router:         router,
		ClientListener: NewClientListener(ClientListenerConfig{Router: router, Logger: slog.Default()}),
		Logger:         slog.Default(),
	})

	go func() {
		admin.Start(ctx) //nolint:errcheck // stopped via ctx cancel
	}()
	time.Sleep(100 * time.Millisecond)

	t.Cleanup(cancelFn)
	return addr, reg, router
}

func TestAdmin_HealthCheck(t *testing.T) {
	addr, reg, _ := testAdminServer(t)

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
	addr, _, _ := testAdminServer(t)

	resp, err := http.Get("http://" + addr + "/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestAdmin_Stats(t *testing.T) {
	addr, _, _ := testAdminServer(t)

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
	addr, _, _ := testAdminServer(t)

	resp, err := http.Get("http://" + addr + "/ports")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var ports []PortResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&ports))
	assert.Empty(t, ports)
}

func TestAdmin_CreateAndListPort(t *testing.T) {
	addr, _, _ := testAdminServer(t)

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
	addr, _, _ := testAdminServer(t)

	resp, err := http.Post("http://"+addr+"/ports", "application/json", bytes.NewBufferString("{bad"))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestAdmin_CreatePort_MissingFields(t *testing.T) {
	addr, _, _ := testAdminServer(t)

	resp, err := http.Post("http://"+addr+"/ports", "application/json", bytes.NewBufferString(`{"port":8080}`))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestAdmin_DeletePort(t *testing.T) {
	addr, _, router := testAdminServer(t)

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
	addr, _, _ := testAdminServer(t)

	req, err := http.NewRequest(http.MethodDelete, "http://"+addr+"/ports/99999", http.NoBody)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestAdmin_ListAgents(t *testing.T) {
	addr, reg, _ := testAdminServer(t)

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
	addr, reg, _ := testAdminServer(t)

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
