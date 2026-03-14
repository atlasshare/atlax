package relay

import (
	"context"
	"net"
)

// TrafficRouter directs inbound client connections to the correct agent stream
// based on customer identity and port allocation.
type TrafficRouter interface {
	// Route forwards a client connection to the agent owning the given
	// customer ID.
	Route(ctx context.Context, customerID string, clientConn net.Conn) error

	// AddPortMapping assigns a relay-side port to a specific service for the
	// given customer.
	AddPortMapping(customerID string, port int, service string) error

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
