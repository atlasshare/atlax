package relay

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/atlasshare/atlax/pkg/protocol"
)

// newRelayMuxWithPeer returns the relay-side MuxSession of a pipe pair.
// The agent-side mux is created and closed via t.Cleanup so pipe I/O does
// not block; callers only need the relay side for exercising
// LiveConnection locking semantics.
func newRelayMuxWithPeer(t *testing.T) *protocol.MuxSession {
	t.Helper()
	c1, c2 := net.Pipe()
	cfg := protocol.MuxConfig{
		MaxConcurrentStreams: 16,
		InitialStreamWindow:  262144,
		ConnectionWindow:     1048576,
		PingInterval:         30 * time.Second,
		PingTimeout:          5 * time.Second,
		IdleTimeout:          60 * time.Second,
	}
	relay := protocol.NewMuxSession(c1, protocol.RoleRelay, cfg)
	agent := protocol.NewMuxSession(c2, protocol.RoleAgent, cfg)
	t.Cleanup(func() {
		relay.Close()
		agent.Close()
	})
	return relay
}

func TestLiveConnection_ServicesAndCertExpiry(t *testing.T) {
	relayMux := newRelayMuxWithPeer(t)
	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}
	conn := NewLiveConnection("customer-001", relayMux, addr)

	// Default zero value
	assert.Empty(t, conn.Services())
	assert.True(t, conn.CertNotAfter().IsZero())

	// Set and retrieve.
	expiry := time.Now().Add(30 * 24 * time.Hour).UTC()
	conn.SetCertNotAfter(expiry)
	conn.SetServices([]string{"samba", "http"})

	assert.Equal(t, expiry, conn.CertNotAfter())
	assert.Equal(t, []string{"samba", "http"}, conn.Services())
}

func TestLiveConnection_Services_DefensiveCopy(t *testing.T) {
	relayMux := newRelayMuxWithPeer(t)
	conn := NewLiveConnection("customer-001", relayMux, &net.TCPAddr{})

	input := []string{"samba", "http"}
	conn.SetServices(input)

	// Mutating the input slice must not affect stored state.
	input[0] = "MUTATED"
	stored := conn.Services()
	assert.Equal(t, []string{"samba", "http"}, stored, "SetServices must store a copy")

	// Mutating the returned slice must not affect the next call either.
	stored[0] = "CORRUPTED"
	second := conn.Services()
	assert.Equal(t, []string{"samba", "http"}, second, "Services must return a copy")
}

func TestLiveConnection_Services_Concurrent(t *testing.T) {
	relayMux := newRelayMuxWithPeer(t)
	conn := NewLiveConnection("customer-001", relayMux, &net.TCPAddr{})

	// Exercise RWMutex: many readers + one writer. Must not race under -race.
	var wg sync.WaitGroup
	done := make(chan struct{})

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					_ = conn.Services()
				}
			}
		}()
	}

	for i := 0; i < 100; i++ {
		conn.SetServices([]string{"samba", "http", "api"})
	}

	close(done)
	wg.Wait()
}

func TestLiveConnection_SetServices_NilInput(t *testing.T) {
	relayMux := newRelayMuxWithPeer(t)
	conn := NewLiveConnection("customer-001", relayMux, &net.TCPAddr{})

	conn.SetServices(nil)
	assert.Empty(t, conn.Services())

	conn.SetServices([]string{})
	assert.Empty(t, conn.Services())
}

func TestLiveConnection_UpdateLastSeen_StillWorksAfterServices(t *testing.T) {
	// Regression: make sure we did not break the existing mu (separate from servMu).
	relayMux := newRelayMuxWithPeer(t)
	conn := NewLiveConnection("customer-001", relayMux, &net.TCPAddr{})

	before := conn.LastSeen()
	time.Sleep(2 * time.Millisecond)
	conn.SetServices([]string{"svc"})
	conn.UpdateLastSeen()

	after := conn.LastSeen()
	require.True(t, after.After(before), "UpdateLastSeen must still advance the timestamp")
}
