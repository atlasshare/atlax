package protocol

import (
	"bytes"
	"fmt"
	"io"
	"sync"
)

// StreamSession is a concrete implementation of the Stream interface.
// It manages the lifecycle of a single multiplexed stream.
type StreamSession struct {
	id     uint32
	config StreamConfig

	mu         sync.Mutex
	cond       *sync.Cond
	state      StreamState
	readBuf    bytes.Buffer
	recvWindow int

	// writeOut receives data chunks queued by Write. The MuxSession
	// drain goroutine reads from this channel and emits STREAM_DATA
	// frames to the transport.
	writeOut chan []byte

	// closedCh is closed when the stream reaches Closed or Reset state.
	closedCh  chan struct{}
	closeOnce sync.Once

	// onLocalClose is called once when Close() transitions the stream to
	// HalfClosedLocal or Closed. The MuxSession uses this to emit a
	// STREAM_CLOSE+FIN frame on the wire.
	onLocalClose   func(uint32)
	localCloseOnce sync.Once
}

// NewStreamSession creates a stream in the Idle state.
func NewStreamSession(id uint32, config StreamConfig) *StreamSession {
	s := &StreamSession{
		id:         id,
		config:     config,
		state:      StateIdle,
		recvWindow: config.InitialWindowSize,
		writeOut:   make(chan []byte, 64),
		closedCh:   make(chan struct{}),
	}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// ID returns the stream identifier.
func (s *StreamSession) ID() uint32 { return s.id }

// State returns the current lifecycle state.
func (s *StreamSession) State() StreamState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Read reads from the internal buffer. It blocks if the buffer is empty
// and the stream is still open for receiving. Returns io.EOF when the
// remote side has closed and the buffer is drained.
func (s *StreamSession) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for s.readBuf.Len() == 0 {
		if s.isReadClosed() {
			return 0, io.EOF
		}
		s.cond.Wait()
	}

	n, err := s.readBuf.Read(p)
	if err == io.EOF {
		// Buffer drained but stream still open; not a real EOF.
		return n, nil
	}
	return n, err
}

// Write queues data for sending via the MuxSession drain loop.
// Fails if the local side is closed.
func (s *StreamSession) Write(p []byte) (int, error) {
	s.mu.Lock()
	if s.isWriteClosed() {
		s.mu.Unlock()
		return 0, fmt.Errorf("stream %d: write: %w", s.id, ErrStreamClosed)
	}
	s.mu.Unlock()

	data := make([]byte, len(p))
	copy(data, p)

	select {
	case s.writeOut <- data:
		return len(p), nil
	case <-s.closedCh:
		return 0, fmt.Errorf("stream %d: write: %w", s.id, ErrStreamClosed)
	}
}

// WriteOut returns the channel that the MuxSession reads to drain
// outgoing data into STREAM_DATA frames.
func (s *StreamSession) WriteOut() <-chan []byte {
	return s.writeOut
}

// SetOnLocalClose registers a callback invoked once when the stream is
// closed locally. Called by MuxSession during stream registration.
func (s *StreamSession) SetOnLocalClose(fn func(uint32)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onLocalClose = fn
}

// Close initiates a graceful local close (sends FIN). Transitions from
// Open to HalfClosedLocal, or from HalfClosedRemote to Closed.
func (s *StreamSession) Close() error {
	s.mu.Lock()
	shouldNotify := false

	switch s.state {
	case StateOpen:
		s.state = StateHalfClosedLocal
		shouldNotify = true
	case StateHalfClosedRemote:
		s.state = StateClosed
		shouldNotify = true
		s.signalClosed()
		s.cond.Broadcast()
	case StateHalfClosedLocal, StateClosed:
		// Already closing or closed -- idempotent
	default:
		// Idle or Reset -- no-op
	}
	s.mu.Unlock()

	if shouldNotify {
		s.localCloseOnce.Do(func() {
			if s.onLocalClose != nil {
				s.onLocalClose(s.id)
			}
		})
	}
	return nil
}

// ReceiveWindow returns the remaining receive window size.
func (s *StreamSession) ReceiveWindow() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recvWindow
}

// Open transitions the stream from Idle to Open. Called when the stream
// handshake completes (STREAM_OPEN+ACK received).
func (s *StreamSession) Open() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = StateOpen
}

// Deliver appends incoming data to the read buffer. Called by the muxer
// read loop when a STREAM_DATA frame arrives.
func (s *StreamSession) Deliver(data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readBuf.Write(data)
	s.recvWindow -= len(data)
	s.cond.Broadcast()
}

// RemoteClose signals that the remote side sent STREAM_CLOSE+FIN.
func (s *StreamSession) RemoteClose() {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch s.state {
	case StateOpen:
		s.state = StateHalfClosedRemote
	case StateHalfClosedLocal:
		s.state = StateClosed
		s.signalClosed()
	default:
		// Ignore if already closed/reset
	}
	s.cond.Broadcast()
}

// Reset immediately terminates the stream from any state.
func (s *StreamSession) Reset(code uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = StateReset
	s.readBuf.Reset()
	s.signalClosed()
	s.cond.Broadcast()
}

// signalClosed closes closedCh exactly once. Must be called with mu held.
func (s *StreamSession) signalClosed() {
	s.closeOnce.Do(func() { close(s.closedCh) })
}

// isReadClosed reports whether Read should return EOF.
func (s *StreamSession) isReadClosed() bool {
	return s.state == StateHalfClosedRemote ||
		s.state == StateClosed ||
		s.state == StateReset
}

// isWriteClosed reports whether Write should fail.
func (s *StreamSession) isWriteClosed() bool {
	return s.state == StateHalfClosedLocal ||
		s.state == StateClosed ||
		s.state == StateReset
}
