package api

import (
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	rateLimitRate  = 20  // sustained requests per second
	rateLimitBurst = 100 // burst capacity
	limiterTTL     = 5 * time.Minute
)

type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// rateLimitMiddleware enforces per-source rate limits on incoming requests.
// Source identity is the peer identity from context (cert CN or "cli"),
// falling back to remote IP.
type rateLimitMiddleware struct {
	limiters sync.Map // string -> *limiterEntry
}

func newRateLimitMiddleware() *rateLimitMiddleware {
	rl := &rateLimitMiddleware{}
	go rl.cleanup()
	return rl
}

func (rl *rateLimitMiddleware) handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := PeerIdentity(r.Context())
		if key == "" {
			key = r.RemoteAddr
		}

		entry := rl.getLimiter(key)
		if !entry.limiter.Allow() {
			w.Header().Set("Retry-After", "1")
			writeJSON(w, http.StatusTooManyRequests, map[string]string{
				"error": "rate limit exceeded",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (rl *rateLimitMiddleware) getLimiter(key string) *limiterEntry {
	now := time.Now()
	if v, ok := rl.limiters.Load(key); ok {
		entry := v.(*limiterEntry)
		entry.lastSeen = now
		return entry
	}
	entry := &limiterEntry{
		limiter:  rate.NewLimiter(rate.Limit(rateLimitRate), rateLimitBurst),
		lastSeen: now,
	}
	actual, _ := rl.limiters.LoadOrStore(key, entry)
	return actual.(*limiterEntry)
}

func (rl *rateLimitMiddleware) cleanup() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-limiterTTL)
		rl.limiters.Range(func(key, value any) bool {
			entry := value.(*limiterEntry)
			if entry.lastSeen.Before(cutoff) {
				rl.limiters.Delete(key)
			}
			return true
		})
	}
}
