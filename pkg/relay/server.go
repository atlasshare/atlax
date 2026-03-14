package relay

import (
	"context"
	"crypto/tls"
	"net"
	"time"
)

// Server is the relay-side listener that accepts agent TLS connections and
// client traffic.
type Server interface {
	// Start begins accepting connections on the configured addresses.
	Start(ctx context.Context) error

	// Stop gracefully shuts down the server, draining active connections.
	Stop(ctx context.Context) error

	// Addr returns the address the client-facing listener is bound to.
	Addr() net.Addr
}

// ServerConfig holds the settings for a relay server instance.
type ServerConfig struct {
	ListenAddr          string
	TLSConfig           *tls.Config
	AgentListenAddr     string
	MaxAgents           int
	MaxStreamsPerAgent  int
	IdleTimeout         time.Duration
	ShutdownGracePeriod time.Duration
}
