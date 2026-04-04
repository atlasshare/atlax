package audit

import (
	"context"
	"time"
)

// Action identifies the type of auditable event.
type Action string

const (
	ActionAgentConnected    Action = "agent.connected"
	ActionAgentDisconnected Action = "agent.disconnected"
	ActionStreamOpened      Action = "stream.opened"
	ActionStreamClosed      Action = "stream.closed"
	ActionStreamReset       Action = "stream.reset"
	ActionAuthSuccess       Action = "auth.success"
	ActionAuthFailure       Action = "auth.failure"
	ActionCertRotation      Action = "cert.rotation"
	ActionGoAway            Action = "connection.goaway"
	ActionHeartbeatTimeout  Action = "agent.heartbeat_timeout"
)

// Event is an immutable record of a single auditable occurrence.
type Event struct {
	Action     Action
	Actor      string
	Target     string
	Timestamp  time.Time
	RequestID  string
	CustomerID string
	Metadata   map[string]string
}

// Emitter is an enterprise extension point. Community edition logs to
// structured log output. Enterprise edition may emit to event buses, SIEM
// systems, or append-only stores.
type Emitter interface {
	// Emit records a single audit event.
	Emit(ctx context.Context, event Event) error

	// Close flushes pending events and releases resources.
	Close() error
}
