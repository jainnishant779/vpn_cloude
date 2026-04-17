package middleware

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// bucket tracks token-bucket state per key.
type bucket struct {
	tokens    float64
	lastRefil time.Time
}

// RateLimiter applies a token-bucket rate limit keyed on a request attribute.
type RateLimiter struct {
	rate     float64       // tokens per second
	capacity float64       // max burst
	ttl      time.Duration // bucket eviction TTL

	mu      sync.Mutex
	buckets map[string]*bucket
	lastGC  time.Time
}

// NewRateLimiter creates a rate limiter allowing `rate` req/s with burst of `burst`.
func NewRateLimiter(rate, burst float64) *RateLimiter {
	return &RateLimiter{
		rate:     rate,
		capacity: burst,
		ttl:      5 * time.Minute,
		buckets:  make(map[string]*bucket),
		lastGC:   time.Now(),
	}
}

// Allow returns true if the key has tokens remaining.
func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Periodic GC to prevent unbounded growth.
	if time.Since(rl.lastGC) > rl.ttl {
		rl.gc()
	}

	b, ok := rl.buckets[key]
	now := time.Now()
	if !ok {
		rl.buckets[key] = &bucket{tokens: rl.capacity - 1, lastRefil: now}
		return true
	}

	elapsed := now.Sub(b.lastRefil).Seconds()
	b.tokens = min64(b.tokens+elapsed*rl.rate, rl.capacity)
	b.lastRefil = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (rl *RateLimiter) gc() {
	now := time.Now()
	for key, b := range rl.buckets {
		if now.Sub(b.lastRefil) > rl.ttl {
			delete(rl.buckets, key)
		}
	}
	rl.lastGC = now
}

func min64(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// keyFunc extracts the limiting key from a request.
type keyFunc func(r *http.Request) string

// RemoteIPKey extracts the client IP from the request RemoteAddr after the
// real-IP middleware has already normalized it.
func RemoteIPKey(r *http.Request) string {
	if host := hostOnly(r.RemoteAddr); host != "" {
		return host
	}
	return r.RemoteAddr
}

// Limit returns middleware that rate-limits using `fn` as the key extractor.
func (rl *RateLimiter) Limit(fn keyFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := fn(r)
			if !rl.Allow(key) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"success": false,
					"data":    nil,
					"error":   "rate limit exceeded",
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
