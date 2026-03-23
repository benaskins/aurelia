package diagnose

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	tool "github.com/benaskins/axon-tool"
)

// testAPIClient wraps an httptest.Server to implement APIClient.
type testAPIClient struct {
	server *httptest.Server
}

func (c *testAPIClient) Get(path string) (*http.Response, error) {
	return http.Get(c.server.URL + path)
}

func (c *testAPIClient) Post(path string) (*http.Response, error) {
	return http.Post(c.server.URL+path, "application/json", nil)
}

func setupTestAPI(t *testing.T, handler http.Handler) APIClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &testAPIClient{server: server}
}

func TestReadToolsReturnsAllTools(t *testing.T) {
	t.Parallel()
	client := setupTestAPI(t, http.NotFoundHandler())
	tools := ReadTools(client)

	expected := []string{"list_services", "get_service", "inspect_service", "get_logs", "get_gpu", "cluster_services", "test_health_check", "get_health_check_history"}
	for _, name := range expected {
		if _, ok := tools[name]; !ok {
			t.Errorf("missing tool %q", name)
		}
	}
	if len(tools) != len(expected) {
		t.Errorf("got %d tools, want %d", len(tools), len(expected))
	}
}

func TestActionToolsReturnsAllActions(t *testing.T) {
	t.Parallel()
	client := setupTestAPI(t, http.NotFoundHandler())
	tools := ActionTools(client, nil)

	expected := []string{"restart_service", "start_service", "stop_service"}
	for _, name := range expected {
		if _, ok := tools[name]; !ok {
			t.Errorf("missing action tool %q", name)
		}
	}
}

func TestAllToolsCombinesReadAndAction(t *testing.T) {
	t.Parallel()
	client := setupTestAPI(t, http.NotFoundHandler())
	tools := AllTools(client, nil)

	if len(tools) != 11 {
		t.Errorf("got %d tools, want 11", len(tools))
	}
}

func TestListServicesTool(t *testing.T) {
	t.Parallel()
	services := []map[string]any{
		{"name": "chat", "state": "running", "health": "healthy"},
		{"name": "auth", "state": "running", "health": "healthy"},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/services", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(services)
	})

	client := setupTestAPI(t, mux)
	tools := ReadTools(client)
	result := tools["list_services"].Execute(&tool.ToolContext{}, nil)

	var got []map[string]any
	if err := json.Unmarshal([]byte(result.Content), &got); err != nil {
		t.Fatalf("failed to parse result: %v\ncontent: %s", err, result.Content)
	}
	if len(got) != 2 {
		t.Errorf("got %d services, want 2", len(got))
	}
}

func TestGetServiceTool(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/services/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"name": name, "state": "running"})
	})

	client := setupTestAPI(t, mux)
	tools := ReadTools(client)
	result := tools["get_service"].Execute(&tool.ToolContext{}, map[string]any{"name": "chat"})

	var got map[string]any
	if err := json.Unmarshal([]byte(result.Content), &got); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if got["name"] != "chat" {
		t.Errorf("name = %v, want chat", got["name"])
	}
}

func TestGetLogsTool(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/services/{name}/logs", func(w http.ResponseWriter, r *http.Request) {
		n := r.URL.Query().Get("n")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"lines": []string{"line1", "line2"}, "requested": n})
	})

	client := setupTestAPI(t, mux)
	tools := ReadTools(client)

	// Default lines
	result := tools["get_logs"].Execute(&tool.ToolContext{}, map[string]any{"name": "chat"})
	var got map[string]any
	if err := json.Unmarshal([]byte(result.Content), &got); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if got["requested"] != "100" {
		t.Errorf("requested = %v, want 100", got["requested"])
	}

	// Custom lines
	result2 := tools["get_logs"].Execute(&tool.ToolContext{}, map[string]any{"name": "chat", "lines": float64(50)})
	var got2 map[string]any
	if err := json.Unmarshal([]byte(result2.Content), &got2); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if got2["requested"] != "50" {
		t.Errorf("requested = %v, want 50", got2["requested"])
	}
}

func TestGetGPUTool(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/gpu", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"name": "Apple M2 Ultra", "vram_total_bytes": 196608000000})
	})

	client := setupTestAPI(t, mux)
	tools := ReadTools(client)
	result := tools["get_gpu"].Execute(&tool.ToolContext{}, nil)

	var got map[string]any
	if err := json.Unmarshal([]byte(result.Content), &got); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if got["name"] != "Apple M2 Ultra" {
		t.Errorf("name = %v, want Apple M2 Ultra", got["name"])
	}
}

func TestInspectServiceTool(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/services/{name}/inspect", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"name": name, "command": "/usr/bin/chat", "state": "running"})
	})

	client := setupTestAPI(t, mux)
	tools := ReadTools(client)
	result := tools["inspect_service"].Execute(&tool.ToolContext{}, map[string]any{"name": "chat"})

	var got map[string]any
	if err := json.Unmarshal([]byte(result.Content), &got); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if got["command"] != "/usr/bin/chat" {
		t.Errorf("command = %v, want /usr/bin/chat", got["command"])
	}
}

func TestClusterServicesTool(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/cluster/services", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"services": []map[string]any{{"name": "chat", "node": "aurelia"}},
			"peers":    map[string]string{"limen": "ok"},
		})
	})

	client := setupTestAPI(t, mux)
	tools := ReadTools(client)
	result := tools["cluster_services"].Execute(&tool.ToolContext{}, nil)

	var got map[string]any
	if err := json.Unmarshal([]byte(result.Content), &got); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if _, ok := got["services"]; !ok {
		t.Error("expected services field in response")
	}
	if _, ok := got["peers"]; !ok {
		t.Error("expected peers field in response")
	}
}

func TestActionToolApproved(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/services/{name}/restart", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "restarting"})
	})

	client := setupTestAPI(t, mux)
	confirm := func(action, service, reason string) bool { return true }
	tools := ActionTools(client, confirm)

	result := tools["restart_service"].Execute(&tool.ToolContext{}, map[string]any{
		"name":   "chat",
		"reason": "service is unhealthy",
	})

	if result.Content == "" {
		t.Fatal("expected non-empty result")
	}
	if !contains(result.Content, "Action executed") {
		t.Errorf("expected approved result, got: %s", result.Content)
	}
}

func TestActionToolRejected(t *testing.T) {
	t.Parallel()
	client := setupTestAPI(t, http.NotFoundHandler())
	confirm := func(action, service, reason string) bool { return false }
	tools := ActionTools(client, confirm)

	result := tools["restart_service"].Execute(&tool.ToolContext{}, map[string]any{
		"name":   "chat",
		"reason": "service is unhealthy",
	})

	if !contains(result.Content, "rejected") {
		t.Errorf("expected rejected result, got: %s", result.Content)
	}
}

func TestActionToolNilConfirm(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/services/{name}/start", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "starting"})
	})

	client := setupTestAPI(t, mux)
	tools := ActionTools(client, nil) // nil confirm = auto-approve

	result := tools["start_service"].Execute(&tool.ToolContext{}, map[string]any{
		"name":   "auth",
		"reason": "dependency needed",
	})

	if !contains(result.Content, "Action executed") {
		t.Errorf("nil confirm should auto-approve, got: %s", result.Content)
	}
}

func TestAPICallError(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/services/{name}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "service not found"})
	})

	client := setupTestAPI(t, mux)
	tools := ReadTools(client)
	result := tools["get_service"].Execute(&tool.ToolContext{}, map[string]any{"name": "nonexistent"})

	var got map[string]any
	if err := json.Unmarshal([]byte(result.Content), &got); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if _, ok := got["error"]; !ok {
		t.Error("expected error field in response")
	}
}

func TestHealthCheckTools(t *testing.T) {
	t.Parallel()

	healthResp := map[string]any{
		"status": "healthy",
		"history": []map[string]any{
			{"timestamp": "2026-03-23T10:00:00Z", "status": "healthy", "latency": 1500000},
			{"timestamp": "2026-03-23T10:00:30Z", "status": "unhealthy", "latency": 5000000000, "error": "connection refused"},
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/services/chat/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(healthResp)
	})

	client := setupTestAPI(t, mux)
	tools := ReadTools(client)

	// test_health_check
	result := tools["test_health_check"].Execute(nil, map[string]any{"name": "chat"})
	if !contains(result.Content, "healthy") {
		t.Errorf("test_health_check: expected 'healthy' in result, got %q", result.Content)
	}

	// get_health_check_history
	result = tools["get_health_check_history"].Execute(nil, map[string]any{"name": "chat"})
	if !contains(result.Content, "connection refused") {
		t.Errorf("get_health_check_history: expected error detail in result, got %q", result.Content)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstring(s, sub))
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
