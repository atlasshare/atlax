package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// FileLoader loads configuration from YAML files with environment
// variable overrides.
type FileLoader struct{}

// Compile-time interface check.
var _ Loader = (*FileLoader)(nil)

// NewFileLoader returns a new FileLoader.
func NewFileLoader() *FileLoader {
	return &FileLoader{}
}

// LoadAgentConfig reads and validates an agent configuration file.
func (l *FileLoader) LoadAgentConfig(path string) (*AgentConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	var cfg AgentConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}

	l.applyAgentEnvOverrides(&cfg)

	if err := l.validateAgentConfig(&cfg); err != nil {
		return nil, fmt.Errorf("config: validate: %w", err)
	}

	return &cfg, nil
}

// LoadRelayConfig reads and validates a relay configuration file.
func (l *FileLoader) LoadRelayConfig(path string) (*RelayConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	var cfg RelayConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}

	l.applyRelayEnvOverrides(&cfg)

	if err := l.validateRelayConfig(&cfg); err != nil {
		return nil, fmt.Errorf("config: validate: %w", err)
	}

	return &cfg, nil
}

// applyAgentEnvOverrides applies environment variable overrides to the
// agent config. Env vars take precedence over YAML values.
func (l *FileLoader) applyAgentEnvOverrides(cfg *AgentConfig) {
	if v := os.Getenv("ATLAX_RELAY_ADDR"); v != "" {
		cfg.Relay.Addr = v
	}
	if v := os.Getenv("ATLAX_TLS_CERT"); v != "" {
		cfg.TLS.CertFile = v
	}
	if v := os.Getenv("ATLAX_TLS_KEY"); v != "" {
		cfg.TLS.KeyFile = v
	}
	if v := os.Getenv("ATLAX_TLS_CA"); v != "" {
		cfg.TLS.CAFile = v
	}
	if v := os.Getenv("ATLAX_LOG_LEVEL"); v != "" {
		cfg.Logging.Level = v
	}
}

// validateAgentConfig checks that required fields are present.
func (l *FileLoader) validateAgentConfig(cfg *AgentConfig) error {
	if cfg.Relay.Addr == "" {
		return fmt.Errorf("relay.addr is required")
	}
	if cfg.TLS.CertFile == "" {
		return fmt.Errorf("tls.cert_file is required")
	}
	if cfg.TLS.KeyFile == "" {
		return fmt.Errorf("tls.key_file is required")
	}
	if cfg.TLS.CAFile == "" {
		return fmt.Errorf("tls.ca_file is required")
	}
	return nil
}

// applyRelayEnvOverrides applies environment variable overrides to the
// relay config.
func (l *FileLoader) applyRelayEnvOverrides(cfg *RelayConfig) {
	if v := os.Getenv("ATLAX_LISTEN_ADDR"); v != "" {
		cfg.Server.ListenAddr = v
	}
	if v := os.Getenv("ATLAX_AGENT_LISTEN_ADDR"); v != "" {
		cfg.Server.AgentListenAddr = v
	}
	if v := os.Getenv("ATLAX_TLS_CERT"); v != "" {
		cfg.TLS.CertFile = v
	}
	if v := os.Getenv("ATLAX_TLS_KEY"); v != "" {
		cfg.TLS.KeyFile = v
	}
	if v := os.Getenv("ATLAX_TLS_CA"); v != "" {
		cfg.TLS.CAFile = v
	}
	if v := os.Getenv("ATLAX_TLS_CLIENT_CA"); v != "" {
		cfg.TLS.ClientCAFile = v
	}
	if v := os.Getenv("ATLAX_LOG_LEVEL"); v != "" {
		cfg.Logging.Level = v
	}
}

// validateRelayConfig checks that required fields are present.
func (l *FileLoader) validateRelayConfig(cfg *RelayConfig) error {
	if cfg.Server.ListenAddr == "" {
		return fmt.Errorf("server.listen_addr is required")
	}
	if cfg.TLS.CertFile == "" {
		return fmt.Errorf("tls.cert_file is required")
	}
	if cfg.TLS.KeyFile == "" {
		return fmt.Errorf("tls.key_file is required")
	}
	if cfg.TLS.ClientCAFile == "" {
		return fmt.Errorf("tls.client_ca_file is required")
	}
	if len(cfg.Customers) == 0 {
		return fmt.Errorf("at least one customer must be configured")
	}
	for i, c := range cfg.Customers {
		if c.ID == "" {
			return fmt.Errorf("customers[%d].id is required", i)
		}
	}
	return nil
}

// PortIndex is a mapping from relay-side port to customer ID and service
// name, built from the relay configuration.
type PortIndex struct {
	Entries map[int]PortIndexEntry
}

// PortIndexEntry holds the customer, service, and limits for a single port.
type PortIndexEntry struct {
	CustomerID string
	Service    string
	MaxStreams int
}

// BuildPortIndex creates a port-to-customer-service index from the relay
// config. Returns an error if any port is assigned to multiple customers.
func BuildPortIndex(customers []CustomerConfig) (*PortIndex, error) {
	idx := &PortIndex{Entries: make(map[int]PortIndexEntry)}
	for _, c := range customers {
		for _, p := range c.Ports {
			if existing, ok := idx.Entries[p.Port]; ok {
				return nil, fmt.Errorf(
					"port %d assigned to both %s and %s",
					p.Port, existing.CustomerID, c.ID)
			}
			idx.Entries[p.Port] = PortIndexEntry{
				CustomerID: c.ID,
				Service:    p.Service,
				MaxStreams: c.MaxStreams,
			}
		}
	}
	return idx, nil
}

// DefaultAgentConfig returns an AgentConfig with sensible defaults
// applied for fields not specified in the YAML.
func DefaultAgentConfig() AgentConfig {
	return AgentConfig{
		Logging: LogConfig{
			Level:  "info",
			Format: "json",
		},
		Update: UpdateConfig{
			CheckInterval: 6 * time.Hour,
		},
	}
}
