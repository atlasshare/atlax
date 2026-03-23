package protocol

import (
	"context"
	"sync"
)

// Frame priority levels for the write queue.
const (
	PriorityControl = 0 // WINDOW_UPDATE, PING, PONG, GOAWAY
	PriorityData    = 1 // STREAM_OPEN, STREAM_DATA, STREAM_CLOSE, STREAM_RESET, UDP
)

// WriteQueue is a priority queue for outgoing frames. Control frames are
// always dequeued before data frames to prevent flow control deadlocks.
type WriteQueue struct {
	mu      sync.Mutex
	cond    *sync.Cond
	control []*Frame
	data    []*Frame
	closed  bool
}

// NewWriteQueue returns an empty write queue.
func NewWriteQueue() *WriteQueue {
	q := &WriteQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// Enqueue adds a frame to the queue at the given priority level.
func (q *WriteQueue) Enqueue(f *Frame, priority int) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return
	}

	if priority == PriorityControl {
		q.control = append(q.control, f)
	} else {
		q.data = append(q.data, f)
	}
	q.cond.Signal()
}

// Dequeue blocks until a frame is available or ctx is canceled. Control
// frames are returned before data frames.
func (q *WriteQueue) Dequeue(ctx context.Context) (*Frame, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			q.cond.Broadcast()
		case <-done:
		}
	}()

	for len(q.control) == 0 && len(q.data) == 0 && !q.closed {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		q.cond.Wait()
	}

	if len(q.control) > 0 {
		f := q.control[0]
		q.control = q.control[1:]
		return f, nil
	}

	if len(q.data) > 0 {
		f := q.data[0]
		q.data = q.data[1:]
		return f, nil
	}

	// Queue is closed and empty
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, ErrGoAway
}

// Close signals the queue is shutting down. Pending Dequeue calls return.
func (q *WriteQueue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.cond.Broadcast()
}
