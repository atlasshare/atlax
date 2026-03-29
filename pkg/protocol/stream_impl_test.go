package protocol

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func defaultStreamConfig() StreamConfig {
	return StreamConfig{
		InitialWindowSize: 262144, // 256KB
		MaxFrameSize:      16384,  // 16KB
	}
}

func TestStreamSession_NewStartsIdle(t *testing.T) {
	s := NewStreamSession(1, defaultStreamConfig())
	assert.Equal(t, StateIdle, s.State())
}

func TestStreamSession_ID(t *testing.T) {
	s := NewStreamSession(42, defaultStreamConfig())
	assert.Equal(t, uint32(42), s.ID())
}

func TestStreamSession_TransitionIdleToOpen(t *testing.T) {
	s := NewStreamSession(1, defaultStreamConfig())
	s.Open()
	assert.Equal(t, StateOpen, s.State())
}

func TestStreamSession_TransitionOpenToHalfClosedLocal(t *testing.T) {
	s := NewStreamSession(1, defaultStreamConfig())
	s.Open()
	require.NoError(t, s.Close())
	assert.Equal(t, StateHalfClosedLocal, s.State())
}

func TestStreamSession_TransitionOpenToHalfClosedRemote(t *testing.T) {
	s := NewStreamSession(1, defaultStreamConfig())
	s.Open()
	s.RemoteClose()
	assert.Equal(t, StateHalfClosedRemote, s.State())
}

func TestStreamSession_TransitionHalfClosedToFullyClosed(t *testing.T) {
	t.Run("LocalThenRemote", func(t *testing.T) {
		s := NewStreamSession(1, defaultStreamConfig())
		s.Open()
		require.NoError(t, s.Close())
		s.RemoteClose()
		assert.Equal(t, StateClosed, s.State())
	})

	t.Run("RemoteThenLocal", func(t *testing.T) {
		s := NewStreamSession(2, defaultStreamConfig())
		s.Open()
		s.RemoteClose()
		require.NoError(t, s.Close())
		assert.Equal(t, StateClosed, s.State())
	})
}

func TestStreamSession_ResetFromAnyState(t *testing.T) {
	states := []struct {
		name  string
		setup func(*StreamSession)
	}{
		{"Idle", func(s *StreamSession) {}},
		{"Open", func(s *StreamSession) { s.Open() }},
		{"HalfClosedLocal", func(s *StreamSession) {
			s.Open()
			_ = s.Close()
		}},
		{"HalfClosedRemote", func(s *StreamSession) {
			s.Open()
			s.RemoteClose()
		}},
	}

	for _, tc := range states {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStreamSession(1, defaultStreamConfig())
			tc.setup(s)
			s.Reset(0)
			assert.Equal(t, StateReset, s.State())
		})
	}
}

func TestStreamSession_WriteFailsWhenHalfClosedLocal(t *testing.T) {
	s := NewStreamSession(1, defaultStreamConfig())
	s.Open()
	require.NoError(t, s.Close())

	_, err := s.Write([]byte("data"))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrStreamClosed)
}

func TestStreamSession_WriteFailsWhenClosed(t *testing.T) {
	s := NewStreamSession(1, defaultStreamConfig())
	s.Open()
	require.NoError(t, s.Close())
	s.RemoteClose()

	_, err := s.Write([]byte("data"))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrStreamClosed)
}

func TestStreamSession_WriteFailsWhenReset(t *testing.T) {
	s := NewStreamSession(1, defaultStreamConfig())
	s.Open()
	s.Reset(0)

	_, err := s.Write([]byte("data"))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrStreamClosed)
}

func TestStreamSession_ReadReturnsEOFWhenHalfClosedRemote(t *testing.T) {
	s := NewStreamSession(1, defaultStreamConfig())
	s.Open()
	s.RemoteClose()

	buf := make([]byte, 10)
	_, err := s.Read(buf)
	assert.ErrorIs(t, err, io.EOF)
}

func TestStreamSession_ReadReturnsEOFWhenClosed(t *testing.T) {
	s := NewStreamSession(1, defaultStreamConfig())
	s.Open()
	require.NoError(t, s.Close())
	s.RemoteClose()

	buf := make([]byte, 10)
	_, err := s.Read(buf)
	assert.ErrorIs(t, err, io.EOF)
}

func TestStreamSession_ReadBlocksUntilDataAvailable(t *testing.T) {
	s := NewStreamSession(1, defaultStreamConfig())
	s.Open()

	readDone := make(chan struct{})
	var readBuf [5]byte
	var readN int
	var readErr error

	go func() {
		readN, readErr = s.Read(readBuf[:])
		close(readDone)
	}()

	// Should not complete immediately
	select {
	case <-readDone:
		t.Fatal("Read should block until data is available")
	case <-time.After(50 * time.Millisecond):
	}

	s.Deliver([]byte("hello"))

	select {
	case <-readDone:
		require.NoError(t, readErr)
		assert.Equal(t, 5, readN)
		assert.Equal(t, []byte("hello"), readBuf[:readN])
	case <-time.After(time.Second):
		t.Fatal("Read should have completed after Deliver")
	}
}

func TestStreamSession_ReadUnblocksOnClose(t *testing.T) {
	s := NewStreamSession(1, defaultStreamConfig())
	s.Open()

	readDone := make(chan error, 1)
	go func() {
		buf := make([]byte, 10)
		_, err := s.Read(buf)
		readDone <- err
	}()

	time.Sleep(20 * time.Millisecond)
	s.RemoteClose()

	select {
	case err := <-readDone:
		assert.ErrorIs(t, err, io.EOF)
	case <-time.After(time.Second):
		t.Fatal("Read should have unblocked on RemoteClose")
	}
}

func TestStreamSession_ReadUnblocksOnReset(t *testing.T) {
	s := NewStreamSession(1, defaultStreamConfig())
	s.Open()

	readDone := make(chan error, 1)
	go func() {
		buf := make([]byte, 10)
		_, err := s.Read(buf)
		readDone <- err
	}()

	time.Sleep(20 * time.Millisecond)
	s.Reset(0)

	select {
	case err := <-readDone:
		assert.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("Read should have unblocked on Reset")
	}
}

func TestStreamSession_ReadDrainsBufferBeforeEOF(t *testing.T) {
	s := NewStreamSession(1, defaultStreamConfig())
	s.Open()

	s.Deliver([]byte("buffered data"))
	s.RemoteClose()

	buf := make([]byte, 20)
	n, err := s.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "buffered data", string(buf[:n]))

	// Next read returns EOF
	_, err = s.Read(buf)
	assert.ErrorIs(t, err, io.EOF)
}

func TestStreamSession_WriteProducesFrames(t *testing.T) {
	s := NewStreamSession(1, defaultStreamConfig())
	s.Open()

	n, err := s.Write([]byte("hello"))
	require.NoError(t, err)
	assert.Equal(t, 5, n)
}

func TestStreamSession_ReceiveWindow(t *testing.T) {
	cfg := defaultStreamConfig()
	s := NewStreamSession(1, cfg)
	assert.Equal(t, cfg.InitialWindowSize, s.ReceiveWindow())
}

func TestStreamSession_ReceiveWindowDecrementsOnDeliver(t *testing.T) {
	cfg := defaultStreamConfig()
	s := NewStreamSession(1, cfg)
	s.Open()

	s.Deliver([]byte("12345"))
	assert.Equal(t, cfg.InitialWindowSize-5, s.ReceiveWindow())
}

func TestStreamSession_ConcurrentReadWrite(t *testing.T) {
	s := NewStreamSession(1, defaultStreamConfig())
	s.Open()

	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Drain goroutine (simulates MuxSession.drainStream)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case _, ok := <-s.WriteOut():
				if !ok {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// Writer goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			select {
			case <-ctx.Done():
				return
			default:
				_, _ = s.Write([]byte("w"))
			}
		}
	}()

	// Deliver goroutine (simulates mux read loop)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			select {
			case <-ctx.Done():
				return
			default:
				s.Deliver([]byte("r"))
			}
		}
		s.RemoteClose()
	}()

	// Reader goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 10)
		for {
			_, err := s.Read(buf)
			if err != nil {
				return
			}
		}
	}()

	wg.Wait()
}

func TestStreamSession_CloseIsIdempotent(t *testing.T) {
	s := NewStreamSession(1, defaultStreamConfig())
	s.Open()

	require.NoError(t, s.Close())
	// Second close should not panic and should return nil or error
	err := s.Close()
	assert.NoError(t, err)
}

// Compile-time interface check
var _ Stream = (*StreamSession)(nil)
