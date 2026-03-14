package agent

import (
	"context"
	"time"
)

// Tunnel manages the set of local service mappings exposed through the relay.
type Tunnel interface {
	// Start begins accepting stream requests from the relay and forwarding
	// them to local services.
	Start(ctx context.Context) error

	// Stop gracefully closes all active streams and stops accepting new ones.
	Stop(ctx context.Context) error

	// Stats returns a snapshot of tunnel throughput and stream counters.
	Stats() TunnelStats
}

// TunnelConfig holds the mapping of local services and stream limits.
type TunnelConfig struct {
	LocalServices        []ServiceMapping
	MaxConcurrentStreams int
}

// ServiceMapping binds a local network address to a relay-side port for a
// given protocol.
type ServiceMapping struct {
	Name      string
	Protocol  string
	LocalAddr string
	RelayPort int
}

// TunnelStats is a point-in-time snapshot of tunnel activity counters.
type TunnelStats struct {
	ActiveStreams int
	TotalStreams  int64
	BytesIn       int64
	BytesOut      int64
	Uptime        time.Duration
}
