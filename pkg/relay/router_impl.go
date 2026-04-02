package relay

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"

	"github.com/atlasshare/atlax/pkg/protocol"
)

// PortRouter routes inbound client connections to agent streams based
// on static port-to-customer-service mappings.
type PortRouter struct {
	registry AgentRegistry
	logger   *slog.Logger
	metrics  *Metrics // optional, nil = no metrics

	mu      sync.RWMutex
	portMap map[int]portEntry // port -> customer+service
}

// ErrStreamLimitExceeded is returned when a customer's stream limit is reached.
var ErrStreamLimitExceeded = fmt.Errorf("relay: stream limit exceeded")

type portEntry struct {
	customerID string
	service    string
	maxStreams int
}

// Compile-time interface check.
var _ TrafficRouter = (*PortRouter)(nil)

// SetMetrics attaches Prometheus metrics to the router.
func (r *PortRouter) SetMetrics(m *Metrics) { r.metrics = m }

// NewPortRouter creates a router with the given registry.
func NewPortRouter(registry AgentRegistry, logger *slog.Logger) *PortRouter {
	return &PortRouter{
		registry: registry,
		logger:   logger,
		portMap:  make(map[int]portEntry),
	}
}

// AddPortMapping assigns a relay-side port to a customer's service.
func (r *PortRouter) AddPortMapping(customerID string, port int, service string, maxStreams int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.portMap[port] = portEntry{
		customerID: customerID,
		service:    service,
		maxStreams: maxStreams,
	}
	return nil
}

// RemovePortMapping releases a previously assigned port mapping.
func (r *PortRouter) RemovePortMapping(customerID string, port int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.portMap[port]
	if !ok || entry.customerID != customerID {
		return fmt.Errorf("relay: router: no mapping for port %d customer %s",
			port, customerID)
	}
	delete(r.portMap, port)
	return nil
}

// Route forwards a client connection to the agent owning the given
// port. The port determines which customer and service to route to.
func (r *PortRouter) Route(
	ctx context.Context,
	customerID string,
	clientConn net.Conn,
	port int,
) error {
	r.mu.RLock()
	entry, ok := r.portMap[port]
	r.mu.RUnlock()

	var service string
	var maxStreams int
	if ok {
		service = entry.service
		maxStreams = entry.maxStreams
	}

	conn, err := r.registry.Lookup(ctx, customerID)
	if err != nil {
		return fmt.Errorf("relay: route: %w", err)
	}

	// Enforce per-customer stream limit before opening.
	if maxStreams > 0 && conn.Muxer().NumStreams() >= maxStreams {
		if r.metrics != nil {
			r.metrics.ClientRejected(customerID, "stream_limit")
		}
		return fmt.Errorf("relay: route: %w: customer %s has %d/%d streams",
			ErrStreamLimitExceeded, customerID, conn.Muxer().NumStreams(), maxStreams)
	}

	mux, ok := conn.Muxer().(*protocol.MuxSession)
	if !ok {
		return fmt.Errorf("relay: route: muxer type assertion failed")
	}

	// Open stream with service name as payload so agent can route.
	var payload []byte
	if service != "" {
		payload = []byte(service)
	}

	stream, err := mux.OpenStreamWithPayload(ctx, payload)
	if err != nil {
		return fmt.Errorf("relay: route: open stream: %w", err)
	}

	if r.metrics != nil {
		r.metrics.StreamOpened(customerID)
		defer r.metrics.StreamClosed(customerID)
	}

	// Bidirectional copy between client TCP and mux stream.
	return r.copyBidirectional(ctx, clientConn, stream)
}

// LookupPort returns the customer ID and service for a given port.
func (r *PortRouter) LookupPort(port int) (customerID, service string, ok bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, found := r.portMap[port]
	if !found {
		return "", "", false
	}
	return entry.customerID, entry.service, true
}

// copyBidirectional copies data between a client TCP connection and a
// mux stream until one side closes or ctx is canceled.
func (r *PortRouter) copyBidirectional(
	ctx context.Context,
	clientConn net.Conn,
	stream protocol.Stream,
) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 2)

	go func() {
		_, err := io.Copy(clientConn, stream)
		cancel()
		errCh <- err
	}()

	go func() {
		_, err := io.Copy(stream, clientConn)
		cancel()
		errCh <- err
	}()

	// Close both on context cancel to unblock io.Copy.
	go func() {
		<-ctx.Done()
		clientConn.Close()
		// Use Reset to force-unblock stream.Read (Close only half-closes)
		if ss, ok := stream.(*protocol.StreamSession); ok {
			ss.Reset(0)
		} else {
			stream.Close()
		}
	}()

	firstErr := <-errCh
	<-errCh

	if firstErr != nil && ctx.Err() != nil {
		return nil // context canceled, not a real error
	}
	return firstErr
}
