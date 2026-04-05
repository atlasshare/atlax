package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/atlasshare/atlax/pkg/audit"
	"github.com/atlasshare/atlax/pkg/auth"
	"github.com/atlasshare/atlax/pkg/config"
	"github.com/atlasshare/atlax/pkg/relay"
)

func main() {
	if err := run(); err != nil {
		slog.Error("relay exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "relay.yaml", "path to relay configuration file")
	flag.Parse()

	// Load configuration
	loader := config.NewFileLoader()
	cfg, err := loader.LoadRelayConfig(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Initialize logger
	logger := initLogger(cfg.Logging)

	// Initialize audit emitter
	emitter := audit.NewSlogEmitter(logger, audit.DefaultBufferSize)
	defer emitter.Close()

	// Build port index from customer config
	portIndex, err := config.BuildPortIndex(cfg.Customers)
	if err != nil {
		return fmt.Errorf("build port index: %w", err)
	}

	// Build server-side mTLS configuration
	store := auth.NewFileStore()
	tlsConfigurator := auth.NewConfigurator(store, auth.TLSPaths{
		CertFile:     cfg.TLS.CertFile,
		KeyFile:      cfg.TLS.KeyFile,
		CAFile:       cfg.TLS.CAFile,
		ClientCAFile: cfg.TLS.ClientCAFile,
	})

	tlsCfg, err := tlsConfigurator.ServerTLSConfig()
	if err != nil {
		return fmt.Errorf("create TLS config: %w", err)
	}

	// Create metrics
	metrics := relay.NewMetrics("atlax", prometheus.DefaultRegisterer)

	// Create components
	registry := relay.NewMemoryRegistry(logger)
	registry.SetMetrics(metrics)

	agentListener := relay.NewAgentListener(relay.AgentListenerConfig{
		Addr:      cfg.Server.ListenAddr,
		TLSConfig: tlsCfg,
		Registry:  registry,
		Emitter:   emitter,
		Logger:    logger,
		MaxAgents: cfg.Server.MaxAgents,
	})

	router := relay.NewPortRouter(registry, logger)
	router.SetMetrics(metrics)
	clientListener := relay.NewClientListener(relay.ClientListenerConfig{Router: router, Logger: logger})

	// Configure per-customer rate limiters from YAML config.
	for _, c := range cfg.Customers {
		if c.RateLimit.RequestsPerSecond > 0 {
			clientListener.SetRateLimiter(c.ID, c.RateLimit.RequestsPerSecond, c.RateLimit.Burst)
		}
	}

	server := relay.NewRelay(relay.ServerDeps{
		AgentListener:  agentListener,
		ClientListener: clientListener,
		Router:         router,
		Registry:       registry,
		PortIndex:      portIndex,
		Logger:         logger,
	})

	// Start server
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	//nolint:errcheck // best-effort audit
	emitter.Emit(ctx, audit.Event{
		Action:    audit.ActionAgentConnected,
		Actor:     "relay",
		Target:    cfg.Server.ListenAddr,
		Timestamp: time.Now(),
	})

	// Start admin server (health check + metrics + CRUD API)
	admin := relay.NewAdminServer(relay.AdminConfig{
		Addr:           cfg.Server.AdminAddr,
		SocketPath:     cfg.Server.AdminSocket,
		Registry:       registry,
		Router:         router,
		ClientListener: clientListener,
		Logger:         logger,
	})
	go func() {
		if adminErr := admin.Start(ctx); adminErr != nil {
			logger.Error("admin server error", "error", adminErr)
		}
	}()

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- server.Start(ctx)
	}()

	logger.Info("relay started",
		"listen_addr", cfg.Server.ListenAddr,
		"customers", len(cfg.Customers),
		"ports", len(portIndex.Entries))

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		logger.Info("received signal, shutting down", "signal", sig)
	case err := <-serverDone:
		if err != nil {
			logger.Error("server stopped with error", "error", err)
		}
	}

	// Graceful shutdown
	cancel()

	gracePeriod := cfg.Server.ShutdownGracePeriod
	if gracePeriod <= 0 {
		gracePeriod = 30 * time.Second
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(
		context.Background(), gracePeriod)
	defer shutdownCancel()

	if stopErr := server.Stop(shutdownCtx); stopErr != nil {
		logger.Error("server stop error", "error", stopErr)
	}

	logger.Info("relay stopped")
	return nil
}

func initLogger(cfg config.LogConfig) *slog.Logger {
	level := slog.LevelInfo
	switch cfg.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	if cfg.Format == "text" {
		handler = slog.NewTextHandler(os.Stderr, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	}

	return slog.New(handler)
}
