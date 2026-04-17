package relay

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeTestCert generates a self-signed cert valid for the given duration
// and writes a PEM file to `path`. Returns the computed NotAfter time.
func writeTestCert(t *testing.T, path string, validFor time.Duration) time.Time {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	notAfter := time.Now().Add(validFor).UTC()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour).UTC(),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)

	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()
	require.NoError(t, pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: der}))
	return notAfter
}

// testAdminServerWithStatus starts an admin server with CertPaths and
// ConfigVersion populated, for /status tests. Returns the address and
// the live registry and router so callers can drive agent/port state.
func testAdminServerWithStatus(t *testing.T, certPaths []CertNamePath, configVersion string) (addr string, reg *MemoryRegistry, router *PortRouter) {
	t.Helper()
	reg = NewMemoryRegistry(slog.Default())
	router = NewPortRouter(reg, slog.Default())
	cl := NewClientListener(ClientListenerConfig{Router: router, Logger: slog.Default()})

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
		CertPaths:      certPaths,
		ConfigVersion:  configVersion,
	})

	go func() {
		admin.Start(ctx) //nolint:errcheck // stopped via ctx cancel
	}()
	time.Sleep(100 * time.Millisecond)

	t.Cleanup(cancelFn)
	return addr, reg, router
}

// getStatus fetches /status and decodes into a StatusResponse, asserting 200.
func getStatus(t *testing.T, addr string) StatusResponse {
	t.Helper()
	resp, err := http.Get("http://" + addr + "/status")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var status StatusResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&status))
	return status
}

func TestAdmin_Status_Fields(t *testing.T) {
	addr, _, _ := testAdminServerWithStatus(t, nil, "v0.1.3")

	resp, err := http.Get("http://" + addr + "/status")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	// Decode into a generic map to verify all JSON keys are present.
	var raw map[string]json.RawMessage
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&raw))

	wantKeys := []string{
		"status", "uptime", "uptime_seconds",
		"agents_connected", "streams_active", "ports_active",
		"config_version", "relay_certs",
	}
	for _, k := range wantKeys {
		_, ok := raw[k]
		assert.True(t, ok, "missing JSON key: %s", k)
	}

	// Also confirm strong-typed decode succeeds.
	var status StatusResponse
	require.NoError(t, json.Unmarshal(collectRaw(raw), &status))
	assert.Equal(t, "ok", status.Status)
	assert.Equal(t, "v0.1.3", status.ConfigVersion)
	assert.Greater(t, status.UptimeSeconds, 0.0)
	assert.NotEmpty(t, status.Uptime)
}

// collectRaw reassembles a raw JSON object map back into a single JSON
// object for strong-typed decoding. Test-only convenience.
func collectRaw(raw map[string]json.RawMessage) []byte {
	buf, _ := json.Marshal(raw)
	return buf
}

func TestAdmin_Status_AgentCount(t *testing.T) {
	addr, reg, _ := testAdminServerWithStatus(t, nil, "test")

	// No agents initially.
	status := getStatus(t, addr)
	assert.Equal(t, 0, status.AgentsConnected)
	assert.Equal(t, 0, status.StreamsActive)

	// Register one agent.
	conn, agentMux := testConnectionPair("customer-status-1")
	defer conn.Close()
	defer agentMux.Close()
	require.NoError(t, reg.Register(context.Background(), "customer-status-1", conn))

	status = getStatus(t, addr)
	assert.Equal(t, 1, status.AgentsConnected)
	// StreamsActive starts at 0 for a freshly-registered mux.
	assert.GreaterOrEqual(t, status.StreamsActive, 0)
}

func TestAdmin_Status_AgentCerts(t *testing.T) {
	addr, reg, _ := testAdminServerWithStatus(t, nil, "test")

	// Register an agent with a cert expiry set.
	conn, agentMux := testConnectionPair("customer-cert-1")
	defer conn.Close()
	defer agentMux.Close()
	expiry := time.Now().Add(60 * 24 * time.Hour) // 60 days
	conn.SetCertNotAfter(expiry)
	require.NoError(t, reg.Register(context.Background(), "customer-cert-1", conn))

	status := getStatus(t, addr)
	require.Len(t, status.AgentCerts, 1)
	assert.Equal(t, "customer-cert-1", status.AgentCerts[0].Name)
	assert.True(t, status.AgentCerts[0].DaysLeft >= 59)
}

func TestAdmin_Status_AgentCerts_ZeroNotAfterOmitted(t *testing.T) {
	addr, reg, _ := testAdminServerWithStatus(t, nil, "test")

	// Register an agent with no cert expiry (zero value).
	conn, agentMux := testConnectionPair("customer-no-cert")
	defer conn.Close()
	defer agentMux.Close()
	require.NoError(t, reg.Register(context.Background(), "customer-no-cert", conn))

	status := getStatus(t, addr)
	assert.Empty(t, status.AgentCerts)
}

func TestAdmin_Status_PortCount(t *testing.T) {
	addr, _, router := testAdminServerWithStatus(t, nil, "test")

	// Add 3 port mappings via the router (bypasses the listener start path).
	require.NoError(t, router.AddPortMapping("customer-a", 28001, "http", "0.0.0.0", 0))
	require.NoError(t, router.AddPortMapping("customer-a", 28002, "https", "0.0.0.0", 0))
	require.NoError(t, router.AddPortMapping("customer-b", 28003, "ssh", "0.0.0.0", 0))

	status := getStatus(t, addr)
	assert.Equal(t, 3, status.PortsActive)
}

func TestAdmin_Status_RelayCerts(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "relay.crt")
	expectedNotAfter := writeTestCert(t, certPath, 90*24*time.Hour)

	certPaths := []CertNamePath{{Name: "relay", Path: certPath}}
	addr, _, _ := testAdminServerWithStatus(t, certPaths, "test")

	status := getStatus(t, addr)
	require.Len(t, status.RelayCerts, 1)

	entry := status.RelayCerts[0]
	assert.Equal(t, "relay", entry.Name)
	parsed, err := time.Parse(time.RFC3339, entry.ExpiresAt)
	require.NoError(t, err, "expires_at must be RFC3339")
	assert.WithinDuration(t, expectedNotAfter, parsed, time.Second)
	// Expect ~89-90 days remaining.
	assert.GreaterOrEqual(t, entry.DaysLeft, 89)
	assert.LessOrEqual(t, entry.DaysLeft, 90)
}

func TestAdmin_Status_CertMissing(t *testing.T) {
	dir := t.TempDir()
	validPath := filepath.Join(dir, "valid.crt")
	writeTestCert(t, validPath, 24*time.Hour)
	missingPath := filepath.Join(dir, "does-not-exist.crt")

	certPaths := []CertNamePath{
		{Name: "valid", Path: validPath},
		{Name: "missing", Path: missingPath},
	}
	addr, _, _ := testAdminServerWithStatus(t, certPaths, "test")

	// Missing cert should not cause the /status call to fail; the
	// missing entry is simply omitted from the response.
	status := getStatus(t, addr)
	require.Len(t, status.RelayCerts, 1)
	assert.Equal(t, "valid", status.RelayCerts[0].Name)
}

func TestAdmin_Status_CertMalformed(t *testing.T) {
	dir := t.TempDir()
	badPath := filepath.Join(dir, "bad.crt")
	require.NoError(t, os.WriteFile(badPath, []byte("not a PEM file"), 0o600))

	certPaths := []CertNamePath{{Name: "bad", Path: badPath}}
	addr, _, _ := testAdminServerWithStatus(t, certPaths, "test")

	// Malformed PEM should be skipped, /status still returns 200.
	status := getStatus(t, addr)
	assert.Empty(t, status.RelayCerts)
}

func TestAdmin_Status_UptimeMonotonic(t *testing.T) {
	addr, _, _ := testAdminServerWithStatus(t, nil, "test")

	first := getStatus(t, addr)
	time.Sleep(50 * time.Millisecond)
	second := getStatus(t, addr)

	assert.GreaterOrEqual(t, second.UptimeSeconds, first.UptimeSeconds)
}

func TestAdmin_Status_MethodNotAllowed(t *testing.T) {
	addr, _, _ := testAdminServerWithStatus(t, nil, "test")

	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/status", http.NoBody)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

func TestAdmin_Status_NoCertsConfigured(t *testing.T) {
	addr, _, _ := testAdminServerWithStatus(t, nil, "test")

	// With no certs configured, relay_certs should be an empty slice
	// (serialized as `[]`, not `null`).
	resp, err := http.Get("http://" + addr + "/status")
	require.NoError(t, err)
	defer resp.Body.Close()

	var raw map[string]json.RawMessage
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&raw))
	assert.Equal(t, "[]", string(raw["relay_certs"]))
}

func TestParseCertExpiry_Valid(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "ok.crt")
	expectedNotAfter := writeTestCert(t, certPath, 30*24*time.Hour)

	entry, err := parseCertExpiry("ok", certPath)
	require.NoError(t, err)
	assert.Equal(t, "ok", entry.Name)
	parsed, parseErr := time.Parse(time.RFC3339, entry.ExpiresAt)
	require.NoError(t, parseErr)
	assert.WithinDuration(t, expectedNotAfter, parsed, time.Second)
	assert.GreaterOrEqual(t, entry.DaysLeft, 29)
	assert.LessOrEqual(t, entry.DaysLeft, 30)
}

func TestParseCertExpiry_MissingFile(t *testing.T) {
	_, err := parseCertExpiry("missing", filepath.Join(t.TempDir(), "nope.crt"))
	require.Error(t, err)
}

func TestParseCertExpiry_MalformedPEM(t *testing.T) {
	dir := t.TempDir()
	badPath := filepath.Join(dir, "bad.crt")
	require.NoError(t, os.WriteFile(badPath, []byte("no pem here"), 0o600))

	_, err := parseCertExpiry("bad", badPath)
	require.Error(t, err)
}

func TestParseCertExpiry_ExpiredCert(t *testing.T) {
	dir := t.TempDir()
	expiredPath := filepath.Join(dir, "expired.crt")
	writeTestCert(t, expiredPath, -24*time.Hour)

	entry, err := parseCertExpiry("expired", expiredPath)
	require.NoError(t, err)
	// Expired certs are reported with negative DaysLeft so operators
	// can alert on them.
	assert.LessOrEqual(t, entry.DaysLeft, 0)
}

// Ensure parseCertExpiry tolerates a PEM block that is the wrong type
// (e.g., a private key file mistakenly configured as a cert path).
func TestParseCertExpiry_WrongPEMType(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: []byte{0x01, 0x02, 0x03},
	})
	require.NoError(t, os.WriteFile(keyPath, pemBytes, 0o600))

	_, err := parseCertExpiry("key", keyPath)
	require.Error(t, err)
}

// TestAdmin_Status_DefensiveCopy ensures that mutating the returned
// RelayCerts slice on the client side cannot race with the server's
// internal state. The server must build a fresh slice per request.
func TestAdmin_Status_DefensiveCopy(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "relay.crt")
	writeTestCert(t, certPath, 72*time.Hour)

	certPaths := []CertNamePath{
		{Name: "a", Path: certPath},
		{Name: "b", Path: certPath},
	}
	addr, _, _ := testAdminServerWithStatus(t, certPaths, "test")

	// Race two parallel /status calls; if the server returned a shared
	// backing array, -race would flag. This is a best-effort smoke test.
	done := make(chan StatusResponse, 2)
	for i := 0; i < 2; i++ {
		go func() {
			done <- getStatus(t, addr)
		}()
	}
	for i := 0; i < 2; i++ {
		s := <-done
		require.Len(t, s.RelayCerts, 2)
	}
}
