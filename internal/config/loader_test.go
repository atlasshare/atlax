package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validAgentYAML = `
relay:
  addr: "relay.test:8443"
  server_name: "relay.test"
  keepalive_interval: 30s
  keepalive_timeout: 10s
tls:
  cert_file: /etc/atlax/agent.crt
  key_file: /etc/atlax/agent.key
  ca_file: /etc/atlax/ca.crt
services:
  - name: samba
    local_addr: "127.0.0.1:445"
    protocol: tcp
logging:
  level: info
  format: json
`

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestLoadAgentConfig_Valid(t *testing.T) {
	path := writeConfig(t, validAgentYAML)
	loader := NewFileLoader()

	cfg, err := loader.LoadAgentConfig(path)
	require.NoError(t, err)
	assert.Equal(t, "relay.test:8443", cfg.Relay.Addr)
	assert.Equal(t, "relay.test", cfg.Relay.ServerName)
	assert.Equal(t, "/etc/atlax/agent.crt", cfg.TLS.CertFile)
	assert.Equal(t, "/etc/atlax/agent.key", cfg.TLS.KeyFile)
	assert.Equal(t, "/etc/atlax/ca.crt", cfg.TLS.CAFile)
	assert.Len(t, cfg.Services, 1)
	assert.Equal(t, "samba", cfg.Services[0].Name)
	assert.Equal(t, "127.0.0.1:445", cfg.Services[0].LocalAddr)
	assert.Equal(t, "info", cfg.Logging.Level)
}

func TestLoadAgentConfig_MissingFile(t *testing.T) {
	loader := NewFileLoader()
	_, err := loader.LoadAgentConfig("/nonexistent/agent.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "config: read")
}

func TestLoadAgentConfig_InvalidYAML(t *testing.T) {
	path := writeConfig(t, "{{invalid yaml")
	loader := NewFileLoader()
	_, err := loader.LoadAgentConfig(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "config: parse")
}

func TestLoadAgentConfig_MissingRelayAddr(t *testing.T) {
	yaml := `
tls:
  cert_file: /cert
  key_file: /key
  ca_file: /ca
`
	path := writeConfig(t, yaml)
	loader := NewFileLoader()
	_, err := loader.LoadAgentConfig(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "relay.addr is required")
}

func TestLoadAgentConfig_MissingTLSCert(t *testing.T) {
	yaml := `
relay:
  addr: "relay:8443"
tls:
  key_file: /key
  ca_file: /ca
`
	path := writeConfig(t, yaml)
	loader := NewFileLoader()
	_, err := loader.LoadAgentConfig(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tls.cert_file is required")
}

func TestLoadAgentConfig_MissingTLSKey(t *testing.T) {
	yaml := `
relay:
  addr: "relay:8443"
tls:
  cert_file: /cert
  ca_file: /ca
`
	path := writeConfig(t, yaml)
	loader := NewFileLoader()
	_, err := loader.LoadAgentConfig(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tls.key_file is required")
}

func TestLoadAgentConfig_MissingTLSCA(t *testing.T) {
	yaml := `
relay:
  addr: "relay:8443"
tls:
  cert_file: /cert
  key_file: /key
`
	path := writeConfig(t, yaml)
	loader := NewFileLoader()
	_, err := loader.LoadAgentConfig(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tls.ca_file is required")
}

func TestLoadAgentConfig_EnvOverrides(t *testing.T) {
	path := writeConfig(t, validAgentYAML)

	t.Setenv("ATLAX_RELAY_ADDR", "override.relay:9443")
	t.Setenv("ATLAX_TLS_CERT", "/override/cert.crt")
	t.Setenv("ATLAX_TLS_KEY", "/override/cert.key")
	t.Setenv("ATLAX_TLS_CA", "/override/ca.crt")
	t.Setenv("ATLAX_LOG_LEVEL", "debug")

	loader := NewFileLoader()
	cfg, err := loader.LoadAgentConfig(path)
	require.NoError(t, err)

	assert.Equal(t, "override.relay:9443", cfg.Relay.Addr)
	assert.Equal(t, "/override/cert.crt", cfg.TLS.CertFile)
	assert.Equal(t, "/override/cert.key", cfg.TLS.KeyFile)
	assert.Equal(t, "/override/ca.crt", cfg.TLS.CAFile)
	assert.Equal(t, "debug", cfg.Logging.Level)
}

func TestLoadAgentConfig_EmptyServicesList(t *testing.T) {
	yaml := `
relay:
  addr: "relay:8443"
tls:
  cert_file: /cert
  key_file: /key
  ca_file: /ca
services: []
`
	path := writeConfig(t, yaml)
	loader := NewFileLoader()
	cfg, err := loader.LoadAgentConfig(path)
	require.NoError(t, err)
	assert.Empty(t, cfg.Services)
}

func TestLoadRelayConfig_MissingFile(t *testing.T) {
	loader := NewFileLoader()
	_, err := loader.LoadRelayConfig("/nonexistent/relay.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "config: read")
}

func TestLoadRelayConfig_InvalidYAML(t *testing.T) {
	path := writeConfig(t, "{{bad")
	loader := NewFileLoader()
	_, err := loader.LoadRelayConfig(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "config: parse")
}

func TestDefaultAgentConfig(t *testing.T) {
	cfg := DefaultAgentConfig()
	assert.Equal(t, "info", cfg.Logging.Level)
	assert.Equal(t, "json", cfg.Logging.Format)
}

func TestLoadRelayConfig_Valid(t *testing.T) {
	yaml := `
server:
  listen_addr: ":8443"
  agent_listen_addr: ":8444"
tls:
  cert_file: /relay.crt
  key_file: /relay.key
  client_ca_file: /customer-ca.crt
`
	path := writeConfig(t, yaml)
	loader := NewFileLoader()
	cfg, err := loader.LoadRelayConfig(path)
	require.NoError(t, err)
	assert.Equal(t, ":8443", cfg.Server.ListenAddr)
	assert.Equal(t, "/relay.crt", cfg.TLS.CertFile)
}
