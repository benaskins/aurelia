package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
		conn, err := net.Dial("unix", sockPath)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("unix", sockPath)
		},
	}
	t.Cleanup(func() { transport.CloseIdleConnections() })

	client := &http.Client{Transport: transport}

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

func TestExternalServiceAPIGuard(t *testing.T) {
	_, client := setupTestServer(t, map[string]string{
		"ext.yaml": `
service:
  name: ext-svc
  type: external

health:
  type: tcp
  port: 19876
  interval: 1s
  timeout: 500ms
`,
	})

	// start should be rejected
	resp, err := client.Post("http://aurelia/v1/services/ext-svc/start", "application/json", nil)
	if err != nil {
		t.Fatalf("POST start: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for start external, got %d", resp.StatusCode)
	}

	// stop should be rejected
	resp, err = client.Post("http://aurelia/v1/services/ext-svc/stop", "application/json", nil)
	if err != nil {
		t.Fatalf("POST stop: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for stop external, got %d", resp.StatusCode)
	}

	// restart should be rejected
	resp, err = client.Post("http://aurelia/v1/services/ext-svc/restart", "application/json", nil)
	if err != nil {
		t.Fatalf("POST restart: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for restart external, got %d", resp.StatusCode)
	}

	// deploy should be rejected
	resp, err = client.Post("http://aurelia/v1/services/ext-svc/deploy", "application/json", nil)
	if err != nil {
		t.Fatalf("POST deploy: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for deploy external, got %d", resp.StatusCode)
	}

	// GET (status) should still work
	resp, err = client.Get("http://aurelia/v1/services/ext-svc")
	if err != nil {
		t.Fatalf("GET service: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("expected 200 for GET external, got %d", resp.StatusCode)
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

func TestServiceLogsCapN(t *testing.T) {
	_, client := setupTestServer(t, map[string]string{
		"svc.yaml": `
service:
  name: log-svc
  type: native
  command: "echo hello"
`,
	})

	// Wait for process to run and produce output
	time.Sleep(200 * time.Millisecond)

	// Request an absurdly large number of lines — should be capped, not OOM
	resp, err := client.Get("http://aurelia/v1/services/log-svc/logs?n=999999999")
	if err != nil {
		t.Fatalf("GET logs: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	// The response should succeed without hanging or OOM
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result["lines"] == nil {
		t.Error("expected lines field in response")
	}
}

func TestListenTCPNonLoopbackWarning(t *testing.T) {
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

	var buf bytes.Buffer
	srv.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	go srv.ListenTCP("0.0.0.0:0")
	time.Sleep(100 * time.Millisecond)
	t.Cleanup(func() { srv.Shutdown(context.Background()) })

	if !strings.Contains(buf.String(), "non-loopback") {
		t.Errorf("expected non-loopback warning in logs, got: %s", buf.String())
	}
}

func TestListenTCPLoopbackNoWarning(t *testing.T) {
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

	var buf bytes.Buffer
	srv.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	go srv.ListenTCP("127.0.0.1:0")
	time.Sleep(100 * time.Millisecond)
	t.Cleanup(func() { srv.Shutdown(context.Background()) })

	if strings.Contains(buf.String(), "non-loopback") {
		t.Errorf("unexpected non-loopback warning for 127.0.0.1: %s", buf.String())
	}
}
