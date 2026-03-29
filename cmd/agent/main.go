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

	"github.com/atlasshare/atlax/internal/audit"
	"github.com/atlasshare/atlax/internal/config"
	"github.com/atlasshare/atlax/pkg/agent"
	"github.com/atlasshare/atlax/pkg/auth"
)

func main() {
	if err := run(); err != nil {
		slog.Error("agent exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "agent.yaml", "path to agent configuration file")
	flag.Parse()

	// Load configuration
	loader := config.NewFileLoader()
	cfg, err := loader.LoadAgentConfig(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Initialize logger
	logger := initLogger(cfg.Logging)

	// Initialize audit emitter
	emitter := audit.NewSlogEmitter(logger, audit.DefaultBufferSize)
	defer emitter.Close()

	// Build mTLS configuration
	store := auth.NewFileStore()
	tlsConfigurator := auth.NewConfigurator(store, auth.TLSPaths{
		CertFile:   cfg.TLS.CertFile,
		KeyFile:    cfg.TLS.KeyFile,
		CAFile:     cfg.TLS.CAFile,
		ServerName: cfg.Relay.ServerName,
	})

	tlsCfg, err := tlsConfigurator.ClientTLSConfig(auth.WithSessionCache(64))
	if err != nil {
		return fmt.Errorf("create TLS config: %w", err)
	}

	// Create agent client
	clientCfg := agent.ClientConfig{
		RelayAddr:            cfg.Relay.Addr,
		TLSConfig:            tlsCfg,
		ReconnectBackoff:     cfg.Relay.ReconnectInterval,
		MaxReconnectAttempts: 10,
		HeartbeatInterval:    cfg.Relay.KeepaliveInterval,
		HeartbeatTimeout:     cfg.Relay.KeepaliveTimeout,
	}

	client := agent.NewClient(clientCfg, logger)

	// Connect to relay
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if connectErr := client.Connect(ctx); connectErr != nil {
		return fmt.Errorf("connect to relay: %w", connectErr)
	}
	defer client.Close()

	//nolint:errcheck // best-effort audit
	emitter.Emit(ctx, audit.Event{
		Action:    audit.ActionAgentConnected,
		Actor:     "agent",
		Target:    cfg.Relay.Addr,
		Timestamp: time.Now(),
	})

	// Build service mappings
	services := make([]agent.ServiceMapping, len(cfg.Services))
	for i, s := range cfg.Services {
		services[i] = agent.ServiceMapping{
			Name:      s.Name,
			Protocol:  s.Protocol,
			LocalAddr: s.LocalAddr,
			RelayPort: s.RelayPort,
		}
	}

	// Create and start tunnel
	tunnel := agent.NewTunnel(client, agent.ServiceForwarderConfig{
		DialTimeout: 5 * time.Second,
		BufferSize:  32 * 1024,
	}, services, logger)

	tunnelDone := make(chan error, 1)
	go func() {
		tunnelDone <- tunnel.Start(ctx)
	}()

	logger.Info("agent started",
		"relay", cfg.Relay.Addr,
		"services", len(services))

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		logger.Info("received signal, shutting down", "signal", sig)
	case err := <-tunnelDone:
		if err != nil {
			logger.Error("tunnel stopped with error", "error", err)
		}
	}

	// Graceful shutdown
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(
		context.Background(), 10*time.Second)
	defer shutdownCancel()

	if stopErr := tunnel.Stop(shutdownCtx); stopErr != nil {
		logger.Error("tunnel stop error", "error", stopErr)
	}

	//nolint:errcheck // best-effort audit
	emitter.Emit(context.Background(), audit.Event{
		Action:    audit.ActionAgentDisconnected,
		Actor:     "agent",
		Target:    cfg.Relay.Addr,
		Timestamp: time.Now(),
	})

	logger.Info("agent stopped")
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
