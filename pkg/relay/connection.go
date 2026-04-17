package relay

import (
	"net"
	"sync"
	"time"

	"github.com/atlasshare/atlax/pkg/protocol"
)

// LiveConnection is a concrete AgentConnection backed by a TLS connection
// and a multiplexed session.
type LiveConnection struct {
	customerID  string
	mux         *protocol.MuxSession
	remoteAddr  net.Addr
	connectedAt time.Time

	mu       sync.Mutex
	lastSeen time.Time

	// servMu guards the mutable service list and certificate expiry.
	// Kept separate from mu so high-frequency heartbeat updates do not
	// contend with service-list reads from the admin API.
	servMu       sync.RWMutex
	services     []string
	certNotAfter time.Time
}

// Compile-time interface check.
var _ AgentConnection = (*LiveConnection)(nil)

// NewLiveConnection wraps a MuxSession into an AgentConnection.
func NewLiveConnection(
	customerID string,
	mux *protocol.MuxSession,
	remoteAddr net.Addr,
) *LiveConnection {
	now := time.Now()
	return &LiveConnection{
		customerID:  customerID,
		mux:         mux,
		remoteAddr:  remoteAddr,
		connectedAt: now,
		lastSeen:    now,
	}
}

func (c *LiveConnection) CustomerID() string     { return c.customerID }
func (c *LiveConnection) Muxer() protocol.Muxer  { return c.mux }
func (c *LiveConnection) RemoteAddr() net.Addr   { return c.remoteAddr }
func (c *LiveConnection) ConnectedAt() time.Time { return c.connectedAt }

// LastSeen returns the time of the most recent heartbeat or data frame.
func (c *LiveConnection) LastSeen() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastSeen
}

// UpdateLastSeen refreshes the last-seen timestamp.
func (c *LiveConnection) UpdateLastSeen() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastSeen = time.Now()
}

// SetServices replaces the cached service name list advertised by the
// agent via CmdServiceList. A defensive copy is stored so later
// mutation of the caller's slice does not affect registry state.
func (c *LiveConnection) SetServices(services []string) {
	var copied []string
	if len(services) > 0 {
		copied = make([]string, len(services))
		copy(copied, services)
	}
	c.servMu.Lock()
	c.services = copied
	c.servMu.Unlock()
}

// Services returns a defensive copy of the service names advertised by
// the agent. The empty slice is returned when no CmdServiceList frame
// has been received (e.g., an older agent).
func (c *LiveConnection) Services() []string {
	c.servMu.RLock()
	defer c.servMu.RUnlock()
	if len(c.services) == 0 {
		return nil
	}
	out := make([]string, len(c.services))
	copy(out, c.services)
	return out
}

// SetCertNotAfter records the peer certificate expiry, captured once
// immediately after the TLS handshake. No lock is strictly required
// because the field is written exactly once during connection setup
// and read thereafter, but we take the write lock for symmetry and
// to keep the memory model explicit.
func (c *LiveConnection) SetCertNotAfter(t time.Time) {
	c.servMu.Lock()
	c.certNotAfter = t
	c.servMu.Unlock()
}

// CertNotAfter returns the NotAfter timestamp of the peer certificate
// captured at connection time. Returns the zero value if the
// certificate metadata was not populated (e.g., a test fixture).
func (c *LiveConnection) CertNotAfter() time.Time {
	c.servMu.RLock()
	defer c.servMu.RUnlock()
	return c.certNotAfter
}

// Close terminates the mux session and underlying transport.
func (c *LiveConnection) Close() error {
	return c.mux.Close()
}
