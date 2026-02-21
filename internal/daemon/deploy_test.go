package daemon

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeployServiceBasic(t *testing.T) {
	dir := t.TempDir()
	routingPath := filepath.Join(t.TempDir(), "traefik", "aurelia.yaml")

	// Start a simple HTTP health server — we'll use a shell command that
	// binds to $PORT and responds to /health.
	// For the test, use "sleep" as the command (no health check).
	writeSpec(t, dir, "chat.yaml", `
service:
  name: chat
  type: native
  command: "sleep 30"

network:
  port: 0

routing:
  hostname: chat.example.local
`)

	d := NewDaemon(dir, WithRouting(routingPath), WithPortRange(27000, 27100))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(5 * time.Second)

	// Wait for initial service to start
	time.Sleep(200 * time.Millisecond)

	stateBefore, err := d.ServiceState("chat")
	if err != nil {
		t.Fatalf("ServiceState: %v", err)
	}
	pidBefore := stateBefore.PID
	portBefore := stateBefore.Port

	// Deploy
	if err := d.DeployService("chat", 1*time.Second); err != nil {
		t.Fatalf("DeployService: %v", err)
	}

	// Wait for new instance to settle
	time.Sleep(200 * time.Millisecond)

	stateAfter, err := d.ServiceState("chat")
	if err != nil {
		t.Fatalf("ServiceState after deploy: %v", err)
	}

	// PID should change
	if stateAfter.PID == pidBefore && pidBefore != 0 {
		t.Error("expected PID to change after deploy")
	}

	// Port may change (temporary allocation)
	if stateAfter.Port == 0 {
		t.Error("expected non-zero port after deploy")
	}

	// State should be running
	if stateAfter.State != "running" {
		t.Errorf("expected running, got %v", stateAfter.State)
	}

	// Routing config should reference the new port
	data, err := os.ReadFile(routingPath)
	if err != nil {
		t.Fatalf("reading routing config: %v", err)
	}
	content := string(data)
	newPortStr := fmt.Sprintf("%d", stateAfter.Port)
	if !strings.Contains(content, newPortStr) {
		t.Errorf("routing config should contain new port %s:\n%s", newPortStr, content)
	}
	// Old port should NOT be in routing config (unless it's the same — possible but unlikely)
	if portBefore != stateAfter.Port {
		oldPortStr := fmt.Sprintf("%d", portBefore)
		if strings.Contains(content, oldPortStr) {
			t.Errorf("routing config should not contain old port %s:\n%s", oldPortStr, content)
		}
	}
}

func TestDeployServiceWithHealthCheck(t *testing.T) {
	dir := t.TempDir()
	routingPath := filepath.Join(t.TempDir(), "traefik", "aurelia.yaml")

	// Start a real HTTP server for health checks
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	healthPort := listener.Addr().(*net.TCPAddr).Port
	srv := &http.Server{Handler: mux}
	go srv.Serve(listener)
	defer srv.Close()

	// Use the health server's port as a static port, with http health check
	writeSpec(t, dir, "web.yaml", fmt.Sprintf(`
service:
  name: web
  type: native
  command: "sleep 30"

network:
  port: 0

routing:
  hostname: web.example.local

health:
  type: http
  path: /health
  port: %d
  interval: 100ms
  timeout: 2s
  grace_period: 100ms
  unhealthy_threshold: 2
`, healthPort))

	d := NewDaemon(dir, WithRouting(routingPath), WithPortRange(28000, 28100))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(5 * time.Second)

	time.Sleep(300 * time.Millisecond)

	// Deploy should succeed — health check against the real server
	if err := d.DeployService("web", 1*time.Second); err != nil {
		t.Fatalf("DeployService: %v", err)
	}

	stateAfter, _ := d.ServiceState("web")
	if stateAfter.State != "running" {
		t.Errorf("expected running after deploy, got %v", stateAfter.State)
	}
}

func TestDeployServiceConcurrentReject(t *testing.T) {
	dir := t.TempDir()
	routingPath := filepath.Join(t.TempDir(), "traefik", "aurelia.yaml")

	writeSpec(t, dir, "svc.yaml", `
service:
  name: svc
  type: native
  command: "sleep 30"

network:
  port: 0

routing:
  hostname: svc.example.local
`)

	d := NewDaemon(dir, WithRouting(routingPath), WithPortRange(29000, 29100))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(5 * time.Second)

	time.Sleep(200 * time.Millisecond)

	// Manually allocate the deploy temp port to simulate an in-progress deploy
	d.ports.AllocateTemporary("svc", deploySuffix)

	err := d.DeployService("svc", 1*time.Second)
	if err == nil {
		t.Error("expected error for concurrent deploy")
	}
	if !strings.Contains(err.Error(), "already in progress") {
		t.Errorf("expected 'already in progress' error, got: %v", err)
	}

	// Clean up
	d.ports.ReleaseTemporary("svc", deploySuffix)
}

func TestDeployServiceNoRouting(t *testing.T) {
	dir := t.TempDir()

	writeSpec(t, dir, "worker.yaml", `
service:
  name: worker
  type: native
  command: "sleep 30"
`)

	d := NewDaemon(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(5 * time.Second)

	time.Sleep(200 * time.Millisecond)

	pidBefore, _ := d.ServiceState("worker")

	// Deploy should fall back to restart (no routing)
	if err := d.DeployService("worker", 1*time.Second); err != nil {
		t.Fatalf("DeployService: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	pidAfter, _ := d.ServiceState("worker")
	if pidAfter.PID == pidBefore.PID && pidBefore.PID != 0 {
		t.Error("expected PID change after restart fallback")
	}
}

func TestDeployServiceNotFound(t *testing.T) {
	dir := t.TempDir()
	d := NewDaemon(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d.Start(ctx)
	defer d.Stop(5 * time.Second)

	err := d.DeployService("nonexistent", 1*time.Second)
	if err == nil {
		t.Error("expected error for nonexistent service")
	}
}
