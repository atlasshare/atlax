package audit

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testEmitter(buf *bytes.Buffer) *SlogEmitter {
	logger := slog.New(slog.NewJSONHandler(buf, nil))
	return NewSlogEmitter(logger, 16)
}

func TestSlogEmitter_EmitWritesJSON(t *testing.T) {
	var buf bytes.Buffer
	e := testEmitter(&buf)

	err := e.Emit(context.Background(), Event{
		Action:     ActionAgentConnected,
		Actor:      "agent-001",
		Target:     "relay.atlax.local",
		Timestamp:  time.Date(2026, 3, 29, 10, 0, 0, 0, time.UTC),
		CustomerID: "customer-dev-001",
	})
	require.NoError(t, err)
	require.NoError(t, e.Close())

	output := buf.String()
	assert.Contains(t, output, "agent.connected")
	assert.Contains(t, output, "agent-001")
	assert.Contains(t, output, "relay.atlax.local")
	assert.Contains(t, output, "customer-dev-001")
}

func TestSlogEmitter_EmitIncludesAllFields(t *testing.T) {
	var buf bytes.Buffer
	e := testEmitter(&buf)

	err := e.Emit(context.Background(), Event{
		Action:     ActionStreamOpened,
		Actor:      "relay",
		Target:     "stream-42",
		Timestamp:  time.Now(),
		RequestID:  "req-abc",
		CustomerID: "customer-123",
		Metadata:   map[string]string{"port": "445", "service": "samba"},
	})
	require.NoError(t, err)
	require.NoError(t, e.Close())

	output := buf.String()
	assert.Contains(t, output, "stream.opened")
	assert.Contains(t, output, "req-abc")
	assert.Contains(t, output, "meta.port")
	assert.Contains(t, output, "meta.service")
}

func TestSlogEmitter_CloseFlushes(t *testing.T) {
	var buf bytes.Buffer
	e := testEmitter(&buf)

	for i := range 5 {
		_ = e.Emit(context.Background(), Event{
			Action:    ActionStreamClosed,
			Actor:     "test",
			Timestamp: time.Now(),
			Metadata:  map[string]string{"i": string(rune('0' + i))},
		})
	}

	require.NoError(t, e.Close())

	// All 5 events should have been flushed
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	assert.Equal(t, 5, len(lines))
}

func TestSlogEmitter_CloseIsIdempotent(t *testing.T) {
	var buf bytes.Buffer
	e := testEmitter(&buf)
	require.NoError(t, e.Close())
	require.NoError(t, e.Close())
}

func TestSlogEmitter_EmitAfterCloseReturnsError(t *testing.T) {
	var buf bytes.Buffer
	e := testEmitter(&buf)
	require.NoError(t, e.Close())

	err := e.Emit(context.Background(), Event{
		Action: ActionAuthFailure,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmitterClosed)
}

func TestSlogEmitter_ConcurrentEmit(t *testing.T) {
	var buf bytes.Buffer
	e := testEmitter(&buf)

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.Emit(context.Background(), Event{ //nolint:errcheck // race test
				Action:    ActionStreamOpened,
				Timestamp: time.Now(),
			})
		}()
	}

	wg.Wait()
	require.NoError(t, e.Close())

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	assert.Equal(t, 20, len(lines))
}

func TestSlogEmitter_DefaultBufferSize(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	e := NewSlogEmitter(logger, 0) // should use default
	require.NoError(t, e.Close())
}

func TestAuditAction_Constants(t *testing.T) {
	actions := []struct {
		action Action
		value  string
	}{
		{ActionAgentConnected, "agent.connected"},
		{ActionAgentDisconnected, "agent.disconnected"},
		{ActionStreamOpened, "stream.opened"},
		{ActionStreamClosed, "stream.closed"},
		{ActionStreamReset, "stream.reset"},
		{ActionAuthSuccess, "auth.success"},
		{ActionAuthFailure, "auth.failure"},
		{ActionCertRotation, "cert.rotation"},
		{ActionGoAway, "connection.goaway"},
		{ActionHeartbeatTimeout, "agent.heartbeat_timeout"},
	}

	for _, tc := range actions {
		assert.Equal(t, tc.value, string(tc.action))
	}
}
