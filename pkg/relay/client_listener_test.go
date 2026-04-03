package relay

import (
	"context"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testClientListener(t *testing.T) *ClientListener {
	t.Helper()
	reg := NewMemoryRegistry(slog.Default())
	router := NewPortRouter(reg, slog.Default())
	cl := NewClientListener(ClientListenerConfig{Router: router, Logger: slog.Default()})
	return cl
}

func TestClientListener_StartPort_ListensOnAddr(t *testing.T) {
	cl := testClientListener(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		cl.StartPort(ctx, "127.0.0.1:0", 19090) //nolint:errcheck // stopped via ctx cancel
	}()
	time.Sleep(50 * time.Millisecond)

	addr := cl.Addr(19090)
	require.NotNil(t, addr, "listener should be running")

	conn, err := net.DialTimeout("tcp", addr.String(), 2*time.Second)
	require.NoError(t, err)
	conn.Close()
}

func TestClientListener_StopPort_ClosesListener(t *testing.T) {
	cl := testClientListener(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		cl.StartPort(ctx, "127.0.0.1:0", 19091) //nolint:errcheck // stopped via ctx cancel
	}()
	time.Sleep(50 * time.Millisecond)

	addr := cl.Addr(19091)
	require.NotNil(t, addr)

	err := cl.StopPort(19091)
	require.NoError(t, err)

	assert.Nil(t, cl.Addr(19091), "addr should be nil after StopPort")

	// Connection should be refused.
	_, dialErr := net.DialTimeout("tcp", addr.String(), 500*time.Millisecond)
	assert.Error(t, dialErr, "dial should fail after listener stopped")
}

func TestClientListener_StopPort_NotFound(t *testing.T) {
	cl := testClientListener(t)

	err := cl.StopPort(99999)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestClientListener_Stop_ClosesAll(t *testing.T) {
	cl := testClientListener(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start two ports.
	go func() {
		cl.StartPort(ctx, "127.0.0.1:0", 19092) //nolint:errcheck // stopped via ctx cancel
	}()
	go func() {
		cl.StartPort(ctx, "127.0.0.1:0", 19093) //nolint:errcheck // stopped via ctx cancel
	}()
	time.Sleep(50 * time.Millisecond)

	require.NotNil(t, cl.Addr(19092))
	require.NotNil(t, cl.Addr(19093))

	cl.Stop()

	assert.Nil(t, cl.Addr(19092), "port 19092 should be stopped")
	assert.Nil(t, cl.Addr(19093), "port 19093 should be stopped")
}

func TestClientListener_Addr_UnstartedPortReturnsNil(t *testing.T) {
	cl := testClientListener(t)
	assert.Nil(t, cl.Addr(55555))
}
