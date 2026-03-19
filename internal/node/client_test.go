package node

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/benaskins/aurelia/internal/daemon"
)

func TestClientInjectsToken(t *testing.T) {
	t.Parallel()
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer srv.Close()

	c := New("test-node", srv.Listener.Addr().String(), "my-secret-token")
	if err := c.Health(); err != nil {
		t.Fatalf("Health() error: %v", err)
	}

	if gotAuth != "Bearer my-secret-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer my-secret-token")
	}
}

func TestClientStatus(t *testing.T) {
	t.Parallel()
	states := []daemon.ServiceState{
		{Name: "svc-a", State: "running"},
		{Name: "svc-b", State: "stopped"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/services" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(states)
	}))
	defer srv.Close()

	c := New("test-node", srv.Listener.Addr().String(), "tok")
	got, err := c.Status()
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Name != "svc-a" {
		t.Errorf("Name = %q, want %q", got[0].Name, "svc-a")
	}
}

func TestClientServiceLifecycle(t *testing.T) {
	t.Parallel()
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer srv.Close()

	c := New("test-node", srv.Listener.Addr().String(), "tok")

	tests := []struct {
		name   string
		fn     func(string) error
		path   string
		method string
	}{
		{"start", c.StartService, "/v1/services/foo/start", "POST"},
		{"stop", c.StopService, "/v1/services/foo/stop", "POST"},
		{"restart", c.RestartService, "/v1/services/foo/restart", "POST"},
		{"deploy", c.DeployService, "/v1/services/foo/deploy", "POST"},
	}

	for _, tt := range tests {
		if err := tt.fn("foo"); err != nil {
			t.Errorf("%s: unexpected error: %v", tt.name, err)
		}
		if gotPath != tt.path {
			t.Errorf("%s: path = %q, want %q", tt.name, gotPath, tt.path)
		}
		if gotMethod != tt.method {
			t.Errorf("%s: method = %q, want %q", tt.name, gotMethod, tt.method)
		}
	}
}

func TestClientReload(t *testing.T) {
	t.Parallel()
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer srv.Close()

	c := New("test-node", srv.Listener.Addr().String(), "tok")
	if err := c.ReloadService(); err != nil {
		t.Fatalf("ReloadService() error: %v", err)
	}
	if gotPath != "/v1/reload" {
		t.Errorf("path = %q, want /v1/reload", gotPath)
	}
}

func TestClientLogs(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/services/foo/logs" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("n") != "50" {
			t.Errorf("n = %q, want 50", r.URL.Query().Get("n"))
		}
		json.NewEncoder(w).Encode(map[string]any{"lines": []string{"line1", "line2"}})
	}))
	defer srv.Close()

	c := New("test-node", srv.Listener.Addr().String(), "tok")
	lines, err := c.Logs("foo", 50)
	if err != nil {
		t.Fatalf("Logs() error: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("len = %d, want 2", len(lines))
	}
}

func TestClientErrorResponse(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "boom"})
	}))
	defer srv.Close()

	c := New("test-node", srv.Listener.Addr().String(), "tok")
	if err := c.Health(); err == nil {
		t.Error("expected error for 500 response")
	}
}

func TestClientConnectionError(t *testing.T) {
	t.Parallel()
	c := New("test-node", "127.0.0.1:1", "tok") // nothing listening
	if err := c.Health(); err == nil {
		t.Error("expected error for connection failure")
	}
}
