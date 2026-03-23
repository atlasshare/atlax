package protocol

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	defaultWindowSize = int32(262144) // 256KB per-stream default
)

func TestFlowWindow_NewWithDefaultSize(t *testing.T) {
	w := NewFlowWindow(defaultWindowSize)
	assert.Equal(t, defaultWindowSize, w.Available())
}

func TestFlowWindow_NewWithCustomSize(t *testing.T) {
	w := NewFlowWindow(1024)
	assert.Equal(t, int32(1024), w.Available())
}

func TestFlowWindow_ConsumeReducesAvailable(t *testing.T) {
	w := NewFlowWindow(1000)
	err := w.Consume(context.Background(), 300)
	require.NoError(t, err)
	assert.Equal(t, int32(700), w.Available())
}

func TestFlowWindow_ConsumeMultiple(t *testing.T) {
	w := NewFlowWindow(1000)

	require.NoError(t, w.Consume(context.Background(), 200))
	require.NoError(t, w.Consume(context.Background(), 300))
	assert.Equal(t, int32(500), w.Available())
}

func TestFlowWindow_ConsumeBlocksWhenExhausted(t *testing.T) {
	w := NewFlowWindow(100)
	require.NoError(t, w.Consume(context.Background(), 100))
	assert.Equal(t, int32(0), w.Available())

	consumed := make(chan error, 1)
	go func() {
		consumed <- w.Consume(context.Background(), 50)
	}()

	// Should not complete immediately
	select {
	case <-consumed:
		t.Fatal("Consume should block when window is exhausted")
	case <-time.After(50 * time.Millisecond):
		// expected: still blocked
	}

	// Unblock by updating window
	require.NoError(t, w.Update(50))

	select {
	case err := <-consumed:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Consume should have unblocked after Update")
	}

	assert.Equal(t, int32(0), w.Available())
}

func TestFlowWindow_ConsumeRespectsContextCancellation(t *testing.T) {
	w := NewFlowWindow(100)
	require.NoError(t, w.Consume(context.Background(), 100))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := w.Consume(ctx, 50)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestFlowWindow_UpdateIncrementsAvailable(t *testing.T) {
	w := NewFlowWindow(1000)
	require.NoError(t, w.Consume(context.Background(), 500))
	require.NoError(t, w.Update(200))
	assert.Equal(t, int32(700), w.Available())
}

func TestFlowWindow_UpdateRejectsZeroIncrement(t *testing.T) {
	w := NewFlowWindow(1000)
	err := w.Update(0)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrZeroWindowIncrement)
}

func TestFlowWindow_UpdateRejectsNegativeIncrement(t *testing.T) {
	w := NewFlowWindow(1000)
	err := w.Update(-1)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrZeroWindowIncrement)
}

func TestFlowWindow_UpdateRejectsOverflow(t *testing.T) {
	maxWindow := int32(2147483647) // 2^31 - 1
	w := NewFlowWindow(maxWindow)
	err := w.Update(1)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrWindowOverflow)
	// Available should not change on overflow
	assert.Equal(t, maxWindow, w.Available())
}

func TestFlowWindow_UpdateOverflowBoundary(t *testing.T) {
	w := NewFlowWindow(2147483600)
	// Increment that would push past 2^31-1
	err := w.Update(100)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrWindowOverflow)
}

func TestFlowWindow_UpdateExactlyMaxWindow(t *testing.T) {
	w := NewFlowWindow(2147483600)
	// Increment that reaches exactly 2^31-1
	err := w.Update(47)
	require.NoError(t, err)
	assert.Equal(t, int32(2147483647), w.Available())
}

func TestFlowWindow_ConcurrentConsumeAndUpdate(t *testing.T) {
	w := NewFlowWindow(10000)

	var wg sync.WaitGroup
	ctx := context.Background()

	// 10 goroutines consuming 100 each = 1000 total
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				require.NoError(t, w.Consume(ctx, 10))
			}
		}()
	}

	// 5 goroutines updating 200 each = 1000 total
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				require.NoError(t, w.Update(20))
			}
		}()
	}

	wg.Wait()
	// Net: consumed 1000, updated 1000
	assert.Equal(t, int32(10000), w.Available())
}

func TestFlowWindow_AvailableNeverNegative(t *testing.T) {
	w := NewFlowWindow(100)
	require.NoError(t, w.Consume(context.Background(), 100))
	assert.Equal(t, int32(0), w.Available())
	// Available should never go below 0
	assert.True(t, w.Available() >= 0)
}

func TestFlowWindow_Reset(t *testing.T) {
	w := NewFlowWindow(1000)
	require.NoError(t, w.Consume(context.Background(), 500))
	assert.Equal(t, int32(500), w.Available())

	w.Reset()
	assert.Equal(t, int32(1000), w.Available())
}

func TestFlowWindow_ConsumeUnblocksOnReset(t *testing.T) {
	w := NewFlowWindow(100)
	require.NoError(t, w.Consume(context.Background(), 100))

	consumed := make(chan error, 1)
	go func() {
		consumed <- w.Consume(context.Background(), 50)
	}()

	// Give goroutine time to block
	time.Sleep(20 * time.Millisecond)
	w.Reset()

	select {
	case err := <-consumed:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Consume should have unblocked after Reset")
	}
}

func BenchmarkWindowConsume(b *testing.B) {
	w := NewFlowWindow(int32(b.N) + 1)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = w.Consume(ctx, 1)
	}
}

func BenchmarkWindowUpdate(b *testing.B) {
	w := NewFlowWindow(1)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = w.Consume(ctx, 1)
		_ = w.Update(1)
	}
}
