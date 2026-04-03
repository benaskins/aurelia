package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/benaskins/aurelia/internal/daemon"
	"github.com/benaskins/aurelia/internal/keychain"
)

func TestSecretGetFromCache(t *testing.T) {
	srv, client := setupTestServer(t, nil)

	inner := keychain.NewMemoryStore()
	inner.Set("api-key", "secret-value")
	cache := keychain.NewCachedStore(inner, 5*time.Minute)
	srv.SetSecretCache(cache)

	resp, err := client.Get("http://aurelia/v1/secrets/api-key")
	if err != nil {
		t.Fatalf("GET /v1/secrets/api-key: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Value != "secret-value" {
		t.Errorf("expected secret-value, got %q", body.Value)
	}
}

func TestSecretGetNotFound(t *testing.T) {
	srv, client := setupTestServer(t, nil)

	inner := keychain.NewMemoryStore()
	cache := keychain.NewCachedStore(inner, 5*time.Minute)
	srv.SetSecretCache(cache)

	resp, err := client.Get("http://aurelia/v1/secrets/missing")
	if err != nil {
		t.Fatalf("GET /v1/secrets/missing: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestSecretGetNoCacheConfigured(t *testing.T) {
	_, client := setupTestServer(t, nil)

	resp, err := client.Get("http://aurelia/v1/secrets/any")
	if err != nil {
		t.Fatalf("GET /v1/secrets/any: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", resp.StatusCode)
	}
}

// --- Tessera endpoints (mTLS-only) ---

func newTestServerDirect(t *testing.T) *Server {
	t.Helper()
	d := daemon.NewDaemon(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := d.Start(ctx); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() { d.Stop(5 * time.Second) })
	return NewServer(d, nil)
}

func TestSecretsListRequiresMTLS(t *testing.T) {
	srv := newTestServerDirect(t)

	inner := keychain.NewMemoryStore()
	inner.Set("a", "1")
	cache := keychain.NewCachedStore(inner, 5*time.Minute)
	srv.SetSecretCache(cache)

	req := httptest.NewRequest("GET", "/v1/secrets", nil)
	ctx := context.WithValue(req.Context(), peerIdentityKey, "cli")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	srv.secretsList(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for CLI client, got %d", w.Code)
	}
}

func TestSecretsListReturnsBulk(t *testing.T) {
	srv := newTestServerDirect(t)

	inner := keychain.NewMemoryStore()
	inner.Set("api-key", "secret1")
	inner.Set("db-url", "secret2")
	cache := keychain.NewCachedStore(inner, 5*time.Minute)
	srv.SetSecretCache(cache)

	req := httptest.NewRequest("GET", "/v1/secrets", nil)
	ctx := context.WithValue(req.Context(), peerIdentityKey, "limen")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	srv.secretsList(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["api-key"] != "secret1" {
		t.Errorf("expected secret1, got %q", body["api-key"])
	}
	if body["db-url"] != "secret2" {
		t.Errorf("expected secret2, got %q", body["db-url"])
	}
}

func TestCacheInvalidateRequiresMTLS(t *testing.T) {
	srv := newTestServerDirect(t)

	req := httptest.NewRequest("POST", "/v1/cache/invalidate", bytes.NewReader([]byte(`{"key":"x"}`)))
	ctx := context.WithValue(req.Context(), peerIdentityKey, "cli")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	srv.cacheInvalidate(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for CLI client, got %d", w.Code)
	}
}

func TestCacheInvalidateEvictsKey(t *testing.T) {
	srv := newTestServerDirect(t)

	inner := keychain.NewMemoryStore()
	inner.Set("api-key", "v1")
	cache := keychain.NewCachedStore(inner, 5*time.Minute)
	srv.SetSecretCache(cache)

	// Warm cache
	cache.Get("api-key")

	// Change inner directly
	inner.Set("api-key", "v2")

	// Push invalidation
	body, _ := json.Marshal(map[string]string{"key": "api-key"})
	req := httptest.NewRequest("POST", "/v1/cache/invalidate", bytes.NewReader(body))
	ctx := context.WithValue(req.Context(), peerIdentityKey, "adyton")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	srv.cacheInvalidate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Next get should fetch from inner
	val, _ := cache.Get("api-key")
	if val != "v2" {
		t.Errorf("expected v2 after invalidation, got %q", val)
	}
}

func TestCacheInvalidateAll(t *testing.T) {
	srv := newTestServerDirect(t)

	inner := keychain.NewMemoryStore()
	inner.Set("a", "1")
	inner.Set("b", "2")
	cache := keychain.NewCachedStore(inner, 5*time.Minute)
	srv.SetSecretCache(cache)

	// Warm cache
	cache.Get("a")
	cache.Get("b")

	// Change inner
	inner.Set("a", "10")
	inner.Set("b", "20")

	// Push invalidate-all (no key field)
	req := httptest.NewRequest("POST", "/v1/cache/invalidate", bytes.NewReader([]byte(`{}`)))
	ctx := context.WithValue(req.Context(), peerIdentityKey, "adyton")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	srv.cacheInvalidate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	val, _ := cache.Get("a")
	if val != "10" {
		t.Errorf("expected 10, got %q", val)
	}
	val, _ = cache.Get("b")
	if val != "20" {
		t.Errorf("expected 20, got %q", val)
	}
}
