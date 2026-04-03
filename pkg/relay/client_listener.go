package relay

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
)

// ClientListener accepts plain TCP connections on per-customer dedicated
// ports and routes them through the TrafficRouter to the correct agent.
type ClientListener struct {
	router       *PortRouter
	logger       *slog.Logger
	rateLimiters map[string]*IPRateLimiter // customerID -> limiter
	listeners    map[int]net.Listener

	mu sync.Mutex
}

// ClientListenerConfig holds settings for the client listener.
type ClientListenerConfig struct {
	Router *PortRouter
	Logger *slog.Logger
}

// NewClientListener creates a client listener.
func NewClientListener(cfg ClientListenerConfig) *ClientListener {
	return &ClientListener{
		router:       cfg.Router,
		logger:       cfg.Logger,
		rateLimiters: make(map[string]*IPRateLimiter),
		listeners:    make(map[int]net.Listener),
	}
}

// SetRateLimiter configures a per-customer rate limiter.
func (cl *ClientListener) SetRateLimiter(customerID string, rps float64, burst int) {
	if rps <= 0 {
		return
	}
	cl.mu.Lock()
	defer cl.mu.Unlock()
	cl.rateLimiters[customerID] = NewIPRateLimiter(rps, burst)
}

// StartPort opens a TCP listener on the given address and routes all
// accepted connections to the customer/service identified by that port.
func (cl *ClientListener) StartPort(
	ctx context.Context,
	addr string,
	port int,
) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("relay: client listener: port %d: %w", port, err)
	}

	cl.mu.Lock()
	cl.listeners[port] = ln
	cl.mu.Unlock()

	cl.logger.Info("relay: client listener started",
		"addr", ln.Addr(), "port", port)

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			cl.logger.Warn("relay: client accept error",
				"port", port, "error", acceptErr)
			continue
		}

		go cl.handleClient(ctx, conn, port)
	}
}

// handleClient routes a single client connection through the port router.
func (cl *ClientListener) handleClient(
	ctx context.Context,
	conn net.Conn,
	port int,
) {
	// Look up customer first so we can tag metrics.
	customerID, _, ok := cl.router.LookupPort(port)
	if !ok {
		cl.logger.Warn("relay: no mapping for client port",
			"port", port,
			"remote_addr", conn.RemoteAddr())
		conn.Close()
		return
	}

	// Rate limit by source IP (per-customer limiter).
	cl.mu.Lock()
	rl := cl.rateLimiters[customerID]
	cl.mu.Unlock()

	if rl != nil {
		ip, _, splitErr := net.SplitHostPort(conn.RemoteAddr().String())
		if splitErr != nil {
			ip = conn.RemoteAddr().String()
		}
		if !rl.Allow(ip) {
			if cl.router.metrics != nil {
				cl.router.metrics.ClientRejected(customerID, "rate_limited")
			}
			cl.logger.Warn("relay: rate limited",
				"port", port,
				"customer_id", customerID,
				"remote_addr", conn.RemoteAddr())
			conn.Close()
			return
		}
	}

	if err := cl.router.Route(ctx, customerID, conn, port); err != nil {
		cl.logger.Warn("relay: route failed",
			"port", port,
			"customer_id", customerID,
			"error", err)
	}
}

// Stop closes all active client listeners.
func (cl *ClientListener) Stop() {
	cl.mu.Lock()
	defer cl.mu.Unlock()

	for port, ln := range cl.listeners {
		ln.Close()
		cl.logger.Info("relay: client listener stopped", "port", port)
	}
	cl.listeners = make(map[int]net.Listener)

	for _, rl := range cl.rateLimiters {
		rl.Stop()
	}
	cl.rateLimiters = make(map[string]*IPRateLimiter)
}

// Addr returns the listening address for the given port, or nil.
func (cl *ClientListener) Addr(port int) net.Addr {
	cl.mu.Lock()
	defer cl.mu.Unlock()

	if ln, ok := cl.listeners[port]; ok {
		return ln.Addr()
	}
	return nil
}
