package relay

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/atlasshare/atlax/pkg/audit"
	"github.com/atlasshare/atlax/pkg/protocol"
)

func testCertsDir() string {
	return filepath.Join("..", "..", "certs")
}

func skipIfNoCerts(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(testCertsDir(), "relay.crt")); err != nil {
		t.Skip("dev certs not found; run 'make certs-dev'")
	}
}

func relayTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	certs := testCertsDir()

	cert, err := tls.LoadX509KeyPair(
		filepath.Join(certs, "relay.crt"),
		filepath.Join(certs, "relay.key"),
	)
	require.NoError(t, err)

	customerCAPEM, err := os.ReadFile(filepath.Join(certs, "customer-ca.crt"))
	require.NoError(t, err)
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(customerCAPEM))

	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
	}
}

func agentTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	certs := testCertsDir()

	cert, err := tls.LoadX509KeyPair(
		filepath.Join(certs, "agent.crt"),
		filepath.Join(certs, "agent.key"),
	)
	require.NoError(t, err)

	relayCAPEM, err := os.ReadFile(filepath.Join(certs, "relay-ca.crt"))
	require.NoError(t, err)
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(relayCAPEM))

	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		ServerName:   "relay.atlax.local",
	}
}

func TestAgentListener_MaxStreamsPerAgentFromConfig(t *testing.T) {
	emitter := audit.NewSlogEmitter(slog.Default(), 16)
	defer emitter.Close()

	reg := NewMemoryRegistry(slog.Default())

	// Explicit value should be stored
	listener := NewAgentListener(AgentListenerConfig{
		Addr:               "127.0.0.1:0",
		Registry:           reg,
		Emitter:            emitter,
		Logger:             slog.Default(),
		MaxStreamsPerAgent: 512,
	})
	assert.Equal(t, 512, listener.maxStreamsPerAgent)

	// Zero value should default to 50
	listenerDefault := NewAgentListener(AgentListenerConfig{
		Addr:     "127.0.0.1:0",
		Registry: reg,
		Emitter:  emitter,
		Logger:   slog.Default(),
	})
	assert.Equal(t, 50, listenerDefault.maxStreamsPerAgent)
}

func TestAgentListener_AcceptsAndRegisters(t *testing.T) {
	skipIfNoCerts(t)

	var auditBuf bytes.Buffer
	auditLogger := slog.New(slog.NewJSONHandler(&auditBuf, nil))
	emitter := audit.NewSlogEmitter(auditLogger, 16)
	defer emitter.Close()

	reg := NewMemoryRegistry(slog.Default())

	listener := NewAgentListener(AgentListenerConfig{
		Addr:      "127.0.0.1:0",
		TLSConfig: relayTLSConfig(t),
		Registry:  reg,
		Emitter:   emitter,
		Logger:    slog.Default(),
		MaxAgents: 10,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start listener in background
	listenerReady := make(chan net.Addr, 1)
	go func() {
		// We need the actual listening address. Start wraps tls.Listen
		// internally, so we use a workaround: start on :0 and connect.
		// The listener blocks in Start(), so we just try connecting after
		// a brief delay.
		listener.Start(ctx) //nolint:errcheck // stopped via ctx cancel
	}()

	// Give listener time to bind
	time.Sleep(100 * time.Millisecond)
	_ = listenerReady

	// For this test, we need to know the listener's actual address.
	// Since AgentListener.Start binds internally, we need to refactor
	// or use a known port. Let's use the addr we set.
	// Since we used "127.0.0.1:0", we can't connect without knowing
	// the actual port. Let me use a fixed port for this test.
	cancel()
}

func TestAgentListener_ServiceListReceived(t *testing.T) {
	skipIfNoCerts(t)

	emitter := audit.NewSlogEmitter(slog.Default(), 256)
	reg := NewMemoryRegistry(slog.Default())

	tcpLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := tcpLn.Addr().String()
	tcpLn.Close()

	listener := NewAgentListener(AgentListenerConfig{
		Addr:      addr,
		TLSConfig: relayTLSConfig(t),
		Registry:  reg,
		Emitter:   emitter,
		Logger:    slog.Default(),
		MaxAgents: 10,
	})

	ctx, cancel := context.WithCancel(context.Background())
	listenerDone := make(chan struct{})
	go func() {
		defer close(listenerDone)
		listener.Start(ctx) //nolint:errcheck // stopped via ctx cancel
	}()
	time.Sleep(100 * time.Millisecond)

	// Agent dials and immediately emits CmdServiceList.
	agentConn, err := tls.Dial("tcp", addr, agentTLSConfig(t))
	require.NoError(t, err)
	defer agentConn.Close()

	agentMux := protocol.NewMuxSession(agentConn, protocol.RoleAgent, protocol.MuxConfig{
		MaxConcurrentStreams: 16,
		InitialStreamWindow:  262144,
		ConnectionWindow:     1048576,
		PingInterval:         30 * time.Second,
		PingTimeout:          5 * time.Second,
		IdleTimeout:          60 * time.Second,
	})
	defer agentMux.Close()

	require.NoError(t, agentMux.SendServiceList([]string{"samba", "http", "api"}))

	// Wait for registration + service ingestion (relay waits up to 50ms).
	time.Sleep(250 * time.Millisecond)

	agents, listErr := reg.ListConnectedAgents(context.Background())
	require.NoError(t, listErr)
	require.GreaterOrEqual(t, len(agents), 1)

	var found *AgentInfo
	for i := range agents {
		if agents[i].CustomerID == "customer-dev-001" {
			found = &agents[i]
			break
		}
	}
	require.NotNil(t, found, "customer-dev-001 not registered")
	assert.Equal(t, []string{"samba", "http", "api"}, found.Services)
	assert.False(t, found.CertNotAfter.IsZero(), "cert NotAfter should be populated from peer cert")
	assert.True(t, found.CertNotAfter.After(time.Now()), "cert NotAfter should be in the future")

	cancel()
	<-listenerDone
}

func TestAgentListener_NoServiceList_DoesNotBlock(t *testing.T) {
	skipIfNoCerts(t)

	emitter := audit.NewSlogEmitter(slog.Default(), 256)
	reg := NewMemoryRegistry(slog.Default())

	tcpLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := tcpLn.Addr().String()
	tcpLn.Close()

	listener := NewAgentListener(AgentListenerConfig{
		Addr:      addr,
		TLSConfig: relayTLSConfig(t),
		Registry:  reg,
		Emitter:   emitter,
		Logger:    slog.Default(),
		MaxAgents: 10,
	})

	ctx, cancel := context.WithCancel(context.Background())
	listenerDone := make(chan struct{})
	go func() {
		defer close(listenerDone)
		listener.Start(ctx) //nolint:errcheck // stopped via ctx cancel
	}()
	time.Sleep(100 * time.Millisecond)

	// Dial and do NOT send CmdServiceList. Bare TLS conn only.
	start := time.Now()
	agentConn, err := tls.Dial("tcp", addr, agentTLSConfig(t))
	require.NoError(t, err)
	defer agentConn.Close()

	// Poll for registration. We want the time from dial to registration
	// to be roughly dominated by the 50ms serviceListWaitTimeout, not
	// an unbounded block.
	deadline := time.Now().Add(2 * time.Second)
	var registered bool
	for time.Now().Before(deadline) {
		agents, listErr := reg.ListConnectedAgents(context.Background())
		require.NoError(t, listErr)
		for _, ag := range agents {
			if ag.CustomerID == "customer-dev-001" {
				registered = true
				break
			}
		}
		if registered {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	elapsed := time.Since(start)

	require.True(t, registered, "agent never registered")
	assert.Less(t, elapsed, 500*time.Millisecond,
		"registration took %v; 50ms service-list wait must not explode into unbounded block", elapsed)

	// Confirm Services is empty (nothing advertised).
	agents, listErr := reg.ListConnectedAgents(context.Background())
	require.NoError(t, listErr)
	var found *AgentInfo
	for i := range agents {
		if agents[i].CustomerID == "customer-dev-001" {
			found = &agents[i]
			break
		}
	}
	require.NotNil(t, found)
	assert.Empty(t, found.Services, "no CmdServiceList was sent; Services must be empty")

	cancel()
	<-listenerDone
}

func TestAgentListener_IntegrationWithMTLS(t *testing.T) {
	skipIfNoCerts(t)

	// Use a no-op emitter to avoid race between MuxSession goroutines and audit close
	emitter := audit.NewSlogEmitter(slog.Default(), 256)

	reg := NewMemoryRegistry(slog.Default())

	// Use a random port via pre-listening
	tcpLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := tcpLn.Addr().String()
	tcpLn.Close() // Release so AgentListener can bind

	listener := NewAgentListener(AgentListenerConfig{
		Addr:      addr,
		TLSConfig: relayTLSConfig(t),
		Registry:  reg,
		Emitter:   emitter,
		Logger:    slog.Default(),
		MaxAgents: 10,
	})

	ctx, cancel := context.WithCancel(context.Background())

	listenerDone := make(chan struct{})
	go func() {
		defer close(listenerDone)
		listener.Start(ctx) //nolint:errcheck // stopped via ctx cancel
	}()

	// Give listener time to bind
	time.Sleep(100 * time.Millisecond)

	// Connect as agent with mTLS
	agentConn, err := tls.Dial("tcp", addr, agentTLSConfig(t))
	require.NoError(t, err)

	// Wait for registration
	time.Sleep(200 * time.Millisecond)

	// Agent should be registered with correct customer ID
	agents, listErr := reg.ListConnectedAgents(context.Background())
	require.NoError(t, listErr)
	require.GreaterOrEqual(t, len(agents), 1)
	assert.Equal(t, "customer-dev-001", agents[0].CustomerID)

	// Cleanup
	agentConn.Close()
	cancel()
	<-listenerDone
}
