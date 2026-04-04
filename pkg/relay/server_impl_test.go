package relay

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/atlasshare/atlax/pkg/audit"
	"github.com/atlasshare/atlax/pkg/config"
)

func TestRelay_StopWithoutStart(t *testing.T) {
	logger := slog.Default()
	emitter := audit.NewSlogEmitter(logger, 16)
	defer emitter.Close()

	reg := NewMemoryRegistry(logger)
	router := NewPortRouter(reg, logger)
	cl := NewClientListener(ClientListenerConfig{Router: router, Logger: logger})

	server := NewRelay(ServerDeps{
		AgentListener: NewAgentListener(AgentListenerConfig{
			Addr:     "127.0.0.1:0",
			Registry: reg,
			Emitter:  emitter,
			Logger:   logger,
		}),
		ClientListener: cl,
		Router:         router,
		Registry:       reg,
		PortIndex:      &config.PortIndex{Entries: make(map[int]config.PortIndexEntry)},
		Logger:         logger,
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// Stop without Start should not panic or block
	require.NoError(t, server.Stop(ctx))
}

func TestRelay_StartRegistersPortMappings(t *testing.T) {
	skipIfNoCerts(t)

	var auditBuf bytes.Buffer
	auditLogger := slog.New(slog.NewJSONHandler(&auditBuf, nil))
	emitter := audit.NewSlogEmitter(auditLogger, 256)

	reg := NewMemoryRegistry(slog.Default())
	router := NewPortRouter(reg, slog.Default())
	cl := NewClientListener(ClientListenerConfig{Router: router, Logger: slog.Default()})

	portIndex := &config.PortIndex{
		Entries: map[int]config.PortIndexEntry{
			18080: {CustomerID: "customer-dev-001", Service: "http"},
		},
	}

	server := NewRelay(ServerDeps{
		AgentListener: NewAgentListener(AgentListenerConfig{
			Addr:      "127.0.0.1:0",
			TLSConfig: relayTLSConfig(t),
			Registry:  reg,
			Emitter:   emitter,
			Logger:    slog.Default(),
		}),
		ClientListener: cl,
		Router:         router,
		Registry:       reg,
		PortIndex:      portIndex,
		Logger:         slog.Default(),
	})

	ctx, cancel := context.WithCancel(context.Background())

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- server.Start(ctx)
	}()

	// Give server time to register port mappings and start listeners
	time.Sleep(200 * time.Millisecond)

	// Verify port mapping was registered
	cid, svc, ok := router.LookupPort(18080)
	assert.True(t, ok)
	assert.Equal(t, "customer-dev-001", cid)
	assert.Equal(t, "http", svc)

	// Shutdown
	cancel()
	<-serverDone
	server.Stop(context.Background()) //nolint:errcheck // test cleanup
	emitter.Close()
}

func TestRelay_GracefulShutdownSendsGoAway(t *testing.T) {
	reg := NewMemoryRegistry(slog.Default())
	router := NewPortRouter(reg, slog.Default())
	cl := NewClientListener(ClientListenerConfig{Router: router, Logger: slog.Default()})

	// Register a mock agent
	conn, agentMux := testConnectionPair("customer-001")
	defer agentMux.Close()
	require.NoError(t, reg.Register(context.Background(), "customer-001", conn))

	emitter := audit.NewSlogEmitter(slog.Default(), 16)
	defer emitter.Close()

	server := NewRelay(ServerDeps{
		AgentListener: NewAgentListener(AgentListenerConfig{
			Addr:     "127.0.0.1:0",
			Registry: reg,
			Emitter:  emitter,
			Logger:   slog.Default(),
		}),
		ClientListener: cl,
		Router:         router,
		Registry:       reg,
		PortIndex:      &config.PortIndex{Entries: make(map[int]config.PortIndexEntry)},
		Logger:         slog.Default(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Stop sends GOAWAY to all agents and unregisters them
	require.NoError(t, server.Stop(ctx))

	// Agent should be unregistered
	_, err := reg.Lookup(context.Background(), "customer-001")
	assert.ErrorIs(t, err, ErrAgentNotFound)
}

func TestRelay_Addr_ReturnsNil(t *testing.T) {
	reg := NewMemoryRegistry(slog.Default())
	router := NewPortRouter(reg, slog.Default())
	cl := NewClientListener(ClientListenerConfig{Router: router, Logger: slog.Default()})

	r := NewRelay(ServerDeps{
		Registry:       reg,
		Router:         router,
		ClientListener: cl,
		Logger:         slog.Default(),
	})

	assert.Nil(t, r.Addr(), "Addr returns nil (no accessor exposed)")
}
