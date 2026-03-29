package protocol

import (
	"context"
	"encoding/binary"
	"io"
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
