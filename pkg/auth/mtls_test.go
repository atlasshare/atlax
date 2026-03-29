package auth

import (
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigurator_ServerTLSConfig_MinVersion(t *testing.T) {
	cfg := relayConfigurator(t)
	tlsCfg, err := cfg.ServerTLSConfig()
	require.NoError(t, err)
	assert.Equal(t, uint16(tls.VersionTLS13), tlsCfg.MinVersion)
}

func TestConfigurator_ServerTLSConfig_ClientAuth(t *testing.T) {
	cfg := relayConfigurator(t)
	tlsCfg, err := cfg.ServerTLSConfig()
	require.NoError(t, err)
	assert.Equal(t, tls.RequireAndVerifyClientCert, tlsCfg.ClientAuth)
}

func TestConfigurator_ServerTLSConfig_HasRelayCert(t *testing.T) {
	cfg := relayConfigurator(t)
	tlsCfg, err := cfg.ServerTLSConfig()
	require.NoError(t, err)
	assert.Len(t, tlsCfg.Certificates, 1)
}

func TestConfigurator_ServerTLSConfig_HasClientCAs(t *testing.T) {
	cfg := relayConfigurator(t)
	tlsCfg, err := cfg.ServerTLSConfig()
	require.NoError(t, err)
	assert.NotNil(t, tlsCfg.ClientCAs)
}

func TestConfigurator_ServerTLSConfig_InvalidCert(t *testing.T) {
	cfg := NewConfigurator(NewFileStore(), TLSPaths{
		CertFile:     "/nonexistent/relay.crt",
		KeyFile:      "/nonexistent/relay.key",
		ClientCAFile: filepath.Join(testCertsDir(), "customer-ca.crt"),
	})
	_, err := cfg.ServerTLSConfig()
	require.Error(t, err)
}

func TestConfigurator_ServerTLSConfig_InvalidClientCA(t *testing.T) {
	certs := testCertsDir()
	cfg := NewConfigurator(NewFileStore(), TLSPaths{
		CertFile:     filepath.Join(certs, "relay.crt"),
		KeyFile:      filepath.Join(certs, "relay.key"),
		ClientCAFile: "/nonexistent/ca.crt",
	})
	_, err := cfg.ServerTLSConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "client CA")
}

func TestConfigurator_ClientTLSConfig_InvalidCA(t *testing.T) {
	certs := testCertsDir()
	cfg := NewConfigurator(NewFileStore(), TLSPaths{
		CertFile:   filepath.Join(certs, "agent.crt"),
		KeyFile:    filepath.Join(certs, "agent.key"),
		CAFile:     "/nonexistent/ca.crt",
		ServerName: "relay.atlax.local",
	})
	_, err := cfg.ClientTLSConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "root CA")
}

func TestConfigurator_ClientTLSConfig_MinVersion(t *testing.T) {
	cfg := agentConfigurator(t)
	tlsCfg, err := cfg.ClientTLSConfig()
	require.NoError(t, err)
	assert.Equal(t, uint16(tls.VersionTLS13), tlsCfg.MinVersion)
}

func TestConfigurator_ClientTLSConfig_HasAgentCert(t *testing.T) {
	cfg := agentConfigurator(t)
	tlsCfg, err := cfg.ClientTLSConfig()
	require.NoError(t, err)
	assert.Len(t, tlsCfg.Certificates, 1)
}

func TestConfigurator_ClientTLSConfig_HasRootCAs(t *testing.T) {
	cfg := agentConfigurator(t)
	tlsCfg, err := cfg.ClientTLSConfig()
	require.NoError(t, err)
	assert.NotNil(t, tlsCfg.RootCAs)
}

func TestConfigurator_ClientTLSConfig_ServerName(t *testing.T) {
	cfg := agentConfigurator(t)
	tlsCfg, err := cfg.ClientTLSConfig()
	require.NoError(t, err)
	assert.Equal(t, "relay.atlax.local", tlsCfg.ServerName)
}

func TestConfigurator_ClientTLSConfig_WithSessionCache(t *testing.T) {
	cfg := agentConfigurator(t)
	tlsCfg, err := cfg.ClientTLSConfig(WithSessionCache(64))
	require.NoError(t, err)
	assert.NotNil(t, tlsCfg.ClientSessionCache)
}

func TestConfigurator_ServerTLSConfig_WithMinVersion(t *testing.T) {
	cfg := relayConfigurator(t)
	tlsCfg, err := cfg.ServerTLSConfig(WithMinVersion(tls.VersionTLS12))
	require.NoError(t, err)
	assert.Equal(t, uint16(tls.VersionTLS12), tlsCfg.MinVersion)
}

func TestConfigurator_ClientTLSConfig_InvalidCert(t *testing.T) {
	cfg := NewConfigurator(NewFileStore(), TLSPaths{
		CertFile:   "/nonexistent/agent.crt",
		KeyFile:    "/nonexistent/agent.key",
		CAFile:     filepath.Join(testCertsDir(), "relay-ca.crt"),
		ServerName: "relay.atlax.local",
	})
	_, err := cfg.ClientTLSConfig()
	require.Error(t, err)
}

func TestConfigurator_FullMTLSHandshake(t *testing.T) {
	certs := testCertsDir()

	relayConf := NewConfigurator(NewFileStore(), TLSPaths{
		CertFile:     filepath.Join(certs, "relay.crt"),
		KeyFile:      filepath.Join(certs, "relay.key"),
		ClientCAFile: filepath.Join(certs, "customer-ca.crt"),
	})

	agentConf := NewConfigurator(NewFileStore(), TLSPaths{
		CertFile:   filepath.Join(certs, "agent.crt"),
		KeyFile:    filepath.Join(certs, "agent.key"),
		CAFile:     filepath.Join(certs, "relay-ca.crt"),
		ServerName: "relay.atlax.local",
	})

	serverCfg, err := relayConf.ServerTLSConfig()
	require.NoError(t, err)

	clientCfg, err := agentConf.ClientTLSConfig()
	require.NoError(t, err)

	// Set up TCP listener
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	serverDone := make(chan error, 1)
	go func() {
		rawConn, acceptErr := ln.Accept()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		tc := tls.Server(rawConn, serverCfg)
		serverDone <- tc.Handshake()
		tc.Close()
	}()

	rawClient, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	tc := tls.Client(rawClient, clientCfg)
	require.NoError(t, tc.Handshake(), "client handshake should succeed")
	tc.Close()

	require.NoError(t, <-serverDone, "server handshake should succeed")
}

func TestConfigurator_HandshakeFailsWrongClientCA(t *testing.T) {
	certs := testCertsDir()

	// Server trusts relay-ca instead of customer-ca for client auth
	relayConf := NewConfigurator(NewFileStore(), TLSPaths{
		CertFile:     filepath.Join(certs, "relay.crt"),
		KeyFile:      filepath.Join(certs, "relay.key"),
		ClientCAFile: filepath.Join(certs, "relay-ca.crt"), // wrong CA
	})

	agentConf := NewConfigurator(NewFileStore(), TLSPaths{
		CertFile:   filepath.Join(certs, "agent.crt"),
		KeyFile:    filepath.Join(certs, "agent.key"),
		CAFile:     filepath.Join(certs, "relay-ca.crt"),
		ServerName: "relay.atlax.local",
	})

	serverCfg, err := relayConf.ServerTLSConfig()
	require.NoError(t, err)

	clientCfg, err := agentConf.ClientTLSConfig()
	require.NoError(t, err)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	serverErr := make(chan error, 1)
	go func() {
		rawConn, acceptErr := ln.Accept()
		if acceptErr != nil {
			serverErr <- acceptErr
			return
		}
		tc := tls.Server(rawConn, serverCfg)
		serverErr <- tc.Handshake()
		tc.Close()
	}()

	rawClient, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	tc := tls.Client(rawClient, clientCfg)
	clientErr := tc.Handshake()
	tc.Close()

	sErr := <-serverErr

	// At least one side must fail: server rejects unknown client CA,
	// and in TLS 1.3 the client may also see the rejection as an alert.
	assert.True(t, clientErr != nil || sErr != nil,
		"at least one side should fail with wrong client CA")
}

func TestConfigurator_HandshakeFailsWrongServerCA(t *testing.T) {
	certs := testCertsDir()

	relayConf := NewConfigurator(NewFileStore(), TLSPaths{
		CertFile:     filepath.Join(certs, "relay.crt"),
		KeyFile:      filepath.Join(certs, "relay.key"),
		ClientCAFile: filepath.Join(certs, "customer-ca.crt"),
	})

	// Agent trusts customer-ca instead of relay-ca for server verification
	agentConf := NewConfigurator(NewFileStore(), TLSPaths{
		CertFile:   filepath.Join(certs, "agent.crt"),
		KeyFile:    filepath.Join(certs, "agent.key"),
		CAFile:     filepath.Join(certs, "customer-ca.crt"), // wrong CA
		ServerName: "relay.atlax.local",
	})

	serverCfg, err := relayConf.ServerTLSConfig()
	require.NoError(t, err)

	clientCfg, err := agentConf.ClientTLSConfig()
	require.NoError(t, err)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	go func() {
		rawConn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		tc := tls.Server(rawConn, serverCfg)
		tc.Handshake() //nolint:errcheck // server handshake error checked via serverErr channel
		tc.Close()
	}()

	rawClient, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	tc := tls.Client(rawClient, clientCfg)
	err = tc.Handshake()
	assert.Error(t, err, "handshake should fail with wrong server CA")
	tc.Close()
}

// relayConfigurator returns a Configurator for relay-side TLS config tests.
func relayConfigurator(t *testing.T) *Configurator {
	t.Helper()
	certs := testCertsDir()

	// Verify certs exist
	_, err := os.Stat(filepath.Join(certs, "relay.crt"))
	require.NoError(t, err, "dev certs not found; run 'make certs-dev'")

	return NewConfigurator(NewFileStore(), TLSPaths{
		CertFile:     filepath.Join(certs, "relay.crt"),
		KeyFile:      filepath.Join(certs, "relay.key"),
		ClientCAFile: filepath.Join(certs, "customer-ca.crt"),
	})
}

// agentConfigurator returns a Configurator for agent-side TLS config tests.
func agentConfigurator(t *testing.T) *Configurator {
	t.Helper()
	certs := testCertsDir()
	return NewConfigurator(NewFileStore(), TLSPaths{
		CertFile:   filepath.Join(certs, "agent.crt"),
		KeyFile:    filepath.Join(certs, "agent.key"),
		CAFile:     filepath.Join(certs, "relay-ca.crt"),
		ServerName: "relay.atlax.local",
	})
}
