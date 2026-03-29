package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestComputeBackoff_Attempt0(t *testing.T) {
	cfg := BackoffConfig{
		InitialInterval: 5 * time.Second,
		MaxInterval:     5 * time.Minute,
		Multiplier:      2.0,
		JitterFraction:  0,
	}
	d := ComputeBackoff(cfg, 0)
	assert.Equal(t, 5*time.Second, d)
}

func TestComputeBackoff_ExponentialGrowth(t *testing.T) {
	cfg := BackoffConfig{
		InitialInterval: 1 * time.Second,
		MaxInterval:     1 * time.Minute,
		Multiplier:      2.0,
		JitterFraction:  0,
	}
	assert.Equal(t, 1*time.Second, ComputeBackoff(cfg, 0))
	assert.Equal(t, 2*time.Second, ComputeBackoff(cfg, 1))
	assert.Equal(t, 4*time.Second, ComputeBackoff(cfg, 2))
	assert.Equal(t, 8*time.Second, ComputeBackoff(cfg, 3))
	assert.Equal(t, 16*time.Second, ComputeBackoff(cfg, 4))
}

func TestComputeBackoff_CapsAtMax(t *testing.T) {
	cfg := BackoffConfig{
		InitialInterval: 1 * time.Second,
		MaxInterval:     10 * time.Second,
		Multiplier:      2.0,
		JitterFraction:  0,
	}
	// 2^4 = 16s > 10s max
	assert.Equal(t, 10*time.Second, ComputeBackoff(cfg, 4))
	assert.Equal(t, 10*time.Second, ComputeBackoff(cfg, 10))
}

func TestComputeBackoff_JitterWithinBounds(t *testing.T) {
	cfg := BackoffConfig{
		InitialInterval: 1 * time.Second,
		MaxInterval:     1 * time.Minute,
		Multiplier:      2.0,
		JitterFraction:  0.5,
	}
	for i := 0; i < 100; i++ {
		d := ComputeBackoff(cfg, 0)
		// base = 1s, jitter in [0, 0.5s], so result in [1s, 1.5s]
		assert.GreaterOrEqual(t, d, 1*time.Second)
		assert.LessOrEqual(t, d, 1500*time.Millisecond)
	}
}

func TestComputeBackoff_ZeroJitter(t *testing.T) {
	cfg := BackoffConfig{
		InitialInterval: 1 * time.Second,
		MaxInterval:     1 * time.Minute,
		Multiplier:      2.0,
		JitterFraction:  0,
	}
	// Without jitter, result is deterministic
	d1 := ComputeBackoff(cfg, 2)
	d2 := ComputeBackoff(cfg, 2)
	assert.Equal(t, d1, d2)
	assert.Equal(t, 4*time.Second, d1)
}

func TestDefaultBackoffConfig(t *testing.T) {
	cfg := DefaultBackoffConfig()
	assert.Equal(t, 5*time.Second, cfg.InitialInterval)
	assert.Equal(t, 5*time.Minute, cfg.MaxInterval)
	assert.Equal(t, 2.0, cfg.Multiplier)
	assert.Equal(t, 0.5, cfg.JitterFraction)
}

func BenchmarkComputeBackoff(b *testing.B) {
	cfg := DefaultBackoffConfig()
	for b.Loop() {
		ComputeBackoff(cfg, 5)
	}
}
