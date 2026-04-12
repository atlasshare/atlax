package config

import "time"

// RelayConfig is the top-level configuration for the atlax-relay binary.
type RelayConfig struct {
	Server    ServerConfig     `yaml:"server"`
	TLS       TLSPaths         `yaml:"tls"`
	Customers []CustomerConfig `yaml:"customers"`
	Logging   LogConfig        `yaml:"logging"`
	Metrics   MetricsConfig    `yaml:"metrics"`
}

// AgentConfig is the top-level configuration for the atlax-agent binary.
type AgentConfig struct {
	Relay    RelayConnection  `yaml:"relay"`
	TLS      TLSPaths         `yaml:"tls"`
	Services []ServiceMapping `yaml:"services"`
	Logging  LogConfig        `yaml:"logging"`
	Update   UpdateConfig     `yaml:"update"`
}

// ServerConfig holds network listener and limit settings for the relay.
type ServerConfig struct {
	ListenAddr          string        `yaml:"listen_addr"`
	AdminAddr           string        `yaml:"admin_addr"`
	AdminSocket         string        `yaml:"admin_socket"`
	AgentListenAddr     string        `yaml:"agent_listen_addr"`
	MaxAgents           int           `yaml:"max_agents"`
	MaxStreamsPerAgent  int           `yaml:"max_streams_per_agent"`
	IdleTimeout         time.Duration `yaml:"idle_timeout"`
	ShutdownGracePeriod time.Duration `yaml:"shutdown_grace_period"`
	// StorePath is the path to the sidecar JSON file that persists runtime
	// port mutations across process restarts. If empty, runtime mutations
	// are not persisted (existing behaviour).
	StorePath string `yaml:"store_path"`
}

// TLSPaths points to the PEM files needed for mTLS.
type TLSPaths struct {
	CertFile     string `yaml:"cert_file"`
	KeyFile      string `yaml:"key_file"`
	CAFile       string `yaml:"ca_file"`
	ClientCAFile string `yaml:"client_ca_file"`
}

// CustomerConfig defines per-customer port allocations and resource limits.
type CustomerConfig struct {
	ID               string          `yaml:"id"`
	Ports            []PortConfig    `yaml:"ports"`
	MaxConnections   int             `yaml:"max_connections"`
	MaxStreams       int             `yaml:"max_streams"`
	MaxBandwidthMbps int             `yaml:"max_bandwidth_mbps"`
	RateLimit        RateLimitConfig `yaml:"rate_limit"`
}

// RateLimitConfig controls per-customer rate limiting on client connections.
type RateLimitConfig struct {
	RequestsPerSecond float64 `yaml:"requests_per_second"` // 0 = no limit
	Burst             int     `yaml:"burst"`
}

// PortConfig maps a relay-side port to a named service for a customer.
type PortConfig struct {
	Port        int    `yaml:"port"`
	Service     string `yaml:"service"`
	Description string `yaml:"description"`
	ListenAddr  string `yaml:"listen_addr"` // default: 0.0.0.0 (all interfaces)
}

// RelayConnection holds the agent-side settings for connecting to a relay.
type RelayConnection struct {
	Addr                string        `yaml:"addr"`
	ServerName          string        `yaml:"server_name"`
	InsecureSkipVerify  bool          `yaml:"insecure_skip_verify"`
	ReconnectInterval   time.Duration `yaml:"reconnect_interval"`
	MaxReconnectBackoff time.Duration `yaml:"reconnect_max_backoff"`
	KeepaliveInterval   time.Duration `yaml:"keepalive_interval"`
	KeepaliveTimeout    time.Duration `yaml:"keepalive_timeout"`
}

// LogConfig controls structured logging output.
type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
	Output string `yaml:"output"`
}

// MetricsConfig controls the optional metrics exporter.
type MetricsConfig struct {
	Enabled    bool   `yaml:"enabled"`
	ListenAddr string `yaml:"listen_addr"`
}

// UpdateConfig controls the agent's automatic update checker.
type UpdateConfig struct {
	Enabled       bool          `yaml:"enabled"`
	CheckInterval time.Duration `yaml:"check_interval"`
	ManifestURL   string        `yaml:"manifest_url"`
	PublicKeyPath string        `yaml:"public_key_path"`
}

// ServiceMapping binds a local network address to a relay-side port.
type ServiceMapping struct {
	Name      string `yaml:"name"`
	Protocol  string `yaml:"protocol"`
	LocalAddr string `yaml:"local_addr"`
	RelayPort int    `yaml:"relay_port"`
}

// Loader supports YAML config files and environment variable overrides.
type Loader interface {
	// LoadRelayConfig reads and validates a relay configuration file.
	LoadRelayConfig(path string) (*RelayConfig, error)

	// LoadAgentConfig reads and validates an agent configuration file.
	LoadAgentConfig(path string) (*AgentConfig, error)
}
