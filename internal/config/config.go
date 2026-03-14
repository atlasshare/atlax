package config

import "time"

// RelayConfig is the top-level configuration for the atlax-relay binary.
type RelayConfig struct {
	Server    ServerConfig
	TLS       TLSPaths
	Customers []CustomerConfig
	Logging   LogConfig
	Metrics   MetricsConfig
}

// AgentConfig is the top-level configuration for the atlax-agent binary.
type AgentConfig struct {
	Relay    RelayConnection
	TLS      TLSPaths
	Services []ServiceMapping
	Logging  LogConfig
	Update   UpdateConfig
}

// ServerConfig holds network listener and limit settings for the relay.
type ServerConfig struct {
	ListenAddr          string
	AgentListenAddr     string
	MaxAgents           int
	MaxStreamsPerAgent  int
	IdleTimeout         time.Duration
	ShutdownGracePeriod time.Duration
}

// TLSPaths points to the PEM files needed for mTLS.
type TLSPaths struct {
	CertFile     string
	KeyFile      string
	CAFile       string
	ClientCAFile string
}

// CustomerConfig defines per-customer resource limits and port allowances.
type CustomerConfig struct {
	CustomerID       string
	AllowedPorts     []int
	MaxStreams       int
	MaxBandwidthMbps int
}

// RelayConnection holds the agent-side settings for connecting to a relay.
type RelayConnection struct {
	Addr       string
	ServerName string
	// InsecureSkipVerify disables TLS verification. For development use only.
	InsecureSkipVerify bool
}

// LogConfig controls structured logging output.
type LogConfig struct {
	Level  string
	Format string
	Output string
}

// MetricsConfig controls the optional metrics exporter.
type MetricsConfig struct {
	Enabled    bool
	ListenAddr string
}

// UpdateConfig controls the agent's automatic update checker.
type UpdateConfig struct {
	Enabled       bool
	CheckInterval time.Duration
	ManifestURL   string
	PublicKeyPath string
}

// ServiceMapping binds a local network address to a relay-side port.
type ServiceMapping struct {
	Name      string
	Protocol  string
	LocalAddr string
	RelayPort int
}

// ConfigLoader supports YAML config files and environment variable overrides.
type ConfigLoader interface {
	// LoadRelayConfig reads and validates a relay configuration file.
	LoadRelayConfig(path string) (*RelayConfig, error)

	// LoadAgentConfig reads and validates an agent configuration file.
	LoadAgentConfig(path string) (*AgentConfig, error)
}
