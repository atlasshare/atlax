package protocol

import (
	"context"
	"fmt"
	"math"
	"sync"
)

// maxWindowSize is the maximum allowed flow control window (2^31 - 1).
const maxWindowSize = math.MaxInt32

// FlowWindow tracks available flow control capacity for a stream or
// connection. It is safe for concurrent use.
type FlowWindow struct {
	available   int32
	initialSize int32
	mu          sync.Mutex
	cond        *sync.Cond
}

// NewFlowWindow returns a flow control window with the given initial size.
func NewFlowWindow(initialSize int32) *FlowWindow {
	w := &FlowWindow{
		available:   initialSize,
		initialSize: initialSize,
	}
	w.cond = sync.NewCond(&w.mu)
	return w
}

// Consume blocks until n bytes of window capacity are available, then
// decrements the window. Returns an error if ctx is canceled while
// waiting.
func (w *FlowWindow) Consume(ctx context.Context, n int32) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Start a goroutine that will signal when context is done.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			w.cond.Broadcast()
		case <-done:
		}
	}()

	for w.available < n {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("window: consume: %w", err)
		}
		w.cond.Wait()
	}

	w.available -= n
	return nil
}

// Update increments the available window by increment bytes. The increment
// must be positive and must not cause the window to exceed 2^31 - 1.
func (w *FlowWindow) Update(increment int32) error {
	if increment <= 0 {
		return fmt.Errorf("window: update: %w", ErrZeroWindowIncrement)
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	newSize := int64(w.available) + int64(increment)
	if newSize > maxWindowSize {
		return fmt.Errorf("window: update: %w: %d + %d = %d",
			ErrWindowOverflow, w.available, increment, newSize)
	}

	w.available = int32(newSize) //nolint:gosec // bounded by maxWindowSize check above
	w.cond.Broadcast()
	return nil
}

// Available returns the current remaining window capacity.
func (w *FlowWindow) Available() int32 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.available
}

// Reset restores the window to its initial size and wakes any blocked
// consumers. Used when a stream is reset.
func (w *FlowWindow) Reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.available = w.initialSize
	w.cond.Broadcast()
}
