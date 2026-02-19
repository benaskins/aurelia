package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeSpec(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestDaemonStartStop(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, "echo.yaml", `
service:
  name: echo
  type: native
  command: "sleep 10"
`)

	d := NewDaemon(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	states := d.ServiceStates()
	if len(states) != 1 {
		t.Fatalf("expected 1 service, got %d", len(states))
	}
	if states[0].Name != "echo" {
		t.Errorf("expected service name 'echo', got %q", states[0].Name)
	}

	d.Stop(5 * time.Second)
}

func TestDaemonServiceState(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, "test.yaml", `
service:
  name: test-svc
  type: native
  command: "sleep 10"
`)

	d := NewDaemon(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(5 * time.Second)

	// Get existing service
	state, err := d.ServiceState("test-svc")
	if err != nil {
		t.Fatalf("ServiceState: %v", err)
	}
	if state.Name != "test-svc" {
		t.Errorf("expected name 'test-svc', got %q", state.Name)
	}

	// Get non-existent service
	_, err = d.ServiceState("nope")
	if err == nil {
		t.Error("expected error for non-existent service")
	}
}

func TestDaemonStartStopService(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, "svc.yaml", `
service:
  name: managed
  type: native
  command: "sleep 10"
`)

	d := NewDaemon(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(5 * time.Second)

	// Wait for process to actually start
	time.Sleep(100 * time.Millisecond)

	// Stop it
	if err := d.StopService("managed", 5*time.Second); err != nil {
		t.Fatalf("StopService: %v", err)
	}

	state, _ := d.ServiceState("managed")
	if state.State != "stopped" {
		t.Errorf("expected stopped, got %v", state.State)
	}

	// Start it again
	if err := d.StartService(ctx, "managed"); err != nil {
		t.Fatalf("StartService: %v", err)
	}

	// Give it a moment to start
	time.Sleep(50 * time.Millisecond)

	state, _ = d.ServiceState("managed")
	if state.State != "running" {
		t.Errorf("expected running, got %v", state.State)
	}
}

func TestDaemonReload(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, "alpha.yaml", `
service:
  name: alpha
  type: native
  command: "sleep 10"
`)

	d := NewDaemon(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(5 * time.Second)

	// Add a new spec and remove the old one
	os.Remove(filepath.Join(dir, "alpha.yaml"))
	writeSpec(t, dir, "beta.yaml", `
service:
  name: beta
  type: native
  command: "sleep 10"
`)

	result, err := d.Reload(ctx)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}

	if len(result.Added) != 1 || result.Added[0] != "beta" {
		t.Errorf("expected added=[beta], got %v", result.Added)
	}
	if len(result.Removed) != 1 || result.Removed[0] != "alpha" {
		t.Errorf("expected removed=[alpha], got %v", result.Removed)
	}

	// Verify state
	states := d.ServiceStates()
	if len(states) != 1 {
		t.Fatalf("expected 1 service after reload, got %d", len(states))
	}
	if states[0].Name != "beta" {
		t.Errorf("expected 'beta', got %q", states[0].Name)
	}
}

func TestDaemonReloadNoChanges(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, "stable.yaml", `
service:
  name: stable
  type: native
  command: "sleep 10"
`)

	d := NewDaemon(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(5 * time.Second)

	result, err := d.Reload(ctx)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}

	if len(result.Added) != 0 || len(result.Removed) != 0 {
		t.Errorf("expected no changes, got added=%v removed=%v", result.Added, result.Removed)
	}
}

func TestDaemonEmptyDir(t *testing.T) {
	dir := t.TempDir()

	d := NewDaemon(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	states := d.ServiceStates()
	if len(states) != 0 {
		t.Errorf("expected 0 services, got %d", len(states))
	}

	d.Stop(5 * time.Second)
}
