package relay

import (
	"context"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/atlasshare/atlax/pkg/protocol"
)

// TestCrossTenantIsolation verifies that a client connecting on
// customer-A's port reaches agent-A and cannot reach agent-B.
func TestCrossTenantIsolation(t *testing.T) {
	cfg := testMuxConfig()
	reg := NewMemoryRegistry(slog.Default())
	router := NewPortRouter(reg, slog.Default())

	// Customer A: port 19001, service "echo-a"
	router.AddPortMapping("customer-a", 19001, "echo-a", 0) //nolint:errcheck // test setup
	// Customer B: port 19002, service "echo-b"
	router.AddPortMapping("customer-b", 19002, "echo-b", 0) //nolint:errcheck // test setup

	// Create mux pairs for both customers
	relayConnA, agentConnA := net.Pipe()
	relayMuxA := protocol.NewMuxSession(relayConnA, protocol.RoleRelay, cfg)
	agentMuxA := protocol.NewMuxSession(agentConnA, protocol.RoleAgent, cfg)
	defer relayMuxA.Close()
	defer agentMuxA.Close()

	relayConnB, agentConnB := net.Pipe()
	relayMuxB := protocol.NewMuxSession(relayConnB, protocol.RoleRelay, cfg)
	agentMuxB := protocol.NewMuxSession(agentConnB, protocol.RoleAgent, cfg)
	defer relayMuxB.Close()
	defer agentMuxB.Close()

	// Register both agents
	liveA := NewLiveConnection("customer-a", relayMuxA, relayConnA.RemoteAddr())
	liveB := NewLiveConnection("customer-b", relayMuxB, relayConnB.RemoteAddr())
	require.NoError(t, reg.Register(context.Background(), "customer-a", liveA))
	require.NoError(t, reg.Register(context.Background(), "customer-b", liveB))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Agent A: accept streams and echo with "A:" prefix
	go func() {
		for {
			stream, err := agentMuxA.AcceptStream(ctx)
			if err != nil {
				return
			}
			go func(s protocol.Stream) {
				buf := make([]byte, 256)
				n, _ := s.Read(buf)
				s.Write(append([]byte("A:"), buf[:n]...)) //nolint:errcheck // test
				// Do not close -- let the client side close first
			}(stream)
		}
	}()

	// Agent B: accept streams and echo with "B:" prefix
	go func() {
		for {
			stream, err := agentMuxB.AcceptStream(ctx)
			if err != nil {
				return
			}
			go func(s protocol.Stream) {
				buf := make([]byte, 256)
				n, _ := s.Read(buf)
				s.Write(append([]byte("B:"), buf[:n]...)) //nolint:errcheck // test
			}(stream)
		}
	}()

	// --- Test 1: Client on port-A reaches agent-A ---
	clientA, relayEndA := net.Pipe()
	routeDoneA := make(chan error, 1)
	go func() {
		routeDoneA <- router.Route(ctx, "customer-a", relayEndA, 19001)
	}()

	_, err := clientA.Write([]byte("hello"))
	require.NoError(t, err)

	// Allow time for data to traverse: client -> relay -> mux -> agent -> echo -> mux -> relay -> client
	time.Sleep(200 * time.Millisecond)

	buf := make([]byte, 64)
	n, err := clientA.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "A:hello", string(buf[:n]), "port-A traffic must reach agent-A")
	clientA.Close()
	<-routeDoneA

	// --- Test 2: Client on port-B reaches agent-B ---
	clientB, relayEndB := net.Pipe()
	routeDoneB := make(chan error, 1)
	go func() {
		routeDoneB <- router.Route(ctx, "customer-b", relayEndB, 19002)
	}()

	_, err = clientB.Write([]byte("world"))
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	n, err = clientB.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "B:world", string(buf[:n]), "port-B traffic must reach agent-B")
	clientB.Close()
	<-routeDoneB

	// --- Test 3: If agent-A disconnects, port-A returns error ---
	reg.Unregister(ctx, "customer-a") //nolint:errcheck // test
	clientC, relayEndC := net.Pipe()
	defer clientC.Close()

	err = router.Route(ctx, "customer-a", relayEndC, 19001)
	assert.Error(t, err, "port-A should fail when agent-A is disconnected")
	assert.Contains(t, err.Error(), "agent not found")
}

// TestCrossTenantIsolation_PortACannotReachAgentB verifies the
// structural invariant: even if we try to route with the wrong
// customer ID, the port mapping prevents cross-tenant access.
func TestCrossTenantIsolation_PortACannotReachAgentB(t *testing.T) {
	reg := NewMemoryRegistry(slog.Default())
	router := NewPortRouter(reg, slog.Default())

	// Only customer-a mapped to port 19001
	router.AddPortMapping("customer-a", 19001, "echo", 0) //nolint:errcheck // test setup

	// Register customer-b (but NOT customer-a)
	relayConn, agentConn := net.Pipe()
	relayMux := protocol.NewMuxSession(relayConn, protocol.RoleRelay, testMuxConfig())
	agentMux := protocol.NewMuxSession(agentConn, protocol.RoleAgent, testMuxConfig())
	defer relayMux.Close()
	defer agentMux.Close()

	liveB := NewLiveConnection("customer-b", relayMux, relayConn.RemoteAddr())
	require.NoError(t, reg.Register(context.Background(), "customer-b", liveB))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Accept on agent-B side (should never receive anything)
	received := make(chan bool, 1)
	go func() {
		stream, err := agentMux.AcceptStream(ctx)
		if err == nil {
			buf := make([]byte, 64)
			stream.Read(buf) //nolint:errcheck // test
			received <- true
		}
	}()

	// Route on port 19001 (mapped to customer-a, but customer-a not registered)
	clientConn, relayEnd := net.Pipe()
	defer clientConn.Close()

	err := router.Route(ctx, "customer-a", relayEnd, 19001)
	assert.Error(t, err, "should fail: customer-a is not registered")
	assert.Contains(t, err.Error(), "agent not found")

	// Agent-B should NOT have received any stream
	select {
	case <-received:
		t.Fatal("agent-B received a stream from port-A -- cross-tenant leak!")
	case <-time.After(200 * time.Millisecond):
		// Good: no cross-tenant access
	}
}
