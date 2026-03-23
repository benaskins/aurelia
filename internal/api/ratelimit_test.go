package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRateLimitAllowsNormalTraffic(t *testing.T) {
	rl := newRateLimitMiddleware()
	handler := rl.handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// A handful of requests should pass
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest("GET", "/v1/health", nil)
		ctx := context.WithValue(req.Context(), peerIdentityKey, "limen")
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i, w.Code)
		}
	}
}

func TestRateLimitRejectsBurst(t *testing.T) {
	rl := newRateLimitMiddleware()
	handler := rl.handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Exhaust the burst capacity
	rejected := 0
	for i := 0; i < rateLimitBurst+50; i++ {
		req := httptest.NewRequest("GET", "/v1/health", nil)
		ctx := context.WithValue(req.Context(), peerIdentityKey, "flood-peer")
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code == http.StatusTooManyRequests {
			rejected++
			if w.Header().Get("Retry-After") == "" {
				t.Error("expected Retry-After header on 429 response")
			}
		}
	}

	if rejected == 0 {
		t.Error("expected some requests to be rate limited after burst")
	}
}

func TestRateLimitPerSourceIsolation(t *testing.T) {
	rl := newRateLimitMiddleware()
	handler := rl.handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Exhaust peer-a's burst
	for i := 0; i < rateLimitBurst+10; i++ {
		req := httptest.NewRequest("GET", "/v1/health", nil)
		ctx := context.WithValue(req.Context(), peerIdentityKey, "peer-a")
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
	}

	// peer-b should still be allowed
	req := httptest.NewRequest("GET", "/v1/health", nil)
	ctx := context.WithValue(req.Context(), peerIdentityKey, "peer-b")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("peer-b should not be rate limited, got %d", w.Code)
	}
}
