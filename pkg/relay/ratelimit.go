package relay

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	// defaultStaleTimeout is how long an IP entry stays in the limiter
	// after its last request before being cleaned up.
	defaultStaleTimeout = 10 * time.Minute

	// cleanupInterval is how often the background goroutine sweeps
	// stale entries.
	cleanupInterval = 1 * time.Minute
)

// IPRateLimiter tracks per-source-IP token bucket rate limiters.
type IPRateLimiter struct {
	rps   rate.Limit
	burst int

	mu      sync.Mutex
	entries map[string]*ipEntry
	stopCh  chan struct{}
}

type ipEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewIPRateLimiter creates a rate limiter that allows rps requests per
// second with the given burst size, per source IP.
func NewIPRateLimiter(rps float64, burst int) *IPRateLimiter {
	rl := &IPRateLimiter{
		rps:     rate.Limit(rps),
		burst:   burst,
		entries: make(map[string]*ipEntry),
		stopCh:  make(chan struct{}),
	}
	go rl.cleanupLoop()
	return rl
}

// Allow checks whether the given IP has capacity for one more request.
// Non-blocking: returns immediately.
func (rl *IPRateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	entry, ok := rl.entries[ip]
	if !ok {
		entry = &ipEntry{
			limiter: rate.NewLimiter(rl.rps, rl.burst),
		}
		rl.entries[ip] = entry
	}
	entry.lastSeen = time.Now()
	rl.mu.Unlock()

	return entry.limiter.Allow()
}

// Len returns the number of tracked IPs (for testing).
func (rl *IPRateLimiter) Len() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return len(rl.entries)
}

// Stop terminates the cleanup goroutine.
func (rl *IPRateLimiter) Stop() {
	close(rl.stopCh)
}

// cleanupLoop periodically removes IPs that have not been seen
// within the stale timeout.
func (rl *IPRateLimiter) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-rl.stopCh:
			return
		case <-ticker.C:
			rl.sweep()
		}
	}
}

func (rl *IPRateLimiter) sweep() {
	cutoff := time.Now().Add(-defaultStaleTimeout)
	rl.mu.Lock()
	defer rl.mu.Unlock()

	for ip, entry := range rl.entries {
		if entry.lastSeen.Before(cutoff) {
			delete(rl.entries, ip)
		}
	}
}
