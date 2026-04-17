package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/atlasshare/atlax/pkg/audit"
	"github.com/atlasshare/atlax/pkg/config"
)

// baseReloadYAML is a minimal, valid relay config used as the starting
// point for reload tests. Tests rewrite the file and call POST /reload
// (or the Reload method directly).
const baseReloadYAML = `
server:
  listen_addr: "127.0.0.1:18443"
tls:
  cert_file: /etc/atlax/relay.crt
  key_file: /etc/atlax/relay.key
  client_ca_file: /etc/atlax/customer-ca.crt
customers:
  - id: customer-001
    ports:
      - port: 18080
        service: http
        listen_addr: "127.0.0.1"
    max_streams: 4
    rate_limit:
      requests_per_second: 10
      burst: 20
`

// reloadTestHarness bundles the moving parts a reload test needs: the
// admin server address, the live registry/router/listener, the
// capture emitter for audit assertions, and the path to the live
// config file the operator would edit.
type reloadTestHarness struct {
	addr           string
	registry       *MemoryRegistry
	router         *PortRouter
	clientListener *ClientListener
	emitter        *captureEmitter
	store          *SidecarStore
	admin          *AdminServer
	configPath     string
	logBuf         *lockedBuffer
	initialCfg     *config.RelayConfig
}

// lockedBuffer is a thread-safe bytes.Buffer; slog handlers write from
// spawned goroutines so the raw bytes.Buffer would race.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func newLockedBuffer() *lockedBuffer {
	return &lockedBuffer{}
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// setupReloadHarness boots an admin server backed by a real config
// file. The caller gets back a harness they can use to rewrite the
// file and drive reload operations.
func setupReloadHarness(t *testing.T) *reloadTestHarness {
	t.Helper()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "relay.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(baseReloadYAML), 0o600))

	loader := config.NewFileLoader()
	initialCfg, err := loader.LoadRelayConfig(cfgPath)
	require.NoError(t, err)

	buf := newLockedBuffer()
	logger := slog.New(slog.NewJSONHandler(io.MultiWriter(buf, os.Stderr), &slog.HandlerOptions{Level: slog.LevelDebug}))

	reg := NewMemoryRegistry(logger)
	router := NewPortRouter(reg, logger)
	cl := NewClientListener(ClientListenerConfig{Router: router, Logger: logger})
	emitter := &captureEmitter{}

	// Seed the router with the same ports the initial config defines so
	// that reload diffs see them as "current" and can reconcile deltas.
	idx, err := config.BuildPortIndex(initialCfg.Customers)
	require.NoError(t, err)
	for port, e := range idx.Entries {
		require.NoError(t, router.AddPortMapping(e.CustomerID, port, e.Service, e.ListenAddr, e.MaxStreams))
	}
	// Seed rate limiters matching the initial config so reload diffs can
	// detect changes from the seeded value.
	for _, c := range initialCfg.Customers {
		if c.RateLimit.RequestsPerSecond > 0 {
			cl.SetRateLimiter(c.ID, c.RateLimit.RequestsPerSecond, c.RateLimit.Burst)
		}
	}

	storePath := filepath.Join(dir, "ports.json")
	store := NewSidecarStore(storePath)

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
		Logger:         logger,
		Emitter:        emitter,
		Store:          store,
		ConfigPath:     cfgPath,
		InitialConfig:  initialCfg,
	})

	go func() { admin.Start(ctx) }() //nolint:errcheck // stopped via ctx cancel
	time.Sleep(100 * time.Millisecond)

	t.Cleanup(cancelFn)

	return &reloadTestHarness{
		addr:           addr,
		registry:       reg,
		router:         router,
		clientListener: cl,
		emitter:        emitter,
		store:          store,
		admin:          admin,
		configPath:     cfgPath,
		logBuf:         buf,
		initialCfg:     initialCfg,
	}
}

// rewriteConfig replaces the on-disk relay.yaml with `content`. The
// admin server does not own the file descriptor so the file can be
// swapped at any time without a restart.
func (h *reloadTestHarness) rewriteConfig(t *testing.T, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(h.configPath, []byte(content), 0o600))
}

// --- GET /config ---

func TestAdmin_GetConfig_Fields(t *testing.T) {
	h := setupReloadHarness(t)

	resp, err := http.Get("http://" + h.addr + "/config")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var decoded config.RelayConfig
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&decoded))
	require.Len(t, decoded.Customers, 1)
	assert.Equal(t, "customer-001", decoded.Customers[0].ID)
	require.Len(t, decoded.Customers[0].Ports, 1)
	assert.Equal(t, 18080, decoded.Customers[0].Ports[0].Port)
	assert.Equal(t, "http", decoded.Customers[0].Ports[0].Service)
	assert.Equal(t, "127.0.0.1:18443", decoded.Server.ListenAddr)
}

func TestAdmin_GetConfig_MethodNotAllowed(t *testing.T) {
	h := setupReloadHarness(t)

	resp, err := http.Post("http://"+h.addr+"/config", "application/json", bytes.NewBufferString("{}"))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

// TestAdmin_GetConfig_DefensiveCopy verifies that the JSON body is a
// snapshot, not a live view: after the reply is sent, mutating the
// internal currentCfg must not be reflected by the already-received
// response. The safest end-to-end signal is that a subsequent Reload()
// surfacing different values does not rewrite the first response.
func TestAdmin_GetConfig_DefensiveCopy(t *testing.T) {
	h := setupReloadHarness(t)

	resp, err := http.Get("http://" + h.addr + "/config")
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	resp.Body.Close()

	// Mutate on disk and reload.
	newYAML := strings.Replace(baseReloadYAML, "port: 18080", "port: 18090", 1)
	h.rewriteConfig(t, newYAML)
	_, err = h.admin.Reload(context.Background())
	require.NoError(t, err)

	// The original body still reports 18080 -- JSON is a snapshot, not a pointer.
	assert.Contains(t, string(body), "18080")
	assert.NotContains(t, string(body), "18090")
}

// --- POST /reload + Reload() method ---

func TestAdmin_Reload_AddsPort(t *testing.T) {
	h := setupReloadHarness(t)

	// Append a new port to the same customer.
	newYAML := strings.Replace(baseReloadYAML,
		"      - port: 18080\n        service: http\n        listen_addr: \"127.0.0.1\"",
		"      - port: 18080\n        service: http\n        listen_addr: \"127.0.0.1\"\n      - port: 18081\n        service: ssh\n        listen_addr: \"127.0.0.1\"",
		1,
	)
	h.rewriteConfig(t, newYAML)

	summary, err := h.admin.Reload(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, summary.PortsAdded)
	assert.Equal(t, 0, summary.PortsRemoved)
	assert.Equal(t, 0, summary.PortsUpdated)
	assert.Equal(t, 0, summary.PortsRejected)

	// New port is now in the router.
	info, ok := h.router.GetPort(18081)
	require.True(t, ok)
	assert.Equal(t, "customer-001", info.CustomerID)
	assert.Equal(t, "ssh", info.Service)

	// Listener bound on port 18081.
	assert.NotNil(t, h.clientListener.Addr(18081))
}

func TestAdmin_Reload_RemovesPort(t *testing.T) {
	h := setupReloadHarness(t)

	// First reload: add a second port 18081 via YAML so it lives both in
	// the router and in currentCfg.
	withExtra := strings.Replace(baseReloadYAML,
		"      - port: 18080\n        service: http\n        listen_addr: \"127.0.0.1\"",
		"      - port: 18080\n        service: http\n        listen_addr: \"127.0.0.1\"\n      - port: 18081\n        service: ssh\n        listen_addr: \"127.0.0.1\"",
		1,
	)
	h.rewriteConfig(t, withExtra)
	_, err := h.admin.Reload(context.Background())
	require.NoError(t, err)
	_, ok := h.router.GetPort(18081)
	require.True(t, ok, "precondition: 18081 must exist before the removal reload")

	// Second reload: drop 18081.
	h.rewriteConfig(t, baseReloadYAML)
	summary, err := h.admin.Reload(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, summary.PortsRemoved)
	assert.Equal(t, 0, summary.PortsAdded)

	// Port gone from the router.
	_, ok = h.router.GetPort(18081)
	assert.False(t, ok)
}

func TestAdmin_Reload_UpdatesPort(t *testing.T) {
	h := setupReloadHarness(t)

	// Change the service name on the existing port.
	newYAML := strings.Replace(baseReloadYAML,
		"service: http", "service: samba", 1)
	h.rewriteConfig(t, newYAML)

	summary, err := h.admin.Reload(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, summary.PortsUpdated)
	assert.Equal(t, 0, summary.PortsAdded)
	assert.Equal(t, 0, summary.PortsRemoved)

	info, ok := h.router.GetPort(18080)
	require.True(t, ok)
	assert.Equal(t, "samba", info.Service)
	// customer_id preserved.
	assert.Equal(t, "customer-001", info.CustomerID)
}

func TestAdmin_Reload_ParseError_LeavesStateUnchanged(t *testing.T) {
	h := setupReloadHarness(t)

	snapshotBefore := h.router.ListPorts()
	h.rewriteConfig(t, "{{{not yaml")

	// Direct Reload call returns an error.
	_, err := h.admin.Reload(context.Background())
	require.Error(t, err)

	// POST /reload returns 422.
	resp, err := http.Post("http://"+h.addr+"/reload", "application/json", http.NoBody)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	// Router state unchanged.
	snapshotAfter := h.router.ListPorts()
	assert.Equal(t, len(snapshotBefore), len(snapshotAfter))
}

func TestAdmin_Reload_ValidateError_LeavesStateUnchanged(t *testing.T) {
	h := setupReloadHarness(t)

	// Missing required tls.cert_file -- loader validator rejects.
	bad := strings.Replace(baseReloadYAML,
		"cert_file: /etc/atlax/relay.crt", "", 1)
	h.rewriteConfig(t, bad)

	_, err := h.admin.Reload(context.Background())
	require.Error(t, err)

	// State unchanged.
	info, ok := h.router.GetPort(18080)
	require.True(t, ok)
	assert.Equal(t, "http", info.Service)
}

func TestAdmin_Reload_DuplicatePort_Returns422(t *testing.T) {
	h := setupReloadHarness(t)

	// Config where two customers claim the same port.
	dupYAML := `
server:
  listen_addr: "127.0.0.1:18443"
tls:
  cert_file: /etc/atlax/relay.crt
  key_file: /etc/atlax/relay.key
  client_ca_file: /etc/atlax/customer-ca.crt
customers:
  - id: customer-001
    ports:
      - port: 18080
        service: http
  - id: customer-002
    ports:
      - port: 18080
        service: smb
`
	h.rewriteConfig(t, dupYAML)

	_, err := h.admin.Reload(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "port 18080")

	// Router state unchanged -- port still belongs to customer-001.
	info, ok := h.router.GetPort(18080)
	require.True(t, ok)
	assert.Equal(t, "customer-001", info.CustomerID)
}

// TestAdmin_Reload_CustomerIDImmutable is the core security test.
// An attacker who can flip a port's customer_id in relay.yaml and
// trigger a reload must NOT be able to reroute the port to a different
// tenant. The router entry must be preserved verbatim; the port
// rejection must be audited and logged.
func TestAdmin_Reload_CustomerIDImmutable(t *testing.T) {
	h := setupReloadHarness(t)

	// New config has a second customer, and re-assigns port 18080 to it.
	// The original port remains in the YAML but under customer-002.
	// Independently, a non-conflicting port change exists (service rename
	// on a *new* port 18099 assigned to customer-001): this must succeed
	// in the same reload, proving rejection is per-port, not global.
	bad := `
server:
  listen_addr: "127.0.0.1:18443"
tls:
  cert_file: /etc/atlax/relay.crt
  key_file: /etc/atlax/relay.key
  client_ca_file: /etc/atlax/customer-ca.crt
customers:
  - id: customer-001
    ports:
      - port: 18099
        service: samba
        listen_addr: "127.0.0.1"
    max_streams: 4
    rate_limit:
      requests_per_second: 10
      burst: 20
  - id: customer-002
    ports:
      - port: 18080
        service: http
        listen_addr: "127.0.0.1"
    max_streams: 4
`
	h.rewriteConfig(t, bad)

	summary, err := h.admin.Reload(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, summary.PortsRejected, "port 18080 customer change must be rejected")

	// Port 18080 is still bound to customer-001 -- immutability enforced.
	info, ok := h.router.GetPort(18080)
	require.True(t, ok, "port 18080 must remain in the router after the rejection")
	assert.Equal(t, "customer-001", info.CustomerID,
		"customer_id must NOT change via reload; cross-tenant routing must be impossible")

	// The unrelated add (18099 for customer-001) went through.
	info2, ok := h.router.GetPort(18099)
	require.True(t, ok, "non-conflicting addition must succeed in the same reload")
	assert.Equal(t, "customer-001", info2.CustomerID)
	assert.Equal(t, "samba", info2.Service)

	// Error was logged for the rejected port.
	assert.Contains(t, h.logBuf.String(), "customer_id_immutable")
	assert.Contains(t, h.logBuf.String(), "18080")
}

func TestAdmin_Reload_RateLimitChange(t *testing.T) {
	h := setupReloadHarness(t)

	newYAML := strings.Replace(baseReloadYAML,
		"requests_per_second: 10", "requests_per_second: 25", 1)
	h.rewriteConfig(t, newYAML)

	summary, err := h.admin.Reload(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, summary.RateLimitsChanged)
}

func TestAdmin_Reload_RestartRequiredFields(t *testing.T) {
	h := setupReloadHarness(t)

	// Change TLS cert path -- a restart-required field. The port data is
	// unchanged so this is the sole observable delta.
	newYAML := strings.Replace(baseReloadYAML,
		"cert_file: /etc/atlax/relay.crt",
		"cert_file: /etc/atlax/relay-new.crt", 1)
	h.rewriteConfig(t, newYAML)

	summary, err := h.admin.Reload(context.Background())
	require.NoError(t, err)
	assert.Contains(t, summary.RestartRequired, "tls.cert_file",
		"changed tls.cert_file must be reported as requiring restart")
	assert.Contains(t, h.logBuf.String(), "restart_required")
}

func TestAdmin_Reload_AuditEventEmitted(t *testing.T) {
	h := setupReloadHarness(t)

	_, err := h.admin.Reload(context.Background())
	require.NoError(t, err)

	events := h.emitter.snapshot()
	found := false
	for _, e := range events {
		if e.Action == audit.ActionAdminReload {
			found = true
			break
		}
	}
	assert.True(t, found, "admin.reload audit event must be emitted on a successful reload")
}

func TestAdmin_Reload_StoreSaveCalled(t *testing.T) {
	h := setupReloadHarness(t)

	// Add a port via reload; the sidecar file should reflect the new set.
	newYAML := strings.Replace(baseReloadYAML,
		"      - port: 18080\n        service: http\n        listen_addr: \"127.0.0.1\"",
		"      - port: 18080\n        service: http\n        listen_addr: \"127.0.0.1\"\n      - port: 18091\n        service: ssh\n        listen_addr: \"127.0.0.1\"",
		1,
	)
	h.rewriteConfig(t, newYAML)

	_, err := h.admin.Reload(context.Background())
	require.NoError(t, err)

	data, err := h.store.Load()
	require.NoError(t, err)
	ports := make(map[int]string)
	for _, p := range data.Ports {
		ports[p.Port] = p.CustomerID
	}
	assert.Equal(t, "customer-001", ports[18091], "sidecar must reflect reload result")
}

func TestAdmin_Reload_Concurrency_Serialized(t *testing.T) {
	h := setupReloadHarness(t)

	// Two reloads in-flight must not race on currentCfg.
	done := make(chan struct{}, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := h.admin.Reload(context.Background())
			assert.NoError(t, err)
			done <- struct{}{}
		}()
	}
	<-done
	<-done
}

func TestAdmin_Reload_HTTPSuccess(t *testing.T) {
	h := setupReloadHarness(t)

	resp, err := http.Post("http://"+h.addr+"/reload", "application/json", http.NoBody)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var summary ReloadSummary
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&summary))
	// No changes between baseline and the live config, so no deltas.
	assert.Equal(t, 0, summary.PortsAdded)
	assert.Equal(t, 0, summary.PortsRemoved)
	assert.Equal(t, 0, summary.PortsUpdated)
	assert.Equal(t, 0, summary.PortsRejected)
}

func TestAdmin_Reload_MethodNotAllowed(t *testing.T) {
	h := setupReloadHarness(t)

	resp, err := http.Get("http://" + h.addr + "/reload")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

func TestAdmin_Reload_MissingConfigPath(t *testing.T) {
	// Admin server with no ConfigPath: /reload must fail cleanly, never
	// panic. This guards against misconfiguration during boot.
	reg := NewMemoryRegistry(slog.Default())
	router := NewPortRouter(reg, slog.Default())
	cl := NewClientListener(ClientListenerConfig{Router: router, Logger: slog.Default()})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	ln.Close()

	ctx, cancelFn := context.WithCancel(context.Background())
	defer cancelFn()

	admin := NewAdminServer(&AdminConfig{
		Addr:           addr,
		Registry:       reg,
		Router:         router,
		ClientListener: cl,
		Logger:         slog.Default(),
	})
	go func() { admin.Start(ctx) }() //nolint:errcheck // stopped via ctx cancel
	time.Sleep(100 * time.Millisecond)

	resp, err := http.Post("http://"+addr+"/reload", "application/json", http.NoBody)
	require.NoError(t, err)
	defer resp.Body.Close()
	// Unconfigured = 422 (operator-actionable), not 500.
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}
