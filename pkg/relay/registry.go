package relay

import (
	"context"
	"net"
	"time"

	"github.com/atlasshare/atlax/pkg/protocol"
)

// AgentRegistry is an enterprise extension point. Community edition uses
// in-memory implementation. Enterprise edition may use Redis, etcd, or other
// distributed stores.
type AgentRegistry interface {
	// Register records a newly authenticated agent connection.
	Register(ctx context.Context, customerID string, conn AgentConnection) error

	// Unregister removes an agent connection from the registry.
	Unregister(ctx context.Context, customerID string) error

	// Lookup returns the active connection for the given customer, if any.
	Lookup(ctx context.Context, customerID string) (AgentConnection, error)

	// Heartbeat updates the last-seen timestamp for the given customer.
	Heartbeat(ctx context.Context, customerID string) error

	// ListConnectedAgents returns information about all currently registered
	// agent connections.
	ListConnectedAgents(ctx context.Context) ([]AgentInfo, error)
}

// AgentConnection represents a live, authenticated agent connection with its
// underlying multiplexer.
type AgentConnection interface {
	// CustomerID returns the authenticated customer identifier.
	CustomerID() string

	// Muxer returns the stream multiplexer for this connection.
	Muxer() protocol.Muxer

	// RemoteAddr returns the network address of the connected agent.
	RemoteAddr() net.Addr

	// ConnectedAt returns the time the agent first connected.
	ConnectedAt() time.Time

	// LastSeen returns the time of the most recent heartbeat or data frame.
	LastSeen() time.Time

	// Close terminates the agent connection.
	Close() error
}

// AgentInfo holds read-only metadata about a connected agent, suitable for
// listing or monitoring endpoints.
type AgentInfo struct {
	CustomerID   string    `json:"customer_id"`
	RemoteAddr   string    `json:"remote_addr"`
	ConnectedAt  time.Time `json:"connected_at"`
	LastSeen     time.Time `json:"last_seen"`
	StreamCount  int       `json:"stream_count"`
	Services     []string  `json:"services"`
	CertNotAfter time.Time `json:"cert_not_after"`
}
