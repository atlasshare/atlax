package protocol

import (
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func defaultMuxConfig() MuxConfig {
	return MuxConfig{
		MaxConcurrentStreams: 256,
		InitialStreamWindow:  262144,
		ConnectionWindow:     1048576,
		PingInterval:         30 * time.Second,
		PingTimeout:          5 * time.Second,
		IdleTimeout:          60 * time.Second,
	}
}

// newMuxPair creates two connected MuxSessions over a net.Pipe.
func newMuxPair(t *testing.T) (relay, agent *MuxSession) {
	t.Helper()
	c1, c2 := net.Pipe()
	relay = NewMuxSession(c1, RoleRelay, defaultMuxConfig())
	agent = NewMuxSession(c2, RoleAgent, defaultMuxConfig())
	t.Cleanup(func() {
		relay.Close()
		agent.Close()
	})
	return relay, agent
}

func TestMuxSession_Create(t *testing.T) {
	relay, agent := newMuxPair(t)
	assert.NotNil(t, relay)
	assert.NotNil(t, agent)
}

func TestMuxSession_OpenStreamRelayOddIDs(t *testing.T) {
	relay, _ := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	s1, err := relay.OpenStream(ctx)
	require.NoError(t, err)
	assert.Equal(t, uint32(1), s1.ID())

	s2, err := relay.OpenStream(ctx)
	require.NoError(t, err)
	assert.Equal(t, uint32(3), s2.ID())
}

func TestMuxSession_OpenStreamAgentEvenIDs(t *testing.T) {
	_, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	s1, err := agent.OpenStream(ctx)
	require.NoError(t, err)
	assert.Equal(t, uint32(2), s1.ID())

	s2, err := agent.OpenStream(ctx)
	require.NoError(t, err)
	assert.Equal(t, uint32(4), s2.ID())
}

func TestMuxSession_OpenStreamMaxExceeded(t *testing.T) {
	c1, c2 := net.Pipe()
	cfg := defaultMuxConfig()
	cfg.MaxConcurrentStreams = 2

	relay := NewMuxSession(c1, RoleRelay, cfg)
	_ = NewMuxSession(c2, RoleAgent, cfg)
	defer relay.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := relay.OpenStream(ctx)
	require.NoError(t, err)
	_, err = relay.OpenStream(ctx)
	require.NoError(t, err)

	_, err = relay.OpenStream(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMaxStreamsExceeded)
}

func TestMuxSession_AcceptStream(t *testing.T) {
	relay, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Relay opens, agent accepts
	_, err := relay.OpenStream(ctx)
	require.NoError(t, err)

	s, err := agent.AcceptStream(ctx)
	require.NoError(t, err)
	assert.Equal(t, uint32(1), s.ID())
}

func TestMuxSession_AcceptStreamBlocksUntilAvailable(t *testing.T) {
	relay, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	accepted := make(chan Stream, 1)
	go func() {
		s, err := agent.AcceptStream(ctx)
		if err == nil {
			accepted <- s
		}
	}()

	// Should not have accepted yet
	select {
	case <-accepted:
		t.Fatal("AcceptStream should block until a stream is opened")
	case <-time.After(50 * time.Millisecond):
	}

	_, err := relay.OpenStream(ctx)
	require.NoError(t, err)

	select {
	case s := <-accepted:
		assert.Equal(t, uint32(1), s.ID())
	case <-time.After(2 * time.Second):
		t.Fatal("AcceptStream should have returned after OpenStream")
	}
}

func TestMuxSession_AcceptStreamRespectsContext(t *testing.T) {
	_, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := agent.AcceptStream(ctx)
	require.Error(t, err)
}

func TestMuxSession_CloseTeardownStreams(t *testing.T) {
	relay, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := relay.OpenStream(ctx)
	require.NoError(t, err)

	// Give time for stream to register on agent side
	time.Sleep(50 * time.Millisecond)

	assert.Equal(t, 1, relay.NumStreams())
	relay.Close()
	assert.Equal(t, 0, relay.NumStreams())

	_ = agent
}

func TestMuxSession_CloseUnblocksAcceptStream(t *testing.T) {
	_, agent := newMuxPair(t)

	done := make(chan error, 1)
	go func() {
		_, err := agent.AcceptStream(context.Background())
		done <- err
	}()

	time.Sleep(50 * time.Millisecond)
	agent.Close()

	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("AcceptStream should have returned after Close")
	}
}

func TestMuxSession_GoAway(t *testing.T) {
	relay, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Open a stream first
	_, err := relay.OpenStream(ctx)
	require.NoError(t, err)

	// Send GOAWAY
	require.NoError(t, relay.GoAway(0))

	// New streams should be rejected
	_, err = relay.OpenStream(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrGoAway)

	_ = agent
}

func TestMuxSession_PingPong(t *testing.T) {
	relay, _ := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	latency, err := relay.Ping(ctx)
	require.NoError(t, err)
	assert.True(t, latency > 0, "latency should be positive")
}

func TestMuxSession_PingTimeout(t *testing.T) {
	// Use a transport that never responds
	c1, c2 := net.Pipe()
	defer c2.Close()

	cfg := defaultMuxConfig()
	cfg.PingTimeout = 100 * time.Millisecond

	m := NewMuxSession(c1, RoleRelay, cfg)
	defer m.Close()

	// Drain writes so writeLoop does not block
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := c2.Read(buf); err != nil {
				return
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := m.Ping(ctx)
	require.Error(t, err)
}

func TestMuxSession_NumStreams(t *testing.T) {
	relay, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	assert.Equal(t, 0, relay.NumStreams())

	_, err := relay.OpenStream(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, relay.NumStreams())

	_, err = relay.OpenStream(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, relay.NumStreams())

	_ = agent
}

func TestMuxSession_BidirectionalData(t *testing.T) {
	relay, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Relay opens stream
	relayStream, err := relay.OpenStream(ctx)
	require.NoError(t, err)

	// Agent accepts
	agentStream, err := agent.AcceptStream(ctx)
	require.NoError(t, err)

	// Relay writes, agent reads via drain loop
	msg := []byte("hello from relay")
	_, err = relayStream.Write(msg)
	require.NoError(t, err)

	buf := make([]byte, 64)
	n, err := agentStream.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, string(msg), string(buf[:n]))

	// Agent writes back, relay reads
	reply := []byte("hello from agent")
	_, err = agentStream.Write(reply)
	require.NoError(t, err)

	n, err = relayStream.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, string(reply), string(buf[:n]))
}

func TestMuxSession_ConcurrentOpenStream(t *testing.T) {
	relay, _ := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	var mu sync.Mutex
	ids := make(map[uint32]bool)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := relay.OpenStream(ctx)
			if err != nil {
				return
			}
			mu.Lock()
			ids[s.ID()] = true
			mu.Unlock()
		}()
	}

	wg.Wait()

	// All IDs should be unique and odd (relay-initiated)
	assert.Equal(t, 10, len(ids))
	for id := range ids {
		assert.Equal(t, uint32(1), id%2, "relay stream ID should be odd")
	}
}

func TestMuxSession_IntegrationLifecycle(t *testing.T) {
	relay, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. Relay opens stream
	relayStream, err := relay.OpenStream(ctx)
	require.NoError(t, err)

	// 2. Agent accepts stream
	agentStream, err := agent.AcceptStream(ctx)
	require.NoError(t, err)
	assert.Equal(t, relayStream.ID(), agentStream.ID())

	// 3. Both sides are open
	assert.Equal(t, StateOpen, relayStream.(*StreamSession).State())
	assert.Equal(t, StateOpen, agentStream.(*StreamSession).State())

	// 4. Relay closes its side
	require.NoError(t, relayStream.Close())
	assert.Equal(t, StateHalfClosedLocal, relayStream.(*StreamSession).State())

	// 5. Ping still works
	latency, err := relay.Ping(ctx)
	require.NoError(t, err)
	assert.True(t, latency >= 0)

	// 6. NumStreams reflects open streams
	assert.True(t, relay.NumStreams() >= 1)

	// 7. GoAway prevents new streams
	require.NoError(t, relay.GoAway(0))
	_, err = relay.OpenStream(ctx)
	assert.ErrorIs(t, err, ErrGoAway)
}

func TestMuxSession_HandleStreamData(t *testing.T) {
	relay, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Relay opens stream, agent accepts
	_, err := relay.OpenStream(ctx)
	require.NoError(t, err)

	agentStream, err := agent.AcceptStream(ctx)
	require.NoError(t, err)

	// Write STREAM_DATA frame directly from relay's write queue
	relay.writeQueue.Enqueue(&Frame{
		Version:  ProtocolVersion,
		Command:  CmdStreamData,
		StreamID: agentStream.ID(),
		Payload:  []byte("direct data"),
	}, PriorityData)

	// Wait for delivery
	time.Sleep(100 * time.Millisecond)

	buf := make([]byte, 32)
	n, err := agentStream.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "direct data", string(buf[:n]))
}

func TestMuxSession_HandleStreamDataFIN(t *testing.T) {
	relay, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := relay.OpenStream(ctx)
	require.NoError(t, err)

	agentStream, err := agent.AcceptStream(ctx)
	require.NoError(t, err)

	// Send STREAM_DATA with FIN flag
	relay.writeQueue.Enqueue(&Frame{
		Version:  ProtocolVersion,
		Command:  CmdStreamData,
		Flags:    FlagFIN,
		StreamID: agentStream.ID(),
		Payload:  []byte("last"),
	}, PriorityData)

	time.Sleep(100 * time.Millisecond)

	buf := make([]byte, 32)
	n, err := agentStream.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "last", string(buf[:n]))

	// Next read should EOF (remote closed via FIN)
	_, err = agentStream.Read(buf)
	assert.ErrorIs(t, err, io.EOF)
}

func TestMuxSession_HandleStreamClose(t *testing.T) {
	relay, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := relay.OpenStream(ctx)
	require.NoError(t, err)

	agentStream, err := agent.AcceptStream(ctx)
	require.NoError(t, err)

	// Send STREAM_CLOSE+FIN from relay
	relay.writeQueue.Enqueue(&Frame{
		Version:  ProtocolVersion,
		Command:  CmdStreamClose,
		Flags:    FlagFIN,
		StreamID: agentStream.ID(),
	}, PriorityData)

	time.Sleep(100 * time.Millisecond)

	ss := agentStream.(*StreamSession)
	state := ss.State()
	assert.True(t, state == StateHalfClosedRemote || state == StateClosed,
		"expected HalfClosedRemote or Closed, got %v", state)
}

func TestMuxSession_HandleStreamReset(t *testing.T) {
	relay, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := relay.OpenStream(ctx)
	require.NoError(t, err)

	agentStream, err := agent.AcceptStream(ctx)
	require.NoError(t, err)

	// Send STREAM_RESET from relay
	resetPayload := make([]byte, 4)
	relay.writeQueue.Enqueue(&Frame{
		Version:  ProtocolVersion,
		Command:  CmdStreamReset,
		StreamID: agentStream.ID(),
		Payload:  resetPayload,
	}, PriorityData)

	time.Sleep(100 * time.Millisecond)

	ss := agentStream.(*StreamSession)
	assert.Equal(t, StateReset, ss.State())
}

func TestMuxSession_HandleWindowUpdate_ConnectionLevel(t *testing.T) {
	relay, agent := newMuxPair(t)

	// Record initial connection window on agent side
	initialWindow := agent.connSendWindow.Available()

	// Send WINDOW_UPDATE (connection level, stream ID 0) from relay to agent
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, 65536)

	relay.writeQueue.Enqueue(&Frame{
		Version:  ProtocolVersion,
		Command:  CmdWindowUpdate,
		StreamID: 0,
		Payload:  payload,
	}, PriorityControl)

	// Wait for frame to arrive
	time.Sleep(100 * time.Millisecond)

	// Agent's connection send window should have increased
	assert.Equal(t, initialWindow+65536, agent.connSendWindow.Available())
}

func TestMuxSession_HandleWindowUpdate_StreamLevel(t *testing.T) {
	relay, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := relay.OpenStream(ctx)
	require.NoError(t, err)

	agentStream, err := agent.AcceptStream(ctx)
	require.NoError(t, err)

	initialWindow := agentStream.ReceiveWindow()

	// Send WINDOW_UPDATE for this stream from relay to agent
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, 32768)

	relay.writeQueue.Enqueue(&Frame{
		Version:  ProtocolVersion,
		Command:  CmdWindowUpdate,
		StreamID: agentStream.ID(),
		Payload:  payload,
	}, PriorityData)

	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, initialWindow+32768, agentStream.ReceiveWindow())
}

func TestMuxSession_HandleWindowUpdate_InvalidStreamID(t *testing.T) {
	relay, _ := newMuxPair(t)

	// Send WINDOW_UPDATE for nonexistent stream -- should not panic
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, 1024)

	relay.writeQueue.Enqueue(&Frame{
		Version:  ProtocolVersion,
		Command:  CmdWindowUpdate,
		StreamID: 99999,
		Payload:  payload,
	}, PriorityData)

	time.Sleep(50 * time.Millisecond)
	// No panic = success
}

func TestMuxSession_StreamRemovedAfterFullClose(t *testing.T) {
	relay, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	relayStream, err := relay.OpenStream(ctx)
	require.NoError(t, err)

	agentStream, err := agent.AcceptStream(ctx)
	require.NoError(t, err)

	// Both sides close
	require.NoError(t, relayStream.Close())
	relay.writeQueue.Enqueue(&Frame{
		Version:  ProtocolVersion,
		Command:  CmdStreamClose,
		Flags:    FlagFIN,
		StreamID: agentStream.ID(),
	}, PriorityData)

	require.NoError(t, agentStream.Close())
	agent.writeQueue.Enqueue(&Frame{
		Version:  ProtocolVersion,
		Command:  CmdStreamClose,
		Flags:    FlagFIN,
		StreamID: relayStream.ID(),
	}, PriorityData)

	time.Sleep(200 * time.Millisecond)

	// Streams should be cleaned up
	assert.Equal(t, 0, agent.NumStreams())
}

func TestWriteQueue_ControlBeforeData(t *testing.T) {
	q := NewWriteQueue()

	dataFrame := &Frame{Command: CmdStreamData, StreamID: 1}
	controlFrame := &Frame{Command: CmdPing, StreamID: 0}

	// Enqueue data first, then control
	q.Enqueue(dataFrame, PriorityData)
	q.Enqueue(controlFrame, PriorityControl)

	ctx := context.Background()

	// Control should come out first
	f, err := q.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, CmdPing, f.Command)

	f, err = q.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, CmdStreamData, f.Command)
}

func TestWriteQueue_BlocksUntilAvailable(t *testing.T) {
	q := NewWriteQueue()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan *Frame, 1)
	go func() {
		f, err := q.Dequeue(ctx)
		if err == nil {
			done <- f
		}
	}()

	select {
	case <-done:
		t.Fatal("Dequeue should block when empty")
	case <-time.After(50 * time.Millisecond):
	}

	q.Enqueue(&Frame{Command: CmdPing}, PriorityControl)

	select {
	case f := <-done:
		assert.Equal(t, CmdPing, f.Command)
	case <-time.After(time.Second):
		t.Fatal("Dequeue should have returned after Enqueue")
	}
}

func TestWriteQueue_ContextCancellation(t *testing.T) {
	q := NewWriteQueue()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := q.Dequeue(ctx)
	require.Error(t, err)
}

func TestWriteQueue_Close(t *testing.T) {
	q := NewWriteQueue()

	done := make(chan error, 1)
	go func() {
		_, err := q.Dequeue(context.Background())
		done <- err
	}()

	time.Sleep(20 * time.Millisecond)
	q.Close()

	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("Dequeue should return after Close")
	}
}

func TestWriteQueue_EnqueueAfterClose(t *testing.T) {
	q := NewWriteQueue()
	q.Close()
	// Should not panic
	q.Enqueue(&Frame{Command: CmdPing}, PriorityControl)
}

// --- STREAM_CLOSE wire emission tests ---

func TestMuxSession_StreamCloseEmitsFrame(t *testing.T) {
	relay, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Relay opens stream, agent accepts
	relayStream, err := relay.OpenStream(ctx)
	require.NoError(t, err)

	agentStream, err := agent.AcceptStream(ctx)
	require.NoError(t, err)
	assert.Equal(t, StateOpen, agentStream.(*StreamSession).State())

	// Relay closes its side -- should emit STREAM_CLOSE+FIN on wire
	require.NoError(t, relayStream.Close())

	// Agent should transition to HalfClosedRemote when it receives STREAM_CLOSE
	time.Sleep(100 * time.Millisecond)

	state := agentStream.(*StreamSession).State()
	assert.Equal(t, StateHalfClosedRemote, state,
		"agent stream should be HalfClosedRemote after relay close")
}

func TestMuxSession_StreamCloseAgentSide(t *testing.T) {
	relay, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	relayStream, err := relay.OpenStream(ctx)
	require.NoError(t, err)

	agentStream, err := agent.AcceptStream(ctx)
	require.NoError(t, err)

	// Agent closes its side
	require.NoError(t, agentStream.Close())

	// Relay should see the close
	time.Sleep(100 * time.Millisecond)

	state := relayStream.(*StreamSession).State()
	assert.Equal(t, StateHalfClosedRemote, state,
		"relay stream should be HalfClosedRemote after agent close")
}

func TestMuxSession_DoubleCloseNoDuplicate(t *testing.T) {
	relay, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	relayStream, err := relay.OpenStream(ctx)
	require.NoError(t, err)

	_, err = agent.AcceptStream(ctx)
	require.NoError(t, err)

	// Close twice -- should not panic or send duplicate frames
	require.NoError(t, relayStream.Close())
	require.NoError(t, relayStream.Close())

	time.Sleep(50 * time.Millisecond)
	// No panic = success
}

func TestMuxSession_CloseAfterResetNoFrame(t *testing.T) {
	relay, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	relayStream, err := relay.OpenStream(ctx)
	require.NoError(t, err)

	agentStream, err := agent.AcceptStream(ctx)
	require.NoError(t, err)

	// Reset the stream first
	relayStream.(*StreamSession).Reset(0)
	assert.Equal(t, StateReset, relayStream.(*StreamSession).State())

	// Close after reset should be a no-op
	require.NoError(t, relayStream.Close())

	time.Sleep(50 * time.Millisecond)

	// Agent should see Reset, not HalfClosedRemote
	state := agentStream.(*StreamSession).State()
	assert.True(t, state == StateReset || state == StateOpen,
		"agent should not see STREAM_CLOSE after Reset, got %v", state)
}

func TestMuxSession_FullStreamLifecycle(t *testing.T) {
	relay, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. Open
	relayStream, err := relay.OpenStream(ctx)
	require.NoError(t, err)
	agentStream, err := agent.AcceptStream(ctx)
	require.NoError(t, err)

	// 2. Exchange data
	_, err = relayStream.Write([]byte("request"))
	require.NoError(t, err)
	buf := make([]byte, 64)
	n, err := agentStream.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "request", string(buf[:n]))

	_, err = agentStream.Write([]byte("response"))
	require.NoError(t, err)
	n, err = relayStream.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "response", string(buf[:n]))

	// 3. Relay closes (half-close)
	require.NoError(t, relayStream.Close())
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, StateHalfClosedLocal, relayStream.(*StreamSession).State())
	assert.Equal(t, StateHalfClosedRemote, agentStream.(*StreamSession).State())

	// 4. Agent reads EOF (remote closed)
	_, err = agentStream.Read(buf)
	assert.ErrorIs(t, err, io.EOF)

	// 5. Agent closes (both sides closed)
	require.NoError(t, agentStream.Close())
	time.Sleep(100 * time.Millisecond)

	// 6. Both streams should be Closed and removed from mux
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, StateClosed, relayStream.(*StreamSession).State())
	assert.Equal(t, StateClosed, agentStream.(*StreamSession).State())
	assert.Equal(t, 0, relay.NumStreams())
	assert.Equal(t, 0, agent.NumStreams())
}

// --- Stream ID recycling tests ---

func TestMuxSession_StreamIDRecycling(t *testing.T) {
	relay, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Open a stream
	s1, err := relay.OpenStream(ctx)
	require.NoError(t, err)
	id1 := s1.ID()

	agentS1, err := agent.AcceptStream(ctx)
	require.NoError(t, err)

	// Close both sides so stream reaches Closed state
	s1.Close()
	time.Sleep(100 * time.Millisecond)
	agentS1.Close()
	time.Sleep(200 * time.Millisecond)

	// Open a new stream -- should reuse id1
	s2, err := relay.OpenStream(ctx)
	require.NoError(t, err)

	assert.Equal(t, id1, s2.ID(), "recycled stream should reuse the closed ID")

	_, err = agent.AcceptStream(ctx)
	require.NoError(t, err)
}

func TestMuxSession_StreamIDRecyclingPreservesParity(t *testing.T) {
	relay, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Open 3 streams from relay (odd IDs: 1, 3, 5)
	ids := make([]uint32, 0, 3)
	for range 3 {
		s, err := relay.OpenStream(ctx)
		require.NoError(t, err)
		ids = append(ids, s.ID())
		_, err = agent.AcceptStream(ctx)
		require.NoError(t, err)
	}

	assert.Equal(t, uint32(1), ids[0])
	assert.Equal(t, uint32(3), ids[1])
	assert.Equal(t, uint32(5), ids[2])

	// Close all via relay side
	relay.mu.Lock()
	for _, s := range relay.streams {
		s.Close()
	}
	relay.mu.Unlock()
	time.Sleep(300 * time.Millisecond)

	// Open new streams -- should recycle in LIFO order (5, 3, 1)
	s1, err := relay.OpenStream(ctx)
	require.NoError(t, err)
	_, err = agent.AcceptStream(ctx)
	require.NoError(t, err)

	// Recycled ID should be odd (relay parity preserved)
	assert.Equal(t, uint32(1), s1.ID()%2, "recycled ID should be odd for relay")
}

// --- Reset cleanup tests (regression for #84: stream leak) ---

func TestMuxSession_ResetRemovesStreamFromMap(t *testing.T) {
	relay, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	relayStream, err := relay.OpenStream(ctx)
	require.NoError(t, err)

	_, err = agent.AcceptStream(ctx)
	require.NoError(t, err)

	assert.Equal(t, 1, relay.NumStreams())

	// Reset the stream (same path as copyBidirectional cleanup)
	relayStream.(*StreamSession).Reset(0)

	// Wait for cleanup
	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, 0, relay.NumStreams(),
		"Reset() must remove stream from MuxSession.streams map")
}

func TestMuxSession_ResetSendsFrameToRemote(t *testing.T) {
	relay, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	relayStream, err := relay.OpenStream(ctx)
	require.NoError(t, err)

	agentStream, err := agent.AcceptStream(ctx)
	require.NoError(t, err)

	// Reset on relay side should send STREAM_RESET to agent
	relayStream.(*StreamSession).Reset(0)

	time.Sleep(200 * time.Millisecond)

	assert.Equal(t, StateReset, agentStream.(*StreamSession).State(),
		"agent stream should be Reset after relay sends STREAM_RESET")
	assert.Equal(t, 0, agent.NumStreams(),
		"agent should also remove the stream from its map")
}

func TestMuxSession_ResetIsIdempotent(t *testing.T) {
	relay, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	relayStream, err := relay.OpenStream(ctx)
	require.NoError(t, err)

	_, err = agent.AcceptStream(ctx)
	require.NoError(t, err)

	ss := relayStream.(*StreamSession)

	// Call Reset twice -- should not panic
	ss.Reset(0)
	ss.Reset(0)

	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, StateReset, ss.State())
	assert.Equal(t, 0, relay.NumStreams())
}

func TestMuxSession_ResetAfterClose(t *testing.T) {
	relay, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	relayStream, err := relay.OpenStream(ctx)
	require.NoError(t, err)

	_, err = agent.AcceptStream(ctx)
	require.NoError(t, err)

	ss := relayStream.(*StreamSession)

	// Close first (graceful), then Reset
	require.NoError(t, relayStream.Close())
	ss.Reset(0)

	time.Sleep(100 * time.Millisecond)

	// Stream should be removed regardless of path
	assert.Equal(t, 0, relay.NumStreams())
}

func TestMuxSession_ResetStreamIDRecycled(t *testing.T) {
	relay, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Open stream, get ID 1
	s1, err := relay.OpenStream(ctx)
	require.NoError(t, err)
	id1 := s1.ID()

	_, err = agent.AcceptStream(ctx)
	require.NoError(t, err)

	// Reset it
	s1.(*StreamSession).Reset(0)
	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, 0, relay.NumStreams())

	// Open another -- should recycle id1
	s2, err := relay.OpenStream(ctx)
	require.NoError(t, err)

	_, err = agent.AcceptStream(ctx)
	require.NoError(t, err)

	assert.Equal(t, id1, s2.ID(),
		"stream ID should be recycled after Reset")
}

func TestMuxSession_BulkCloseNoResetFrames(t *testing.T) {
	relay, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Open 5 streams
	for range 5 {
		_, err := relay.OpenStream(ctx)
		require.NoError(t, err)
		_, err = agent.AcceptStream(ctx)
		require.NoError(t, err)
	}

	assert.Equal(t, 5, relay.NumStreams())

	// Close the session (bulk teardown). This calls s.Reset(0) on
	// each stream internally. The onReset callback should detect
	// closeCh is closed and skip sending individual STREAM_RESET
	// frames.
	relay.Close()

	assert.Equal(t, 0, relay.NumStreams())

	// Agent should eventually see the transport close.
	// The key assertion: no panic, no deadlock.
	time.Sleep(200 * time.Millisecond)
}

func TestMuxSession_ResetHighChurn(t *testing.T) {
	relay, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Open and reset 50 streams (same pattern as production traffic)
	for i := range 50 {
		s, err := relay.OpenStream(ctx)
		require.NoError(t, err, "open iteration %d", i)

		_, err = agent.AcceptStream(ctx)
		require.NoError(t, err, "accept iteration %d", i)

		s.(*StreamSession).Reset(0)
		time.Sleep(20 * time.Millisecond)
	}

	// Wait for all remote cleanup
	time.Sleep(300 * time.Millisecond)

	assert.Equal(t, 0, relay.NumStreams(),
		"all relay streams must be cleaned up after reset")
	assert.Equal(t, 0, agent.NumStreams(),
		"all agent streams must be cleaned up after reset")

	// Open 50 more -- should all succeed (proves no leak)
	for i := range 50 {
		s, err := relay.OpenStream(ctx)
		require.NoError(t, err, "second round open iteration %d", i)

		_, err = agent.AcceptStream(ctx)
		require.NoError(t, err, "second round accept iteration %d", i)

		s.(*StreamSession).Reset(0)
		time.Sleep(20 * time.Millisecond)
	}

	time.Sleep(300 * time.Millisecond)

	assert.Equal(t, 0, relay.NumStreams())
	assert.Equal(t, 0, agent.NumStreams())
}

func TestMuxSession_StreamIDHighChurn(t *testing.T) {
	relay, agent := newMuxPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Open and close 20 streams with both sides closing -- verify recycling
	for i := range 20 {
		s, err := relay.OpenStream(ctx)
		require.NoError(t, err, "iteration %d", i)

		as, err := agent.AcceptStream(ctx)
		require.NoError(t, err, "iteration %d accept", i)

		s.Close()
		time.Sleep(50 * time.Millisecond)
		as.Close()
		time.Sleep(50 * time.Millisecond)
	}

	// With recycling, nextStreamID should be much less than 40
	// (without recycling: 1, 3, 5, ..., 39 = nextStreamID 41)
	relay.mu.Lock()
	nextID := relay.nextStreamID
	relay.mu.Unlock()

	assert.Less(t, nextID, uint32(40),
		"with recycling, nextStreamID should not reach 40 after 20 cycles")
}

// --- SERVICE_LIST (0x0E) tests ---

func TestMuxSession_ServiceListFrame(t *testing.T) {
	relay, agent := newMuxPair(t)

	// Agent emits the service list.
	err := agent.SendServiceList([]string{"samba", "http", "api"})
	require.NoError(t, err)

	select {
	case services := <-relay.ServiceListCh():
		assert.Equal(t, []string{"samba", "http", "api"}, services)
	case <-time.After(1 * time.Second):
		t.Fatal("relay did not receive service list")
	}
}

func TestMuxSession_ServiceListFrame_FiltersEmpty(t *testing.T) {
	relay, _ := newMuxPair(t)

	// Inject a raw frame containing empty segments to simulate
	// a malformed agent. Empty strings must be filtered out.
	relay.handleServiceList(&Frame{
		Version:  ProtocolVersion,
		Command:  CmdServiceList,
		StreamID: 0,
		Payload:  []byte("samba\n\nhttp\n"),
	})

	select {
	case services := <-relay.ServiceListCh():
		assert.Equal(t, []string{"samba", "http"}, services)
	case <-time.After(1 * time.Second):
		t.Fatal("service list not delivered")
	}
}

func TestMuxSession_ServiceListFrame_EmptyPayload(t *testing.T) {
	relay, _ := newMuxPair(t)

	relay.handleServiceList(&Frame{
		Version:  ProtocolVersion,
		Command:  CmdServiceList,
		StreamID: 0,
		Payload:  nil,
	})

	select {
	case services := <-relay.ServiceListCh():
		assert.Empty(t, services, "empty payload should deliver empty slice")
	case <-time.After(1 * time.Second):
		t.Fatal("service list not delivered for empty payload")
	}
}

func TestMuxSession_ServiceListFrame_NonBlocking(t *testing.T) {
	relay, _ := newMuxPair(t)

	// First send fills the buffer-of-1.
	relay.handleServiceList(&Frame{Command: CmdServiceList, Payload: []byte("first")})

	// Second send must drop silently rather than block.
	done := make(chan struct{})
	go func() {
		relay.handleServiceList(&Frame{Command: CmdServiceList, Payload: []byte("second")})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("handleServiceList blocked when channel was full")
	}

	// Receiver should see only the first.
	services := <-relay.ServiceListCh()
	assert.Equal(t, []string{"first"}, services)
}

func TestMuxSession_SendServiceList_EnqueuesControlPriority(t *testing.T) {
	// End-to-end: agent sends SERVICE_LIST, relay receives on ServiceListCh.
	// This exercises SendServiceList -> writeQueue -> wire -> codec -> handleFrame.
	relay, agent := newMuxPair(t)

	// Pre-fill the agent's data queue with frames destined for the relay.
	// The SERVICE_LIST (control priority) must still be delivered promptly.
	for i := 0; i < 3; i++ {
		agent.writeQueue.Enqueue(&Frame{
			Version:  ProtocolVersion,
			Command:  CmdPing, // benign; relay will respond with PONG
			StreamID: 0,
			Payload:  []byte{0, 0, 0, 0, 0, 0, 0, byte(i)},
		}, PriorityControl)
	}

	require.NoError(t, agent.SendServiceList([]string{"samba", "http"}))

	select {
	case services := <-relay.ServiceListCh():
		assert.Equal(t, []string{"samba", "http"}, services)
	case <-time.After(2 * time.Second):
		t.Fatal("SendServiceList did not deliver to peer")
	}
}

func TestMuxSession_SendServiceList_FramePayloadFormat(t *testing.T) {
	// Unit test: confirm SendServiceList emits a CmdServiceList frame
	// with newline-joined payload and StreamID=0. Uses a muxer with no
	// transport attached by consuming frames before the writeLoop can.
	c1, _ := net.Pipe()
	m := &MuxSession{
		transport:      c1,
		codec:          NewFrameCodec(),
		config:         defaultMuxConfig(),
		role:           RoleAgent,
		logger:         slog.Default(),
		streams:        make(map[uint32]*StreamSession),
		acceptCh:       make(chan *StreamSession, 1),
		closeCh:        make(chan struct{}),
		writeQueue:     NewWriteQueue(),
		pendingOpen:    make(map[uint32]chan struct{}),
		connSendWindow: NewFlowWindow(1048576),
		serviceListCh:  make(chan []string, 1),
	}
	// Note: no readLoop/writeLoop started. Queue stays put.
	defer c1.Close()

	require.NoError(t, m.SendServiceList([]string{"samba", "http"}))

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	f, err := m.writeQueue.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, CmdServiceList, f.Command)
	assert.Equal(t, uint32(0), f.StreamID)
	assert.Equal(t, "samba\nhttp", string(f.Payload))
}

func TestMuxSession_SendServiceList_Empty(t *testing.T) {
	// Empty slice produces an empty-payload frame. The caller in the agent
	// is responsible for skipping the send when it has no services.
	c1, _ := net.Pipe()
	m := &MuxSession{
		transport:      c1,
		codec:          NewFrameCodec(),
		config:         defaultMuxConfig(),
		role:           RoleAgent,
		logger:         slog.Default(),
		streams:        make(map[uint32]*StreamSession),
		acceptCh:       make(chan *StreamSession, 1),
		closeCh:        make(chan struct{}),
		writeQueue:     NewWriteQueue(),
		pendingOpen:    make(map[uint32]chan struct{}),
		connSendWindow: NewFlowWindow(1048576),
		serviceListCh:  make(chan []string, 1),
	}
	defer c1.Close()

	require.NoError(t, m.SendServiceList(nil))

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	f, err := m.writeQueue.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, CmdServiceList, f.Command)
	assert.Empty(t, f.Payload)
}

func TestMuxSession_ServiceListFrame_DroppedOnClose(t *testing.T) {
	relay, _ := newMuxPair(t)
	relay.Close()

	// Sending on the close path should not panic; channel send remains
	// non-blocking. The purpose of this test is to ensure SendServiceList
	// errs gracefully (no runtime panic).
	_ = relay.SendServiceList([]string{"svc"})
}
