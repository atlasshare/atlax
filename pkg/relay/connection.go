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

// Close terminates the mux session and underlying transport.
func (c *LiveConnection) Close() error {
	return c.mux.Close()
}
