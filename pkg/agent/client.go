package agent

import (
	"context"
	"crypto/tls"
	"time"
)

// Client manages the agent's persistent connection to a relay server.
type Client interface {
	// Connect establishes the initial TLS connection and mTLS handshake.
	Connect(ctx context.Context) error

	// Reconnect tears down the current connection (if any) and establishes a
	// new one using exponential backoff with jitter.
	Reconnect(ctx context.Context) error

	// Close gracefully terminates the connection and all open streams.
	Close() error

	// Status returns a snapshot of the client's current connection state.
	Status() ClientStatus
}

// ClientConfig holds the settings for connecting an agent to a relay.
type ClientConfig struct {
	RelayAddr            string
	TLSConfig            *tls.Config
	ReconnectBackoff     time.Duration
	MaxReconnectAttempts int
	HeartbeatInterval    time.Duration
	HeartbeatTimeout     time.Duration

	// Services lists the service names this agent forwards (derived from
	// the agent config's services map keys). It is sent to the relay
	// immediately after the mux handshake as a CmdServiceList frame so
	// that the relay admin API can expose per-agent service inventory.
	// Empty slice skips the send (no empty-payload frame on the wire).
	Services []string
}

// ClientStatus is a point-in-time snapshot of the agent client state.
type ClientStatus struct {
	Connected     bool
	RelayAddr     string
	CustomerID    string
	ConnectedAt   time.Time
	StreamCount   int
	LastHeartbeat time.Time
}
