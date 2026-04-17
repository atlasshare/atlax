package agent

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/atlasshare/atlax/pkg/protocol"
)

// Dialer abstracts network dialing for testability. Production code uses
// TLSDialer; tests inject in-memory pipes.
type Dialer interface {
	DialContext(ctx context.Context, addr string) (net.Conn, error)
}

// TLSDialer dials a TLS connection using the provided config.
type TLSDialer struct {
	Config *tls.Config
}

// DialContext dials the addr over TLS with context support.
func (d *TLSDialer) DialContext(ctx context.Context, addr string) (net.Conn, error) {
	dialer := &tls.Dialer{Config: d.Config}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("agent: dial: %w", err)
	}
	return conn, nil
}

// TunnelClient manages the agent's persistent connection to a relay.
type TunnelClient struct {
	config  ClientConfig
	dialer  Dialer
	logger  *slog.Logger
	backoff BackoffConfig

	mu            sync.Mutex
	mux           *protocol.MuxSession
	conn          net.Conn
	status        ClientStatus
	heartbeatStop context.CancelFunc
	closed        bool

	// disconnectCh is written to when the heartbeat detects a dead
	// connection. The tunnel supervision loop reads this to trigger
	// reconnection.
	disconnectCh chan struct{}
}

// Compile-time interface check.
var _ Client = (*TunnelClient)(nil)

// NewClient creates a TunnelClient. Use WithDialer to override the default
// TLS dialer (useful for testing).
func NewClient(cfg ClientConfig, logger *slog.Logger, opts ...ClientOption) *TunnelClient { //nolint:gocritic // hugeParam: cfg carries tls.Config and services slice; passing by value keeps construction site explicit.
	c := &TunnelClient{
		config:       cfg,
		logger:       logger,
		backoff:      DefaultBackoffConfig(),
		status:       ClientStatus{RelayAddr: cfg.RelayAddr},
		disconnectCh: make(chan struct{}, 1),
	}

	if cfg.ReconnectBackoff > 0 {
		c.backoff.InitialInterval = cfg.ReconnectBackoff
	}

	for _, o := range opts {
		o(c)
	}

	if c.dialer == nil {
		c.dialer = &TLSDialer{Config: cfg.TLSConfig}
	}

	return c
}

// ClientOption configures a TunnelClient.
type ClientOption func(*TunnelClient)

// WithDialer overrides the default TLS dialer.
func WithDialer(d Dialer) ClientOption {
	return func(c *TunnelClient) { c.dialer = d }
}

// WithBackoffConfig overrides the default backoff configuration.
func WithBackoffConfig(b BackoffConfig) ClientOption {
	return func(c *TunnelClient) { c.backoff = b }
}

// Connect establishes the mTLS connection and starts the heartbeat.
func (c *TunnelClient) Connect(ctx context.Context) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return fmt.Errorf("agent: connect: client is closed")
	}
	c.mu.Unlock()

	conn, err := c.dialer.DialContext(ctx, c.config.RelayAddr)
	if err != nil {
		return fmt.Errorf("agent: connect: %w", err)
	}

	mux := protocol.NewMuxSession(conn, protocol.RoleAgent, protocol.MuxConfig{
		MaxConcurrentStreams: 50,
		InitialStreamWindow:  262144,
		ConnectionWindow:     1048576,
		PingInterval:         c.config.HeartbeatInterval,
		PingTimeout:          c.config.HeartbeatTimeout,
		IdleTimeout:          60 * time.Second,
	})

	// Advertise the local service inventory to the relay so that the
	// admin API can surface per-agent service metadata. Skip the send
	// when we have nothing to advertise to avoid emitting empty frames
	// on the wire.
	if len(c.config.Services) > 0 {
		if sendErr := mux.SendServiceList(c.config.Services); sendErr != nil {
			c.logger.Warn("agent: send service list failed",
				"error", sendErr)
		}
	}

	c.mu.Lock()
	c.conn = conn
	c.mux = mux
	c.status.Connected = true
	c.status.ConnectedAt = time.Now()
	c.mu.Unlock()

	// Start heartbeat goroutine
	hbCtx, hbCancel := context.WithCancel(context.Background())
	c.mu.Lock()
	c.heartbeatStop = hbCancel
	c.mu.Unlock()
	go c.runHeartbeat(hbCtx)

	c.logger.Info("agent: connected to relay", "addr", c.config.RelayAddr)
	return nil
}

// Reconnect tears down the current connection and re-establishes with
// exponential backoff and jitter.
func (c *TunnelClient) Reconnect(ctx context.Context) error {
	c.teardown()

	maxAttempts := c.config.MaxReconnectAttempts
	if maxAttempts <= 0 {
		maxAttempts = 10
	}

	for attempt := range maxAttempts {
		delay := ComputeBackoff(c.backoff, attempt)
		c.logger.Info("agent: reconnecting",
			"attempt", attempt+1,
			"delay", delay)

		select {
		case <-ctx.Done():
			return fmt.Errorf("agent: reconnect: %w", ctx.Err())
		case <-time.After(delay):
		}

		if err := c.Connect(ctx); err != nil {
			c.logger.Warn("agent: reconnect attempt failed",
				"attempt", attempt+1, "error", err)
			continue
		}
		return nil
	}

	return fmt.Errorf("agent: reconnect: exhausted %d attempts", maxAttempts)
}

// Close gracefully shuts down the connection.
func (c *TunnelClient) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()

	c.teardown()
	c.logger.Info("agent: client closed")
	return nil
}

// Status returns a snapshot of the client's current connection state.
func (c *TunnelClient) Status() ClientStatus {
	c.mu.Lock()
	defer c.mu.Unlock()

	s := c.status
	if c.mux != nil {
		s.StreamCount = c.mux.NumStreams()
	}
	return s
}

// Mux returns the current MuxSession. Returns nil if not connected.
func (c *TunnelClient) Mux() *protocol.MuxSession {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mux
}

// DisconnectCh returns a channel that receives when the heartbeat
// detects a dead connection. Used by the tunnel supervision loop.
func (c *TunnelClient) DisconnectCh() <-chan struct{} {
	return c.disconnectCh
}

// teardown stops the heartbeat and closes the current connection.
func (c *TunnelClient) teardown() {
	c.mu.Lock()
	if c.heartbeatStop != nil {
		c.heartbeatStop()
		c.heartbeatStop = nil
	}
	mux := c.mux
	conn := c.conn
	c.mux = nil
	c.conn = nil
	c.status.Connected = false
	c.mu.Unlock()

	if mux != nil {
		//nolint:errcheck // best-effort GoAway during teardown; connection may already be dead
		mux.GoAway(0)
		mux.Close()
	}
	if conn != nil {
		conn.Close()
	}
}

// runHeartbeat sends periodic PING and triggers reconnect on timeout.
func (c *TunnelClient) runHeartbeat(ctx context.Context) {
	interval := c.config.HeartbeatInterval
	if interval <= 0 {
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.mu.Lock()
			mux := c.mux
			c.mu.Unlock()

			if mux == nil {
				return
			}

			pingCtx, pingCancel := context.WithTimeout(ctx, c.config.HeartbeatTimeout)
			_, err := mux.Ping(pingCtx)
			pingCancel()

			if err != nil {
				c.logger.Warn("agent: heartbeat failed", "error", err)
				select {
				case c.disconnectCh <- struct{}{}:
				default:
				}
				return
			}

			c.mu.Lock()
			c.status.LastHeartbeat = time.Now()
			c.mu.Unlock()
		}
	}
}
