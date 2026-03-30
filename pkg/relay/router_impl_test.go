package relay

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/atlasshare/atlax/pkg/protocol"
)

func testMuxConfig() protocol.MuxConfig {
	return protocol.MuxConfig{
		MaxConcurrentStreams: 256,
		InitialStreamWindow:  262144,
		ConnectionWindow:     1048576,
		PingInterval:         30 * time.Second,
		PingTimeout:          5 * time.Second,
		IdleTimeout:          60 * time.Second,
	}
}

func TestPortRouter_AddAndLookup(t *testing.T) {
	reg := NewMemoryRegistry(slog.Default())
	router := NewPortRouter(reg, slog.Default())

	require.NoError(t, router.AddPortMapping("customer-001", 8080, "http"))

	cid, svc, ok := router.LookupPort(8080)
	assert.True(t, ok)
	assert.Equal(t, "customer-001", cid)
	assert.Equal(t, "http", svc)
}

func TestPortRouter_LookupUnknownPort(t *testing.T) {
	reg := NewMemoryRegistry(slog.Default())
	router := NewPortRouter(reg, slog.Default())

	_, _, ok := router.LookupPort(9999)
	assert.False(t, ok)
}

func TestPortRouter_RemovePortMapping(t *testing.T) {
	reg := NewMemoryRegistry(slog.Default())
	router := NewPortRouter(reg, slog.Default())

	require.NoError(t, router.AddPortMapping("customer-001", 8080, "http"))
	require.NoError(t, router.RemovePortMapping("customer-001", 8080))

	_, _, ok := router.LookupPort(8080)
	assert.False(t, ok)
}

func TestPortRouter_RemoveWrongCustomer(t *testing.T) {
	reg := NewMemoryRegistry(slog.Default())
	router := NewPortRouter(reg, slog.Default())

	require.NoError(t, router.AddPortMapping("customer-001", 8080, "http"))
	err := router.RemovePortMapping("customer-002", 8080)
	assert.Error(t, err)
}

func TestPortRouter_RouteNoAgent(t *testing.T) {
	reg := NewMemoryRegistry(slog.Default())
	router := NewPortRouter(reg, slog.Default())
	router.AddPortMapping("customer-001", 8080, "http") //nolint:errcheck // test setup, error not relevant

	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := router.Route(ctx, "customer-001", c1, 8080)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "agent not found")
}

func TestPortRouter_RouteEndToEnd(t *testing.T) {
	// Set up: registry, router, agent mux pair, echo server
	reg := NewMemoryRegistry(slog.Default())
	router := NewPortRouter(reg, slog.Default())
	router.AddPortMapping("customer-001", 8080, "echo") //nolint:errcheck // test setup, error not relevant

	// Create relay<->agent mux pair
	relayConn, agentConn := net.Pipe()
	relayMux := protocol.NewMuxSession(relayConn, protocol.RoleRelay, testMuxConfig())
	agentMux := protocol.NewMuxSession(agentConn, protocol.RoleAgent, testMuxConfig())
	defer relayMux.Close()
	defer agentMux.Close()

	// Register the agent
	live := NewLiveConnection("customer-001", relayMux, relayConn.RemoteAddr())
	require.NoError(t, reg.Register(context.Background(), "customer-001", live))

	// Start echo server as local service
	echoLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer echoLn.Close()

	go func() {
		for {
			c, acceptErr := echoLn.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer c.Close()
				io.Copy(c, c)
			}()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Agent side: accept stream, forward to echo server
	go func() {
		stream, acceptErr := agentMux.AcceptStream(ctx)
		if acceptErr != nil {
			return
		}
		// Read service name from payload
		if ss, ok := stream.(*protocol.StreamSession); ok {
			assert.Equal(t, "echo", string(ss.OpenPayload()))
		}
		// Forward to echo server
		localConn, dialErr := net.Dial("tcp", echoLn.Addr().String())
		if dialErr != nil {
			return
		}
		defer localConn.Close()
		defer stream.Close()

		go io.Copy(localConn, stream)
		io.Copy(stream, localConn)
	}()

	// Client side: connect via pipe (simulates TCP client -> relay port)
	clientConn, relayEnd := net.Pipe()
	defer clientConn.Close()

	routeDone := make(chan error, 1)
	go func() {
		routeDone <- router.Route(ctx, "customer-001", relayEnd, 8080)
	}()

	// Client sends data
	msg := []byte("hello through relay")
	_, err = clientConn.Write(msg)
	require.NoError(t, err)

	// Client reads echo response
	buf := make([]byte, 64)
	n, err := clientConn.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, string(msg), string(buf[:n]))

	// Cleanup
	clientConn.Close()
	cancel()
	<-routeDone
}

func TestPortRouter_StreamOpenPayloadCarriesServiceName(t *testing.T) {
	// Verify STREAM_OPEN payload propagation via mux pair
	c1, c2 := net.Pipe()
	relay := protocol.NewMuxSession(c1, protocol.RoleRelay, testMuxConfig())
	agent := protocol.NewMuxSession(c2, protocol.RoleAgent, testMuxConfig())
	defer relay.Close()
	defer agent.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Relay opens stream with service name
	_, err := relay.OpenStreamWithPayload(ctx, []byte("smb"))
	require.NoError(t, err)

	// Agent accepts and reads payload
	stream, err := agent.AcceptStream(ctx)
	require.NoError(t, err)

	ss, ok := stream.(*protocol.StreamSession)
	require.True(t, ok)
	assert.Equal(t, "smb", string(ss.OpenPayload()))
}
