package main

import (
	"context"
	"crypto/tls"
	"errors"
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
	validate := flag.Bool("validate", false, "parse and validate config file, then exit (0=valid, 1=invalid)")
	flag.Parse()

	// Load configuration
	loader := config.NewFileLoader()
	cfg, err := loader.LoadRelayConfig(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Build port index from customer config (validates port assignments)
	portIndex, err := config.BuildPortIndex(cfg.Customers)
	if err != nil {
		return fmt.Errorf("build port index: %w", err)
	}

	// Pre-flight chain cert validation. Catches the common footgun of
	// pointing cert_file at a bare leaf instead of a chain cert.
	if err := auth.ValidateChainCertFile(cfg.TLS.CertFile); err != nil {
		return fmt.Errorf("validate cert: %w", err)
	}

	// Dry-run mode: config parsed and validated. Exit cleanly.
	if *validate {
		fmt.Fprintf(os.Stderr, "config %s is valid (%d customers, %d ports)\n",
			*configPath, len(cfg.Customers), len(portIndex.Entries))
		return nil
	}

	// Initialize logger
	logger := initLogger(cfg.Logging)

	// Initialize audit emitter
	emitter := audit.NewSlogEmitter(logger, audit.DefaultBufferSize)
	defer emitter.Close()

	// Build server-side mTLS configuration
	store := auth.NewFileStore(auth.WithLogger(logger))
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

	// Load sidecar and merge runtime port additions into the port index.
	// Sidecar entries that conflict with relay.yaml are skipped (relay.yaml wins).
	var sidecarStore *relay.SidecarStore
	if cfg.Server.StorePath != "" {
		sidecarStore = relay.NewSidecarStore(cfg.Server.StorePath)
		sidecarData, loadErr := sidecarStore.Load()
		if loadErr != nil {
			logger.Warn("relay: sidecar load error, starting without persisted runtime ports",
				"path", cfg.Server.StorePath, "error", loadErr)
			sidecarStore = nil
		} else {
			for _, sp := range sidecarData.Ports {
				if _, exists := portIndex.Entries[sp.Port]; exists {
					continue // relay.yaml entry takes precedence
				}
				portIndex.Entries[sp.Port] = config.PortIndexEntry{
					CustomerID: sp.CustomerID,
					Service:    sp.Service,
					ListenAddr: sp.ListenAddr,
					MaxStreams:  sp.MaxStreams,
				}
			}
			logger.Info("relay: sidecar loaded",
				"path", cfg.Server.StorePath,
				"ports", len(sidecarData.Ports))
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
	admin := relay.NewAdminServer(&relay.AdminConfig{
		Addr:           cfg.Server.AdminAddr,
		SocketPath:     cfg.Server.AdminSocket,
		Registry:       registry,
		Router:         router,
		ClientListener: clientListener,
		Logger:         logger,
		Emitter:        emitter,
		Store:          sidecarStore,
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

	// Start the cert rotation watcher. Polls the cert file for content
	// changes and calls tlsConfigurator.Reload() so new handshakes use
	// the updated cert without a process restart.
	go func() {
		watchErr := store.WatchForRotation(ctx,
			cfg.TLS.CertFile, cfg.TLS.KeyFile,
			func(cert tls.Certificate) {
				tlsConfigurator.Reload(&cert)
				logger.Info("relay: cert hot-reloaded")
			})
		if watchErr != nil && !errors.Is(watchErr, context.Canceled) {
			logger.Warn("relay: cert watcher exited", "error", watchErr)
		}
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
