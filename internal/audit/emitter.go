package audit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
)

// DefaultBufferSize is the default channel buffer for the async emitter.
const DefaultBufferSize = 256

// ErrEmitterClosed is returned when Emit is called after Close.
var ErrEmitterClosed = errors.New("audit: emitter is closed")

// SlogEmitter writes audit events as structured JSON log entries via slog.
// It is the community edition implementation of the Emitter interface.
type SlogEmitter struct {
	logger  *slog.Logger
	eventCh chan Event
	closeCh chan struct{} // closed to signal Emit to stop sending
	done    chan struct{} // closed when drainLoop finishes
	once    sync.Once
}

// Compile-time interface check.
var _ Emitter = (*SlogEmitter)(nil)

// NewSlogEmitter creates an async emitter backed by the given logger.
// Events are buffered in a channel of the given size and drained by a
// background goroutine.
func NewSlogEmitter(logger *slog.Logger, bufferSize int) *SlogEmitter {
	if bufferSize <= 0 {
		bufferSize = DefaultBufferSize
	}
	e := &SlogEmitter{
		logger:  logger,
		eventCh: make(chan Event, bufferSize),
		closeCh: make(chan struct{}),
		done:    make(chan struct{}),
	}
	go e.drainLoop()
	return e
}

// Emit sends an audit event to the background drain loop. Returns an
// error if the emitter has been closed.
//
//nolint:gocritic // Event is immutable value type; interface requires value semantics
func (e *SlogEmitter) Emit(_ context.Context, event Event) error {
	select {
	case <-e.closeCh:
		return fmt.Errorf("audit: emit %s: %w", event.Action, ErrEmitterClosed)
	default:
	}

	select {
	case e.eventCh <- event:
		return nil
	case <-e.closeCh:
		return fmt.Errorf("audit: emit %s: %w", event.Action, ErrEmitterClosed)
	}
}

// Close stops the drain loop and flushes remaining events. It is
// idempotent.
func (e *SlogEmitter) Close() error {
	e.once.Do(func() {
		close(e.closeCh) // signal Emit to stop sending
		close(e.eventCh) // signal drainLoop to finish
		<-e.done         // wait for drain to flush
	})
	return nil
}

// drainLoop reads events from the channel and logs them.
func (e *SlogEmitter) drainLoop() {
	defer close(e.done)
	for event := range e.eventCh {
		e.logEvent(event)
	}
}

// logEvent writes a single event as structured slog attributes.
//
//nolint:gocritic // called from drainLoop with channel-received value
func (e *SlogEmitter) logEvent(event Event) {
	attrs := []slog.Attr{
		slog.String("action", string(event.Action)),
		slog.String("actor", event.Actor),
		slog.String("target", event.Target),
		slog.Time("event_time", event.Timestamp),
		slog.String("customer_id", event.CustomerID),
	}
	if event.RequestID != "" {
		attrs = append(attrs, slog.String("request_id", event.RequestID))
	}
	for k, v := range event.Metadata {
		attrs = append(attrs, slog.String("meta."+k, v))
	}

	args := make([]any, len(attrs))
	for i, a := range attrs {
		args[i] = a
	}
	e.logger.Info("audit", args...)
}
