package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/benaskins/aurelia/internal/daemon"
)

func setupTestServer(t *testing.T, specs map[string]string) (*Server, *http.Client) {
	t.Helper()

	dir := t.TempDir()
	for name, content := range specs {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	d := daemon.NewDaemon(dir)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	if err := d.Start(ctx); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() { d.Stop(5 * time.Second) })

	// Wait for processes to start
	time.Sleep(100 * time.Millisecond)

	srv := NewServer(d, nil)

	// Use a random Unix socket
	sockPath := filepath.Join(t.TempDir(), "test.sock")
	go srv.ListenUnix(sockPath)
	t.Cleanup(func() { srv.Shutdown(context.Background()) })

	// Wait for socket to be ready
	for i := 0; i < 20; i++ {
		if _, err := net.Dial("unix", sockPath); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", sockPath)
			},
		},
	}

	return srv, client
}

func TestHealthEndpoint(t *testing.T) {
	_, client := setupTestServer(t, nil)

	resp, err := client.Get("http://aurelia/v1/health")
	if err != nil {
		t.Fatalf("GET /v1/health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	if result["status"] != "ok" {
		t.Errorf("expected status ok, got %q", result["status"])
	}
}

func TestListServices(t *testing.T) {
	_, client := setupTestServer(t, map[string]string{
		"svc.yaml": `
service:
  name: test-svc
  type: native
  command: "sleep 30"
`,
	})

	resp, err := client.Get("http://aurelia/v1/services")
	if err != nil {
		t.Fatalf("GET /v1/services: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var states []daemon.ServiceState
	json.NewDecoder(resp.Body).Decode(&states)
	if len(states) != 1 {
		t.Fatalf("expected 1 service, got %d", len(states))
	}
	if states[0].Name != "test-svc" {
		t.Errorf("expected 'test-svc', got %q", states[0].Name)
	}
}

func TestGetService(t *testing.T) {
	_, client := setupTestServer(t, map[string]string{
		"svc.yaml": `
service:
  name: my-svc
  type: native
  command: "sleep 30"
`,
	})

	// Existing service
	resp, err := client.Get("http://aurelia/v1/services/my-svc")
	if err != nil {
		t.Fatalf("GET /v1/services/my-svc: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var state daemon.ServiceState
	json.NewDecoder(resp.Body).Decode(&state)
	if state.Name != "my-svc" {
		t.Errorf("expected 'my-svc', got %q", state.Name)
	}

	// Non-existent service
	resp2, err := client.Get("http://aurelia/v1/services/nope")
	if err != nil {
		t.Fatalf("GET /v1/services/nope: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp2.StatusCode)
	}
}

func TestStopStartService(t *testing.T) {
	_, client := setupTestServer(t, map[string]string{
		"svc.yaml": `
service:
  name: ctl-svc
  type: native
  command: "sleep 30"
`,
	})

	// Stop
	resp, err := client.Post("http://aurelia/v1/services/ctl-svc/stop", "application/json", nil)
	if err != nil {
		t.Fatalf("POST stop: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != 202 {
		t.Errorf("expected 202, got %d", resp.StatusCode)
	}

	// Start
	resp2, err := client.Post("http://aurelia/v1/services/ctl-svc/start", "application/json", nil)
	if err != nil {
		t.Fatalf("POST start: %v", err)
	}
	resp2.Body.Close()

	if resp2.StatusCode != 202 {
		t.Errorf("expected 202, got %d", resp2.StatusCode)
	}
}

func TestRestartService(t *testing.T) {
	_, client := setupTestServer(t, map[string]string{
		"svc.yaml": `
service:
  name: rst-svc
  type: native
  command: "sleep 30"
`,
	})

	resp, err := client.Post("http://aurelia/v1/services/rst-svc/restart", "application/json", nil)
	if err != nil {
		t.Fatalf("POST restart: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != 202 {
		t.Errorf("expected 202, got %d", resp.StatusCode)
	}
}

func TestReload(t *testing.T) {
	_, client := setupTestServer(t, map[string]string{
		"svc.yaml": `
service:
  name: reload-svc
  type: native
  command: "sleep 30"
`,
	})

	resp, err := client.Post("http://aurelia/v1/reload", "application/json", nil)
	if err != nil {
		t.Fatalf("POST reload: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestTCPAuthRequired(t *testing.T) {
	d := daemon.NewDaemon(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	if err := d.Start(ctx); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() { d.Stop(5 * time.Second) })

	srv := NewServer(d, nil)
	tokenPath := filepath.Join(t.TempDir(), "api.token")
	if err := srv.GenerateToken(tokenPath); err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	// Start TCP listener on a random port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close() // free the port for ListenTCP

	go srv.ListenTCP(addr)
	t.Cleanup(func() { srv.Shutdown(context.Background()) })

	// Wait for TCP to be ready
	for i := 0; i < 20; i++ {
		if conn, err := net.Dial("tcp", addr); err == nil {
			conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	baseURL := fmt.Sprintf("http://%s", addr)

	// No token — should get 401
	resp, err := http.Get(baseURL + "/v1/health")
	if err != nil {
		t.Fatalf("GET without token: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("expected 401 without token, got %d", resp.StatusCode)
	}

	// Wrong token — should get 401
	req, _ := http.NewRequest("GET", baseURL+"/v1/health", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET with wrong token: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("expected 401 with wrong token, got %d", resp.StatusCode)
	}

	// Correct token — should get 200
	token, _ := os.ReadFile(tokenPath)
	req, _ = http.NewRequest("GET", baseURL+"/v1/health", nil)
	req.Header.Set("Authorization", "Bearer "+string(token))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET with correct token: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("expected 200 with correct token, got %d", resp.StatusCode)
	}
}

func TestTCPRequiresToken(t *testing.T) {
	srv := NewServer(daemon.NewDaemon(t.TempDir()), nil)
	err := srv.ListenTCP("127.0.0.1:0")
	if err == nil {
		t.Fatal("expected error when calling ListenTCP without GenerateToken")
	}
}
