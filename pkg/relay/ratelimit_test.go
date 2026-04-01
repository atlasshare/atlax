package relay

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestIPRateLimiter_AllowUnderLimit(t *testing.T) {
	rl := NewIPRateLimiter(10, 5) // 10 rps, burst 5
	defer rl.Stop()

	// First 5 requests should pass (burst)
	for range 5 {
		assert.True(t, rl.Allow("192.168.1.1"))
	}
}

func TestIPRateLimiter_RejectsOverLimit(t *testing.T) {
	rl := NewIPRateLimiter(1, 1) // 1 rps, burst 1
	defer rl.Stop()

	// First request passes
	assert.True(t, rl.Allow("192.168.1.1"))

	// Second immediate request should fail (burst exhausted)
	assert.False(t, rl.Allow("192.168.1.1"))
}

func TestIPRateLimiter_IndependentIPs(t *testing.T) {
	rl := NewIPRateLimiter(1, 1)
	defer rl.Stop()

	// Exhaust IP A
	assert.True(t, rl.Allow("10.0.0.1"))
	assert.False(t, rl.Allow("10.0.0.1"))

	// IP B should still have capacity
	assert.True(t, rl.Allow("10.0.0.2"))
}

func TestIPRateLimiter_Len(t *testing.T) {
	rl := NewIPRateLimiter(10, 5)
	defer rl.Stop()

	assert.Equal(t, 0, rl.Len())

	rl.Allow("10.0.0.1")
	assert.Equal(t, 1, rl.Len())

	rl.Allow("10.0.0.2")
	assert.Equal(t, 2, rl.Len())

	// Same IP does not increase count
	rl.Allow("10.0.0.1")
	assert.Equal(t, 2, rl.Len())
}

func TestIPRateLimiter_RefillsOverTime(t *testing.T) {
	rl := NewIPRateLimiter(100, 1) // 100 rps, burst 1
	defer rl.Stop()

	assert.True(t, rl.Allow("10.0.0.1"))
	assert.False(t, rl.Allow("10.0.0.1"))

	// Wait for refill (100 rps = 10ms per token)
	time.Sleep(20 * time.Millisecond)
	assert.True(t, rl.Allow("10.0.0.1"))
}

func TestIPRateLimiter_SweepRemovesStale(t *testing.T) {
	rl := NewIPRateLimiter(10, 5)
	defer rl.Stop()

	rl.Allow("10.0.0.1")
	assert.Equal(t, 1, rl.Len())

	// Manually backdate the entry to simulate staleness
	rl.mu.Lock()
	rl.entries["10.0.0.1"].lastSeen = time.Now().Add(-defaultStaleTimeout - time.Minute)
	rl.mu.Unlock()

	rl.sweep()
	assert.Equal(t, 0, rl.Len())
}

func TestIPRateLimiter_SweepKeepsFresh(t *testing.T) {
	rl := NewIPRateLimiter(10, 5)
	defer rl.Stop()

	rl.Allow("10.0.0.1")
	rl.sweep() // should not remove -- just created
	assert.Equal(t, 1, rl.Len())
}
