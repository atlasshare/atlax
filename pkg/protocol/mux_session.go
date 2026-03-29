package protocol

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// MuxRole determines stream ID allocation parity.
type MuxRole int

const (
	// RoleRelay allocates odd stream IDs (1, 3, 5, ...).
	RoleRelay MuxRole = iota
	// RoleAgent allocates even stream IDs (2, 4, 6, ...).
	RoleAgent
)

// MuxSession multiplexes streams over a single transport connection.
// It satisfies the Muxer interface.
type MuxSession struct {
	transport io.ReadWriteCloser
	codec     *FrameCodec
	config    MuxConfig
	role      MuxRole
	logger    *slog.Logger

	mu           sync.Mutex
	streams      map[uint32]*StreamSession
	nextStreamID uint32
	acceptCh     chan *StreamSession
	closeCh      chan struct{}
	closeOnce    sync.Once
	goingAway    atomic.Bool
	writeQueue   *WriteQueue

	// pendingOpen tracks streams waiting for STREAM_OPEN+ACK.
	pendingMu   sync.Mutex
	pendingOpen map[uint32]chan struct{}

	// Ping tracking
	pingMu   sync.Mutex
	pingCh   chan time.Duration
	pingData [8]byte

	// Connection-level flow control
	connSendWindow *FlowWindow
}

// Compile-time interface check.
var _ Muxer = (*MuxSession)(nil)

// NewMuxSession creates a muxer over the given transport.
func NewMuxSession(
	transport io.ReadWriteCloser,
	role MuxRole,
	config MuxConfig,
) *MuxSession {
	startID := uint32(1) // relay: odd
	if role == RoleAgent {
		startID = 2 // agent: even
	}

	m := &MuxSession{
		transport:      transport,
		codec:          NewFrameCodec(),
		config:         config,
		role:           role,
		logger:         slog.Default(),
		streams:        make(map[uint32]*StreamSession),
		nextStreamID:   startID,
		acceptCh:       make(chan *StreamSession, config.MaxConcurrentStreams),
		closeCh:        make(chan struct{}),
		writeQueue:     NewWriteQueue(),
		pendingOpen:    make(map[uint32]chan struct{}),
		connSendWindow: NewFlowWindow(int32(config.ConnectionWindow)), //nolint:gosec // ConnectionWindow is bounded by config validation
	}

	go m.readLoop()
	go m.writeLoop()
	return m
}

// OpenStream initiates a new stream to the remote peer with no payload.
func (m *MuxSession) OpenStream(ctx context.Context) (Stream, error) {
	return m.OpenStreamWithPayload(ctx, nil)
}

// OpenStreamWithPayload initiates a new stream with an optional payload
// in the STREAM_OPEN frame (e.g., target service name for routing).
func (m *MuxSession) OpenStreamWithPayload(ctx context.Context, payload []byte) (Stream, error) {
	if m.goingAway.Load() {
		return nil, fmt.Errorf("mux: open stream: %w", ErrGoAway)
	}

	m.mu.Lock()
	if len(m.streams) >= m.config.MaxConcurrentStreams {
		m.mu.Unlock()
		return nil, fmt.Errorf("mux: open stream: %w", ErrMaxStreamsExceeded)
	}

	id := m.nextStreamID
	m.nextStreamID += 2

	s := NewStreamSession(id, StreamConfig{
		InitialWindowSize: m.config.InitialStreamWindow,
	})
	m.setupStreamClose(s)
	m.streams[id] = s
	m.mu.Unlock()

	// Register pending open channel for ACK notification.
	ackCh := make(chan struct{}, 1)
	m.pendingMu.Lock()
	m.pendingOpen[id] = ackCh
	m.pendingMu.Unlock()

	// Send STREAM_OPEN with optional payload
	m.writeQueue.Enqueue(&Frame{
		Version:  ProtocolVersion,
		Command:  CmdStreamOpen,
		StreamID: id,
		Payload:  payload,
	}, PriorityData)

	// Start drain goroutine for this stream
	go m.drainStream(s)

	// Wait for ACK
	select {
	case <-ackCh:
		s.Open()
		return s, nil
	case <-ctx.Done():
		m.cleanupPendingOpen(id)
		m.removeStream(id)
		return nil, fmt.Errorf("mux: open stream: %w", ctx.Err())
	case <-m.closeCh:
		m.cleanupPendingOpen(id)
		return nil, fmt.Errorf("mux: open stream: %w", ErrGoAway)
	}
}

// AcceptStream waits for the remote peer to open a new stream.
func (m *MuxSession) AcceptStream(ctx context.Context) (Stream, error) {
	select {
	case s := <-m.acceptCh:
		if s == nil {
			return nil, fmt.Errorf("mux: accept stream: %w", ErrGoAway)
		}
		return s, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("mux: accept stream: %w", ctx.Err())
	case <-m.closeCh:
		return nil, fmt.Errorf("mux: accept stream: %w", ErrGoAway)
	}
}

// Close tears down all streams and the underlying connection.
func (m *MuxSession) Close() error {
	m.closeOnce.Do(func() {
		close(m.closeCh)
		m.writeQueue.Close()

		m.mu.Lock()
		for _, s := range m.streams {
			s.Reset(0)
		}
		m.streams = make(map[uint32]*StreamSession)
		m.mu.Unlock()

		m.transport.Close()
	})
	return nil
}

// GoAway signals the remote peer that no new streams will be accepted.
func (m *MuxSession) GoAway(code uint32) error {
	m.goingAway.Store(true)

	m.mu.Lock()
	lastID := uint32(0)
	for id := range m.streams {
		if id > lastID {
			lastID = id
		}
	}
	m.mu.Unlock()

	payload := make([]byte, 8)
	binary.BigEndian.PutUint32(payload[0:4], lastID)
	binary.BigEndian.PutUint32(payload[4:8], code)

	m.writeQueue.Enqueue(&Frame{
		Version:  ProtocolVersion,
		Command:  CmdGoAway,
		StreamID: 0,
		Payload:  payload,
	}, PriorityControl)

	return nil
}

// Ping measures round-trip latency to the remote peer.
func (m *MuxSession) Ping(ctx context.Context) (time.Duration, error) {
	m.pingMu.Lock()
	m.pingCh = make(chan time.Duration, 1)
	now := time.Now()
	binary.BigEndian.PutUint64(m.pingData[:], uint64(now.UnixNano()))
	m.pingMu.Unlock()

	start := time.Now()
	m.writeQueue.Enqueue(&Frame{
		Version:  ProtocolVersion,
		Command:  CmdPing,
		StreamID: 0,
		Payload:  m.pingData[:],
	}, PriorityControl)

	var timeout <-chan time.Time
	if m.config.PingTimeout > 0 {
		timer := time.NewTimer(m.config.PingTimeout)
		defer timer.Stop()
		timeout = timer.C
	}

	select {
	case <-m.pingCh:
		return time.Since(start), nil
	case <-timeout:
		return 0, fmt.Errorf("mux: ping: timeout after %v", m.config.PingTimeout)
	case <-ctx.Done():
		return 0, fmt.Errorf("mux: ping: %w", ctx.Err())
	case <-m.closeCh:
		return 0, fmt.Errorf("mux: ping: %w", ErrGoAway)
	}
}

// NumStreams returns the count of currently open streams.
func (m *MuxSession) NumStreams() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.streams)
}

// readLoop reads frames from the transport and dispatches them.
func (m *MuxSession) readLoop() {
	for {
		f, err := m.codec.ReadFrame(m.transport)
		if err != nil {
			select {
			case <-m.closeCh:
				return
			default:
			}
			m.logger.Error("mux: read loop", "error", err)
			m.Close()
			return
		}
		m.handleFrame(f)
	}
}

// writeLoop dequeues frames and writes them to the transport.
func (m *MuxSession) writeLoop() {
	ctx := context.Background()
	for {
		select {
		case <-m.closeCh:
			return
		default:
		}

		f, err := m.writeQueue.Dequeue(ctx)
		if err != nil {
			return
		}

		if err := m.codec.WriteFrame(m.transport, f); err != nil {
			select {
			case <-m.closeCh:
				return
			default:
			}
			m.logger.Error("mux: write loop", "error", err)
			m.Close()
			return
		}
	}
}

// drainStream reads from a stream's write output channel and enqueues
// STREAM_DATA frames into the write queue.
func (m *MuxSession) drainStream(s *StreamSession) {
	for {
		select {
		case data, ok := <-s.writeOut:
			if !ok {
				return
			}
			m.writeQueue.Enqueue(&Frame{
				Version:  ProtocolVersion,
				Command:  CmdStreamData,
				StreamID: s.id,
				Payload:  data,
			}, PriorityData)
		case <-s.closedCh:
			return
		case <-m.closeCh:
			return
		}
	}
}

// handleFrame dispatches an incoming frame by command type.
func (m *MuxSession) handleFrame(f *Frame) {
	switch f.Command {
	case CmdStreamOpen:
		m.handleStreamOpen(f)
	case CmdStreamData:
		m.handleStreamData(f)
	case CmdStreamClose:
		m.handleStreamClose(f)
	case CmdStreamReset:
		m.handleStreamReset(f)
	case CmdPing:
		m.handlePing(f)
	case CmdPong:
		m.handlePong(f)
	case CmdWindowUpdate:
		m.handleWindowUpdate(f)
	case CmdGoAway:
		m.handleGoAway(f)
	}
}

func (m *MuxSession) handleStreamOpen(f *Frame) {
	if f.Flags&FlagACK != 0 {
		// ACK for a stream we opened -- notify the pending open.
		m.pendingMu.Lock()
		ch, ok := m.pendingOpen[f.StreamID]
		if ok {
			delete(m.pendingOpen, f.StreamID)
		}
		m.pendingMu.Unlock()

		if ok {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
		return
	}

	// Remote peer is opening a new stream.
	cfg := StreamConfig{InitialWindowSize: m.config.InitialStreamWindow}
	s := NewStreamSession(f.StreamID, cfg)
	m.setupStreamClose(s)
	if len(f.Payload) > 0 {
		s.SetOpenPayload(f.Payload)
	}
	s.Open()

	m.mu.Lock()
	m.streams[f.StreamID] = s
	m.mu.Unlock()

	// Start drain goroutine for this stream.
	go m.drainStream(s)

	// Send ACK
	m.writeQueue.Enqueue(&Frame{
		Version:  ProtocolVersion,
		Command:  CmdStreamOpen,
		Flags:    FlagACK,
		StreamID: f.StreamID,
	}, PriorityData)

	// Deliver to AcceptStream
	select {
	case m.acceptCh <- s:
	case <-m.closeCh:
	}
}

func (m *MuxSession) handleStreamData(f *Frame) {
	m.mu.Lock()
	s, ok := m.streams[f.StreamID]
	m.mu.Unlock()
	if !ok {
		return
	}

	if len(f.Payload) > 0 {
		s.Deliver(f.Payload)
	}

	if f.Flags&FlagFIN != 0 {
		s.RemoteClose()
		m.maybeRemoveStream(f.StreamID, s)
	}
}

func (m *MuxSession) handleStreamClose(f *Frame) {
	m.mu.Lock()
	s, ok := m.streams[f.StreamID]
	m.mu.Unlock()
	if !ok {
		return
	}

	s.RemoteClose()
	m.maybeRemoveStream(f.StreamID, s)
}

func (m *MuxSession) handleStreamReset(f *Frame) {
	m.mu.Lock()
	s, ok := m.streams[f.StreamID]
	m.mu.Unlock()
	if !ok {
		return
	}

	var code uint32
	if len(f.Payload) >= 4 {
		code = binary.BigEndian.Uint32(f.Payload[:4])
	}
	s.Reset(code)

	m.mu.Lock()
	delete(m.streams, f.StreamID)
	m.mu.Unlock()
}

func (m *MuxSession) handlePing(f *Frame) {
	m.writeQueue.Enqueue(&Frame{
		Version:  ProtocolVersion,
		Command:  CmdPong,
		StreamID: 0,
		Payload:  f.Payload,
	}, PriorityControl)
}

func (m *MuxSession) handlePong(_ *Frame) {
	m.pingMu.Lock()
	ch := m.pingCh
	m.pingMu.Unlock()

	if ch != nil {
		select {
		case ch <- 0:
		default:
		}
	}
}

func (m *MuxSession) handleWindowUpdate(f *Frame) {
	if len(f.Payload) < 4 {
		return
	}

	increment := int32(binary.BigEndian.Uint32(f.Payload[:4])) //nolint:gosec // protocol field

	if f.StreamID == 0 {
		// Connection-level window update.
		if err := m.connSendWindow.Update(increment); err != nil {
			m.logger.Warn("mux: connection window update failed",
				"error", err)
		}
		return
	}

	// Stream-level window update.
	m.mu.Lock()
	s, ok := m.streams[f.StreamID]
	m.mu.Unlock()
	if !ok {
		return
	}

	s.mu.Lock()
	s.recvWindow += int(increment)
	s.mu.Unlock()
}

func (m *MuxSession) handleGoAway(_ *Frame) {
	m.goingAway.Store(true)
}

// maybeRemoveStream removes a stream from the map if it is fully closed.
func (m *MuxSession) maybeRemoveStream(id uint32, s *StreamSession) {
	if s.State() == StateClosed || s.State() == StateReset {
		m.mu.Lock()
		delete(m.streams, id)
		m.mu.Unlock()
	}
}

// removeStream removes a stream by ID (used for cleanup on failed open).
func (m *MuxSession) removeStream(id uint32) {
	m.mu.Lock()
	if s, ok := m.streams[id]; ok {
		s.Reset(0)
		delete(m.streams, id)
	}
	m.mu.Unlock()
}

// setupStreamClose registers the onLocalClose callback so that
// Stream.Close() emits a STREAM_CLOSE+FIN frame on the wire and
// cleans up the stream if fully closed.
func (m *MuxSession) setupStreamClose(s *StreamSession) {
	s.SetOnLocalClose(func(streamID uint32) {
		m.writeQueue.Enqueue(&Frame{
			Version:  ProtocolVersion,
			Command:  CmdStreamClose,
			Flags:    FlagFIN,
			StreamID: streamID,
		}, PriorityData)
		m.maybeRemoveStream(streamID, s)
	})
}

// cleanupPendingOpen removes a pending open channel by stream ID.
func (m *MuxSession) cleanupPendingOpen(id uint32) {
	m.pendingMu.Lock()
	delete(m.pendingOpen, id)
	m.pendingMu.Unlock()
}
