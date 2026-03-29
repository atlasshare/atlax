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
