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

const validRelayYAML = `
server:
  listen_addr: ":8443"
  admin_addr: ":9090"
  agent_listen_addr: ":8444"
  max_agents: 1000
  max_streams_per_agent: 100
  idle_timeout: 300s
  shutdown_grace_period: 30s
tls:
  cert_file: /relay.crt
  key_file: /relay.key
  ca_file: /root-ca.crt
  client_ca_file: /customer-ca.crt
customers:
  - id: "customer-dev-001"
    ports:
      - port: 8080
        service: "http"
        description: "HTTP web service"
      - port: 8081
        service: "smb"
        description: "SMB file sharing"
logging:
  level: info
  format: json
`

func TestLoadRelayConfig_Valid(t *testing.T) {
	path := writeConfig(t, validRelayYAML)
	loader := NewFileLoader()
	cfg, err := loader.LoadRelayConfig(path)
	require.NoError(t, err)
	assert.Equal(t, ":8443", cfg.Server.ListenAddr)
	assert.Equal(t, ":9090", cfg.Server.AdminAddr)
	assert.Equal(t, "/relay.crt", cfg.TLS.CertFile)
	assert.Equal(t, "/customer-ca.crt", cfg.TLS.ClientCAFile)
	require.Len(t, cfg.Customers, 1)
	assert.Equal(t, "customer-dev-001", cfg.Customers[0].ID)
	require.Len(t, cfg.Customers[0].Ports, 2)
	assert.Equal(t, 8080, cfg.Customers[0].Ports[0].Port)
	assert.Equal(t, "http", cfg.Customers[0].Ports[0].Service)
}

func TestLoadRelayConfig_MissingListenAddr(t *testing.T) {
	yml := `
tls:
  cert_file: /cert
  key_file: /key
  client_ca_file: /ca
customers:
  - id: "c1"
`
	path := writeConfig(t, yml)
	loader := NewFileLoader()
	_, err := loader.LoadRelayConfig(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "server.listen_addr is required")
}

func TestLoadRelayConfig_MissingClientCA(t *testing.T) {
	yml := `
server:
  listen_addr: ":8443"
tls:
  cert_file: /cert
  key_file: /key
customers:
  - id: "c1"
`
	path := writeConfig(t, yml)
	loader := NewFileLoader()
	_, err := loader.LoadRelayConfig(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tls.client_ca_file is required")
}

func TestLoadRelayConfig_NoCustomers(t *testing.T) {
	yml := `
server:
  listen_addr: ":8443"
tls:
  cert_file: /cert
  key_file: /key
  client_ca_file: /ca
customers: []
`
	path := writeConfig(t, yml)
	loader := NewFileLoader()
	_, err := loader.LoadRelayConfig(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one customer")
}

func TestLoadRelayConfig_CustomerMissingID(t *testing.T) {
	yml := `
server:
  listen_addr: ":8443"
tls:
  cert_file: /cert
  key_file: /key
  client_ca_file: /ca
customers:
  - ports:
      - port: 8080
        service: "http"
`
	path := writeConfig(t, yml)
	loader := NewFileLoader()
	_, err := loader.LoadRelayConfig(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "customers[0].id is required")
}

func TestLoadRelayConfig_EnvOverrides(t *testing.T) {
	path := writeConfig(t, validRelayYAML)

	t.Setenv("ATLAX_LISTEN_ADDR", ":9443")
	t.Setenv("ATLAX_TLS_CLIENT_CA", "/override/ca.crt")
	t.Setenv("ATLAX_LOG_LEVEL", "debug")

	loader := NewFileLoader()
	cfg, err := loader.LoadRelayConfig(path)
	require.NoError(t, err)
	assert.Equal(t, ":9443", cfg.Server.ListenAddr)
	assert.Equal(t, "/override/ca.crt", cfg.TLS.ClientCAFile)
	assert.Equal(t, "debug", cfg.Logging.Level)
}

func TestBuildPortIndex(t *testing.T) {
	customers := []CustomerConfig{
		{
			ID: "customer-001",
			Ports: []PortConfig{
				{Port: 8080, Service: "http"},
				{Port: 8081, Service: "smb"},
			},
		},
		{
			ID: "customer-002",
			Ports: []PortConfig{
				{Port: 9080, Service: "http"},
			},
		},
	}

	idx, err := BuildPortIndex(customers)
	require.NoError(t, err)
	assert.Len(t, idx.Entries, 3)
	assert.Equal(t, "customer-001", idx.Entries[8080].CustomerID)
	assert.Equal(t, "http", idx.Entries[8080].Service)
	assert.Equal(t, "customer-002", idx.Entries[9080].CustomerID)
}

func TestBuildPortIndex_DuplicatePort(t *testing.T) {
	customers := []CustomerConfig{
		{ID: "c1", Ports: []PortConfig{{Port: 8080, Service: "http"}}},
		{ID: "c2", Ports: []PortConfig{{Port: 8080, Service: "web"}}},
	}

	_, err := BuildPortIndex(customers)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "port 8080 assigned to both")
}

func TestBuildPortIndex_ListenAddrDefault(t *testing.T) {
	customers := []CustomerConfig{
		{
			ID:    "c1",
			Ports: []PortConfig{{Port: 8080, Service: "http"}},
		},
	}
	idx, err := BuildPortIndex(customers)
	require.NoError(t, err)
	assert.Equal(t, "0.0.0.0", idx.Entries[8080].ListenAddr)
}

func TestBuildPortIndex_ListenAddrCustom(t *testing.T) {
	customers := []CustomerConfig{
		{
			ID: "c1",
			Ports: []PortConfig{
				{Port: 8080, Service: "http", ListenAddr: "127.0.0.1"},
			},
		},
	}
	idx, err := BuildPortIndex(customers)
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1", idx.Entries[8080].ListenAddr)
}
