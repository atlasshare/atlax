package agent

import (
	"math"
	"math/rand/v2"
	"time"
)

// BackoffConfig controls exponential backoff with jitter.
type BackoffConfig struct {
	InitialInterval time.Duration
	MaxInterval     time.Duration
	Multiplier      float64
	JitterFraction  float64 // fraction of base added as random jitter [0, base*fraction]
}

// DefaultBackoffConfig returns a sensible default: 5s initial, 5m max,
// 2x multiplier, 50% jitter.
func DefaultBackoffConfig() BackoffConfig {
	return BackoffConfig{
		InitialInterval: 5 * time.Second,
		MaxInterval:     5 * time.Minute,
		Multiplier:      2.0,
		JitterFraction:  0.5,
	}
}

// ComputeBackoff returns the backoff duration for the given attempt.
// The base delay is InitialInterval * Multiplier^attempt, capped at
// MaxInterval. Jitter adds a random duration in [0, base*JitterFraction].
func ComputeBackoff(cfg BackoffConfig, attempt int) time.Duration {
	base := float64(cfg.InitialInterval) * math.Pow(cfg.Multiplier, float64(attempt))
	if base > float64(cfg.MaxInterval) {
		base = float64(cfg.MaxInterval)
	}

	if cfg.JitterFraction <= 0 {
		return time.Duration(base)
	}

	jitter := rand.Float64() * base * cfg.JitterFraction //nolint:gosec // jitter does not need crypto randomness
	return time.Duration(base + jitter)
}
