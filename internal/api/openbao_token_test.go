package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/benaskins/aurelia/internal/config"
	"github.com/benaskins/aurelia/internal/daemon"
	"github.com/benaskins/aurelia/internal/keychain"
)

func TestOpenbaoTokenRequiresMTLS(t *testing.T) {
	d := daemon.NewDaemon(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := d.Start(ctx); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() { d.Stop(5 * time.Second) })

	srv := NewServer(d, nil)

	// Request without peer identity (simulates CLI/bearer token client)
	req := httptest.NewRequest("POST", "/v1/openbao/token", nil)
	ctx = context.WithValue(req.Context(), peerIdentityKey, "cli")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	srv.openbaoToken(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for CLI client, got %d", w.Code)
	}
}

func TestOpenbaoTokenRejectsUnknownNode(t *testing.T) {
	d := daemon.NewDaemon(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := d.Start(ctx); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() { d.Stop(5 * time.Second) })

	srv := NewServer(d, nil)

	// Configure vendor with known nodes (no "rogue")
	fakeBao := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not reach OpenBao for unknown node")
	}))
	defer fakeBao.Close()

	store := keychain.NewBaoStore(fakeBao.URL, "root-token", "secret")
	vendor := keychain.NewBaoTokenVendor(store)
	srv.SetTokenVendor(vendor, []config.Node{
		{Name: "hestia"},
		{Name: "limen"},
	})

	req := httptest.NewRequest("POST", "/v1/openbao/token", nil)
	ctx = context.WithValue(req.Context(), peerIdentityKey, "rogue")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	srv.openbaoToken(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for unknown node, got %d", w.Code)
	}
}

func TestOpenbaoTokenVendsForKnownNode(t *testing.T) {
	d := daemon.NewDaemon(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := d.Start(ctx); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() { d.Stop(5 * time.Second) })

	srv := NewServer(d, nil)

	// Fake OpenBao that returns a token
	fakeBao := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth/token/create" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.Error(w, "not found", 404)
			return
		}
		if r.Header.Get("X-Vault-Token") != "root-token" {
			t.Errorf("expected root token in header, got %q", r.Header.Get("X-Vault-Token"))
		}

		var body struct {
			Policies []string `json:"policies"`
			TTL      string   `json:"ttl"`
		}
		json.NewDecoder(r.Body).Decode(&body)

		if len(body.Policies) != 1 || body.Policies[0] != "node-hestia" {
			t.Errorf("expected policy [node-hestia], got %v", body.Policies)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"auth": map[string]any{
				"client_token":   "s.short-lived-token",
				"policies":       []string{"default", "node-hestia"},
				"lease_duration": 60,
			},
		})
	}))
	defer fakeBao.Close()

	store := keychain.NewBaoStore(fakeBao.URL, "root-token", "secret")
	vendor := keychain.NewBaoTokenVendor(store)
	srv.SetTokenVendor(vendor, []config.Node{
		{Name: "hestia"},
		{Name: "limen"},
	})

	req := httptest.NewRequest("POST", "/v1/openbao/token", nil)
	ctx = context.WithValue(req.Context(), peerIdentityKey, "hestia")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	srv.openbaoToken(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp keychain.BaoTokenResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Token != "s.short-lived-token" {
		t.Errorf("expected token s.short-lived-token, got %q", resp.Token)
	}
	if len(resp.Policies) != 2 {
		t.Errorf("expected 2 policies, got %v", resp.Policies)
	}
}

func TestOpenbaoTokenNotConfigured(t *testing.T) {
	d := daemon.NewDaemon(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := d.Start(ctx); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() { d.Stop(5 * time.Second) })

	srv := NewServer(d, nil)
	// No vendor configured

	req := httptest.NewRequest("POST", "/v1/openbao/token", nil)
	ctx = context.WithValue(req.Context(), peerIdentityKey, "hestia")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	srv.openbaoToken(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when vendor not configured, got %d", w.Code)
	}
}
