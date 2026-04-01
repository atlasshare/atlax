package relay

import (
	"context"
	"net"
)

// TrafficRouter directs inbound client connections to the correct agent stream
// based on customer identity and port allocation.
type TrafficRouter interface {
	// Route forwards a client connection to the agent owning the given
	// customer ID. The port identifies which service to route to.
	Route(ctx context.Context, customerID string, clientConn net.Conn, port int) error

	// AddPortMapping assigns a relay-side port to a specific service for the
	// given customer. maxStreams of 0 means unlimited.
	AddPortMapping(customerID string, port int, service string, maxStreams int) error

	// RemovePortMapping releases a previously assigned port mapping.
	RemovePortMapping(customerID string, port int) error
}

// PortAllocation tracks the relay-side ports assigned to a single customer.
type PortAllocation struct {
	CustomerID string
	TCPPorts   []int
	UDPPorts   []int
	ServiceMap map[int]string
}

// TrafficRouterConfig holds settings for port range management.
type TrafficRouterConfig struct {
	PortRangeStart      int
	PortRangeEnd        int
	MaxPortsPerCustomer int
}
