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
	router    *PortRouter
	logger    *slog.Logger
	listeners map[int]net.Listener

	mu sync.Mutex
}

// NewClientListener creates a client listener.
func NewClientListener(router *PortRouter, logger *slog.Logger) *ClientListener {
	return &ClientListener{
		router:    router,
		logger:    logger,
		listeners: make(map[int]net.Listener),
	}
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
	customerID, _, ok := cl.router.LookupPort(port)
	if !ok {
		cl.logger.Warn("relay: no mapping for client port",
			"port", port,
			"remote_addr", conn.RemoteAddr())
		conn.Close()
		return
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
