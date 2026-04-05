package relay

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/atlasshare/atlax/pkg/audit"
	"github.com/atlasshare/atlax/pkg/auth"
	"github.com/atlasshare/atlax/pkg/protocol"
)

// AgentListener accepts inbound agent TLS connections, performs mTLS
// handshake, extracts identity, creates MuxSessions, and registers
// agents in the registry.
type AgentListener struct {
	addr               string
	tlsConfig          *tls.Config
	registry           AgentRegistry
	emitter            audit.Emitter
	logger             *slog.Logger
	maxAgents          int
	maxStreamsPerAgent int
}

// AgentListenerConfig holds settings for the agent listener.
type AgentListenerConfig struct {
	Addr               string
	TLSConfig          *tls.Config
	Registry           AgentRegistry
	Emitter            audit.Emitter
	Logger             *slog.Logger
	MaxAgents          int
	MaxStreamsPerAgent int
}

// NewAgentListener creates an agent listener.
func NewAgentListener(cfg AgentListenerConfig) *AgentListener { //nolint:gocritic // hugeParam: cfg is mutated for defaults, pass by value is intentional
	if cfg.MaxAgents <= 0 {
		cfg.MaxAgents = 1000
	}
	if cfg.MaxStreamsPerAgent <= 0 {
		cfg.MaxStreamsPerAgent = 256
	}
	return &AgentListener{
		addr:               cfg.Addr,
		tlsConfig:          cfg.TLSConfig,
		registry:           cfg.Registry,
		emitter:            cfg.Emitter,
		logger:             cfg.Logger,
		maxAgents:          cfg.MaxAgents,
		maxStreamsPerAgent: cfg.MaxStreamsPerAgent,
	}
}

// Start creates a TLS listener and begins accepting agent connections.
// Blocks until ctx is canceled.
func (l *AgentListener) Start(ctx context.Context) error {
	ln, err := tls.Listen("tcp", l.addr, l.tlsConfig)
	if err != nil {
		return fmt.Errorf("relay: agent listener: %w", err)
	}
	return l.StartWithListener(ctx, ln)
}

// StartWithListener begins accepting agent TLS connections on the given
// listener. Use this instead of Start when the listener was created
// externally (e.g., inherited via fd passing for zero-downtime restart).
// Blocks until ctx is canceled.
func (l *AgentListener) StartWithListener(ctx context.Context, ln net.Listener) error {
	l.logger.Info("relay: agent listener started", "addr", ln.Addr())

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			select {
			case <-ctx.Done():
				return nil // clean shutdown
			default:
			}
			l.logger.Warn("relay: accept error", "error", acceptErr)
			continue
		}

		go l.handleConnection(ctx, conn)
	}
}

// handleConnection processes a single agent connection: handshake,
// identity extraction, mux creation, and registration.
func (l *AgentListener) handleConnection(ctx context.Context, conn net.Conn) {
	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		conn.Close()
		return
	}

	// Force handshake to access peer certificates.
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		l.logger.Warn("relay: agent handshake failed", "error", err)
		l.emitAudit(ctx, audit.ActionAuthFailure, conn.RemoteAddr().String(), "")
		conn.Close()
		return
	}

	identity, err := auth.ExtractIdentity(tlsConn)
	if err != nil {
		l.logger.Warn("relay: identity extraction failed", "error", err)
		l.emitAudit(ctx, audit.ActionAuthFailure, conn.RemoteAddr().String(), "")
		conn.Close()
		return
	}

	customerID := identity.CustomerID
	if customerID == "" {
		l.logger.Warn("relay: non-customer cert connected",
			"relay_id", identity.RelayID)
		conn.Close()
		return
	}

	l.emitAudit(ctx, audit.ActionAuthSuccess, conn.RemoteAddr().String(), customerID)

	mux := protocol.NewMuxSession(conn, protocol.RoleRelay, protocol.MuxConfig{
		MaxConcurrentStreams: l.maxStreamsPerAgent,
		InitialStreamWindow:  262144,
		ConnectionWindow:     1048576,
		PingInterval:         30 * time.Second,
		PingTimeout:          5 * time.Second,
		IdleTimeout:          60 * time.Second,
	})

	liveConn := NewLiveConnection(customerID, mux, conn.RemoteAddr())

	if regErr := l.registry.Register(ctx, customerID, liveConn); regErr != nil {
		l.logger.Error("relay: agent registration failed",
			"customer_id", customerID, "error", regErr)
		mux.Close()
		conn.Close()
		return
	}

	l.emitAudit(ctx, audit.ActionAgentConnected, conn.RemoteAddr().String(), customerID)
	l.logger.Info("relay: agent connected",
		"customer_id", customerID,
		"remote_addr", conn.RemoteAddr())
}

func (l *AgentListener) emitAudit(
	ctx context.Context,
	action audit.Action,
	target string,
	customerID string,
) {
	//nolint:errcheck // best-effort audit
	l.emitter.Emit(ctx, audit.Event{
		Action:     action,
		Actor:      "relay",
		Target:     target,
		Timestamp:  time.Now(),
		CustomerID: customerID,
	})
}
