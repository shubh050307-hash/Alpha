// Package api — rate limiting middleware for Alpha Network API server.
// Uses a token bucket algorithm with per-IP and per-agent limits.
package api

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// freeIPRatePerMin is the default request cap for unauthenticated / unregistered callers.
	freeIPRatePerMin = 100
	// agentRatePerMin is the request cap for callers that provide a valid agent_id header.
	agentRatePerMin = 1000
	// bucketRefillInterval is how often the background reaper cleans stale buckets.
	bucketRefillInterval = 5 * time.Minute
)

// tokenBucket implements the token bucket algorithm.
// Tokens are replenished continuously; we use a "lazy" approach — on each
// request we compute how many tokens have accumulated since last access.
type tokenBucket struct {
	mu         sync.Mutex
	tokens     float64
	capacity   float64   // max tokens (= rate per minute)
	ratePerSec float64   // tokens added per second
	lastAccess time.Time
}

func newTokenBucket(ratePerMin float64) *tokenBucket {
	return &tokenBucket{
		tokens:     ratePerMin, // start full
		capacity:   ratePerMin,
		ratePerSec: ratePerMin / 60.0,
		lastAccess: time.Now(),
	}
}

// allow returns true and drains one token if a request is permitted.
// Returns false and the number of seconds until the next token is available otherwise.
func (b *tokenBucket) allow() (ok bool, retryAfterSec int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(b.lastAccess).Seconds()
	b.lastAccess = now

	// Replenish
	b.tokens += elapsed * b.ratePerSec
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}

	if b.tokens >= 1.0 {
		b.tokens--
		return true, 0
	}

	// Calculate wait time until next token
	waitSec := (1.0 - b.tokens) / b.ratePerSec
	return false, int(waitSec) + 1
}

// RateLimiter manages per-IP and per-agent token buckets.
type RateLimiter struct {
	mu       sync.RWMutex
	ipBuckets    map[string]*tokenBucket
	agentBuckets map[string]*tokenBucket
	stopCh   chan struct{}
}

// NewRateLimiter creates and starts a RateLimiter.
func NewRateLimiter() *RateLimiter {
	rl := &RateLimiter{
		ipBuckets:    make(map[string]*tokenBucket),
		agentBuckets: make(map[string]*tokenBucket),
		stopCh:       make(chan struct{}),
	}
	go rl.reapLoop()
	return rl
}

// Stop halts the background reaper goroutine.
func (rl *RateLimiter) Stop() {
	close(rl.stopCh)
}

// reapLoop removes stale buckets every bucketRefillInterval to prevent unbounded growth.
func (rl *RateLimiter) reapLoop() {
	ticker := time.NewTicker(bucketRefillInterval)
	defer ticker.Stop()
	for {
		select {
		case <-rl.stopCh:
			return
		case <-ticker.C:
			rl.reap()
		}
	}
}

func (rl *RateLimiter) reap() {
	cutoff := time.Now().Add(-bucketRefillInterval)
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for k, b := range rl.ipBuckets {
		b.mu.Lock()
		idle := b.lastAccess.Before(cutoff)
		b.mu.Unlock()
		if idle {
			delete(rl.ipBuckets, k)
		}
	}
	for k, b := range rl.agentBuckets {
		b.mu.Lock()
		idle := b.lastAccess.Before(cutoff)
		b.mu.Unlock()
		if idle {
			delete(rl.agentBuckets, k)
		}
	}
}

// ipBucket returns (or creates) the token bucket for the given IP address.
func (rl *RateLimiter) ipBucket(ip string) *tokenBucket {
	rl.mu.RLock()
	b, ok := rl.ipBuckets[ip]
	rl.mu.RUnlock()
	if ok {
		return b
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if b, ok = rl.ipBuckets[ip]; ok {
		return b
	}
	b = newTokenBucket(freeIPRatePerMin)
	rl.ipBuckets[ip] = b
	return b
}

// agentBucket returns (or creates) the token bucket for the given agent ID.
func (rl *RateLimiter) agentBucket(id string) *tokenBucket {
	rl.mu.RLock()
	b, ok := rl.agentBuckets[id]
	rl.mu.RUnlock()
	if ok {
		return b
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if b, ok = rl.agentBuckets[id]; ok {
		return b
	}
	b = newTokenBucket(agentRatePerMin)
	rl.agentBuckets[id] = b
	return b
}

// extractIP extracts the real client IP from the request, honouring
// X-Forwarded-For and X-Real-IP headers for proxy deployments.
func extractIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.SplitN(xff, ",", 2)
		ip := strings.TrimSpace(parts[0])
		if ip != "" {
			return ip
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	// Fall back to RemoteAddr, strip port
	addr := r.RemoteAddr
	if i := strings.LastIndex(addr, ":"); i != -1 {
		return addr[:i]
	}
	return addr
}

// Middleware wraps an http.Handler with rate limiting.
// If the caller provides a non-empty "agent_id" header it gets the higher agent limit;
// otherwise the per-IP limit applies.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Phase 4: P2P, sync, and block endpoints are internal peer comm — no rate limiting
		if strings.HasPrefix(r.URL.Path, "/api/v1/blocks/") ||
			strings.HasPrefix(r.URL.Path, "/api/v1/peers") ||
			strings.HasPrefix(r.URL.Path, "/api/v1/sync") ||
			strings.HasPrefix(r.URL.Path, "/api/v1/p2p/") {
			next.ServeHTTP(w, r)
			return
		}

		agentID := strings.TrimSpace(r.Header.Get("agent_id"))

		var ok bool
		var retryAfter int

		if agentID != "" {
			ok, retryAfter = rl.agentBucket(agentID).allow()
		} else {
			ip := extractIP(r)
			ok, retryAfter = rl.ipBucket(ip).allow()
		}

		if !ok {
			w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprintf(w, `{"error":"rate limit exceeded","success":false,"retry_after":%d}`, retryAfter)
			return
		}

		next.ServeHTTP(w, r)
	})
}
