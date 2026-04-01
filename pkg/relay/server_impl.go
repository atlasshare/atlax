package relay

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/atlasshare/atlax/internal/config"
)

// Relay orchestrates the agent listener, client listener, registry,
// and router. It satisfies the Server interface.
type Relay struct {
	agentListener  *AgentListener
	clientListener *ClientListener
	router         *PortRouter
	registry       *MemoryRegistry
	portIndex      *config.PortIndex
	logger         *slog.Logger
}

// Compile-time interface check.
var _ Server = (*Relay)(nil)

// ServerDeps holds all dependencies for the relay server.
type ServerDeps struct {
	AgentListener  *AgentListener
	ClientListener *ClientListener
	Router         *PortRouter
	Registry       *MemoryRegistry
	PortIndex      *config.PortIndex
	Logger         *slog.Logger
}

// NewRelay creates a relay server from pre-built components.
func NewRelay(cfg ServerDeps) *Relay {
	return &Relay{
		agentListener:  cfg.AgentListener,
		clientListener: cfg.ClientListener,
		router:         cfg.Router,
		registry:       cfg.Registry,
		portIndex:      cfg.PortIndex,
		logger:         cfg.Logger,
	}
}

// Start begins accepting agent and client connections. Blocks until ctx
// is canceled.
func (s *Relay) Start(ctx context.Context) error {
	// Register port mappings from config.
	for port, entry := range s.portIndex.Entries {
		if err := s.router.AddPortMapping(
			entry.CustomerID, port, entry.Service, entry.MaxStreams,
		); err != nil {
			return fmt.Errorf("relay: add port mapping: %w", err)
		}
	}

	// Start per-port client listeners.
	for port, entry := range s.portIndex.Entries {
		addr := fmt.Sprintf("%s:%d", entry.ListenAddr, port)
		go func(a string, p int) {
			if err := s.clientListener.StartPort(ctx, a, p); err != nil {
				s.logger.Error("relay: client listener failed",
					"port", p, "error", err)
			}
		}(addr, port)
	}

	// Start agent listener (blocks until ctx canceled).
	s.logger.Info("relay: server started")
	return s.agentListener.Start(ctx)
}

// Stop gracefully shuts down. Sends GOAWAY to all agents and waits
// for drain.
func (s *Relay) Stop(ctx context.Context) error {
	s.logger.Info("relay: stopping server")

	// Send GOAWAY to all registered agents.
	agents, err := s.registry.ListConnectedAgents(ctx)
	if err != nil {
		s.logger.Warn("relay: list agents for shutdown", "error", err)
	}

	for _, info := range agents {
		conn, lookupErr := s.registry.Lookup(ctx, info.CustomerID)
		if lookupErr != nil {
			continue
		}
		conn.Muxer().GoAway(0) //nolint:errcheck // best-effort GoAway during shutdown
	}

	// Stop client listeners.
	s.clientListener.Stop()

	// Unregister all agents (closes mux sessions).
	for _, info := range agents {
		s.registry.Unregister(ctx, info.CustomerID) //nolint:errcheck // best-effort cleanup
	}

	s.logger.Info("relay: server stopped")
	return nil
}

// Addr returns the agent listener's address. May be nil if not started.
func (s *Relay) Addr() net.Addr {
	// The agent listener binds internally; we don't expose its addr
	// directly. Return nil for now; Phase 5 may add an accessor.
	return nil
}
