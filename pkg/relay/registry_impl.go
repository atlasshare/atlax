package relay

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// ErrAgentNotFound is returned when a customer ID has no active connection.
var ErrAgentNotFound = fmt.Errorf("relay: agent not found")

// ErrConnectionLimitExceeded is returned when a customer has reached
// their maximum allowed connections.
var ErrConnectionLimitExceeded = fmt.Errorf("relay: connection limit exceeded")

// MemoryRegistry is the community edition AgentRegistry backed by an
// in-memory map. Enterprise editions may use Redis, etcd, or other
// distributed stores.
type MemoryRegistry struct {
	mu             sync.RWMutex
	agents         map[string]*LiveConnection
	customerLimits map[string]int // customerID -> max connections (0 = default 1)
	logger         *slog.Logger
}

// Compile-time interface check.
var _ AgentRegistry = (*MemoryRegistry)(nil)

// NewMemoryRegistry creates an empty agent registry.
func NewMemoryRegistry(logger *slog.Logger) *MemoryRegistry {
	return &MemoryRegistry{
		agents:         make(map[string]*LiveConnection),
		customerLimits: make(map[string]int),
		logger:         logger,
	}
}

// SetCustomerLimit configures the maximum connections for a customer.
// Default is 1 (replace on reconnect). Set > 1 for multi-agent.
func (r *MemoryRegistry) SetCustomerLimit(customerID string, maxConns int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.customerLimits[customerID] = maxConns
}

// Register records a newly authenticated agent connection. With default
// limit of 1, an existing connection is replaced with GOAWAY.
func (r *MemoryRegistry) Register(
	_ context.Context,
	customerID string,
	conn AgentConnection,
) error {
	live, ok := conn.(*LiveConnection)
	if !ok {
		return fmt.Errorf("relay: register: expected *LiveConnection")
	}

	r.mu.Lock()
	limit := r.customerLimits[customerID]
	if limit <= 0 {
		limit = 1 // default: one connection per customer
	}

	old, exists := r.agents[customerID]

	// With limit of 1, replace existing connection.
	// With limit > 1, reject if at capacity (multi-agent support is
	// deferred -- current map only holds one connection per customer).
	if exists && limit == 1 {
		r.agents[customerID] = live
		r.mu.Unlock()
		r.logger.Info("relay: replacing existing agent connection",
			"customer_id", customerID)
		old.Muxer().GoAway(0) //nolint:errcheck // best-effort GoAway
		old.Close()
	} else {
		r.agents[customerID] = live
		r.mu.Unlock()
	}

	r.logger.Info("relay: agent registered",
		"customer_id", customerID,
		"remote_addr", conn.RemoteAddr())
	return nil
}

// Unregister removes an agent connection and closes it.
func (r *MemoryRegistry) Unregister(_ context.Context, customerID string) error {
	r.mu.Lock()
	conn, ok := r.agents[customerID]
	if ok {
		delete(r.agents, customerID)
	}
	r.mu.Unlock()

	if ok {
		conn.Close()
		r.logger.Info("relay: agent unregistered",
			"customer_id", customerID)
	}
	return nil
}

// Lookup returns the active connection for the given customer.
func (r *MemoryRegistry) Lookup(
	_ context.Context,
	customerID string,
) (AgentConnection, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	conn, ok := r.agents[customerID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrAgentNotFound, customerID)
	}
	return conn, nil
}

// Heartbeat updates the last-seen timestamp for the given customer.
func (r *MemoryRegistry) Heartbeat(_ context.Context, customerID string) error {
	r.mu.RLock()
	conn, ok := r.agents[customerID]
	r.mu.RUnlock()

	if !ok {
		return fmt.Errorf("%w: %s", ErrAgentNotFound, customerID)
	}
	conn.UpdateLastSeen()
	return nil
}

// ListConnectedAgents returns metadata about all registered agents.
func (r *MemoryRegistry) ListConnectedAgents(
	_ context.Context,
) ([]AgentInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	infos := make([]AgentInfo, 0, len(r.agents))
	for _, conn := range r.agents {
		infos = append(infos, AgentInfo{
			CustomerID:  conn.CustomerID(),
			RemoteAddr:  conn.RemoteAddr().String(),
			ConnectedAt: conn.ConnectedAt(),
			LastSeen:    conn.LastSeen(),
			StreamCount: conn.Muxer().NumStreams(),
		})
	}
	return infos, nil
}
