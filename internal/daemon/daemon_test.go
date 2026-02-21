package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestDaemonReloadDetectsChangedSpec(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, "svc.yaml", `
service:
  name: svc
  type: native
  command: "sleep 10"

env:
  FOO: bar
`)

	d := NewDaemon(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(5 * time.Second)

	// Wait for process to start
	time.Sleep(100 * time.Millisecond)

	// Get PID before reload
	stateBefore, _ := d.ServiceState("svc")
	pidBefore := stateBefore.PID

	// Modify the spec (change env var)
	writeSpec(t, dir, "svc.yaml", `
service:
  name: svc
  type: native
  command: "sleep 10"

env:
  FOO: baz
`)

	result, err := d.Reload(ctx)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}

	if len(result.Restarted) != 1 || result.Restarted[0] != "svc" {
		t.Errorf("expected restarted=[svc], got %v", result.Restarted)
	}
	if len(result.Added) != 0 {
		t.Errorf("expected no added, got %v", result.Added)
	}
	if len(result.Removed) != 0 {
		t.Errorf("expected no removed, got %v", result.Removed)
	}

	// Wait for new process to start
	time.Sleep(100 * time.Millisecond)

	stateAfter, _ := d.ServiceState("svc")
	if stateAfter.PID == pidBefore && pidBefore != 0 {
		t.Error("expected PID to change after restart")
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

	if len(result.Added) != 0 || len(result.Removed) != 0 || len(result.Restarted) != 0 {
		t.Errorf("expected no changes, got added=%v removed=%v restarted=%v", result.Added, result.Removed, result.Restarted)
	}
}

func TestDaemonRoutingGeneration(t *testing.T) {
	dir := t.TempDir()
	routingPath := filepath.Join(t.TempDir(), "traefik", "aurelia.yaml")

	writeSpec(t, dir, "chat.yaml", `
service:
  name: chat
  type: native
  command: "sleep 30"

network:
  port: 8090

routing:
  hostname: chat.example.local
  tls: true
`)

	writeSpec(t, dir, "plain.yaml", `
service:
  name: plain
  type: native
  command: "sleep 30"
`)

	d := NewDaemon(dir, WithRouting(routingPath))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(5 * time.Second)

	// Wait for onStarted callback to fire
	time.Sleep(200 * time.Millisecond)

	// Check that routing config was generated
	data, err := os.ReadFile(routingPath)
	if err != nil {
		t.Fatalf("routing config not written: %v", err)
	}

	content := string(data)
	if !containsAll(content, "chat.example.local", "8090", "websecure") {
		t.Errorf("routing config missing expected content:\n%s", content)
	}
	// plain service has no routing — should not appear
	if containsAll(content, "plain") {
		t.Errorf("plain service should not appear in routing config:\n%s", content)
	}
}

func containsAll(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

func TestDaemonDynamicPort(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, "dynamic.yaml", `
service:
  name: dynamic-svc
  type: native
  command: "sleep 10"

network:
  port: 0
`)

	d := NewDaemon(dir, WithPortRange(25000, 25100))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(5 * time.Second)

	// Wait for process to start
	time.Sleep(100 * time.Millisecond)

	state, err := d.ServiceState("dynamic-svc")
	if err != nil {
		t.Fatalf("ServiceState: %v", err)
	}

	if state.Port < 25000 || state.Port > 25100 {
		t.Errorf("expected port in range 25000-25100, got %d", state.Port)
	}
	if state.State != "running" {
		t.Errorf("expected running, got %v", state.State)
	}
}

func TestDaemonDynamicPortRouting(t *testing.T) {
	dir := t.TempDir()
	routingPath := filepath.Join(t.TempDir(), "traefik", "aurelia.yaml")

	writeSpec(t, dir, "dynamic-routed.yaml", `
service:
  name: dynamic-routed
  type: native
  command: "sleep 30"

network:
  port: 0

routing:
  hostname: dynamic.example.local
  tls: true
`)

	d := NewDaemon(dir, WithRouting(routingPath), WithPortRange(26000, 26100))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(5 * time.Second)

	// Wait for onStarted callback to fire and routing to generate
	time.Sleep(200 * time.Millisecond)

	state, err := d.ServiceState("dynamic-routed")
	if err != nil {
		t.Fatalf("ServiceState: %v", err)
	}

	// Verify routing config was generated with the allocated port
	data, err := os.ReadFile(routingPath)
	if err != nil {
		t.Fatalf("routing config not written: %v", err)
	}

	content := string(data)
	portStr := fmt.Sprintf("%d", state.Port)
	if !containsAll(content, "dynamic.example.local", portStr) {
		t.Errorf("routing config missing hostname or allocated port %d:\n%s", state.Port, content)
	}
}

func TestDaemonExternalServiceShowsHealth(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, "ext.yaml", `
service:
  name: ext-svc
  type: external

health:
  type: tcp
  port: 19999
  interval: 100ms
  timeout: 50ms
  unhealthy_threshold: 2
`)

	d := NewDaemon(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(5 * time.Second)

	// Wait for health checks to run
	time.Sleep(500 * time.Millisecond)

	state, err := d.ServiceState("ext-svc")
	if err != nil {
		t.Fatalf("ServiceState: %v", err)
	}

	if state.Type != "external" {
		t.Errorf("expected type 'external', got %q", state.Type)
	}
	if state.State != "running" {
		t.Errorf("expected state 'running' for external service, got %q", state.State)
	}
	// Nothing listening on 19999 so health should be unhealthy
	if state.Health != "unhealthy" {
		t.Errorf("expected health 'unhealthy', got %q", state.Health)
	}
	if state.PID != 0 {
		t.Errorf("expected no PID for external service, got %d", state.PID)
	}
	if state.Port != 19999 {
		t.Errorf("expected port 19999 from health check, got %d", state.Port)
	}
}

func TestDaemonExternalServiceInDeps(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, "ext.yaml", `
service:
  name: ext-dep
  type: external

health:
  type: tcp
  port: 19998
  interval: 1s
  timeout: 500ms
`)
	writeSpec(t, dir, "app.yaml", `
service:
  name: app
  type: native
  command: "sleep 10"

dependencies:
  after: [ext-dep]
`)

	d := NewDaemon(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(5 * time.Second)

	// Both should be registered
	states := d.ServiceStates()
	if len(states) != 2 {
		t.Fatalf("expected 2 services, got %d", len(states))
	}
}

func TestRedeployAdoptedServices(t *testing.T) {
	dir := t.TempDir()
	stateDir := t.TempDir()

	writeSpec(t, dir, "sleeper.yaml", `
service:
  name: sleeper
  type: native
  command: "sleep 300"
`)

	// Start a standalone sleep process to simulate a process surviving a daemon crash.
	// We can't use daemon1 because exec.CommandContext kills the child on cancel.
	cmd := exec.Command("sleep", "300")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting sleep process: %v", err)
	}
	adoptedPID := cmd.Process.Pid
	// Reap the process in a goroutine so it doesn't become a zombie after SIGTERM.
	// kill(pid, 0) returns success for zombies, which would make the adopted
	// driver's poll loop never detect death.
	go cmd.Wait()
	t.Cleanup(func() { cmd.Process.Kill() })

	// Write state file as if a previous daemon was managing this process
	sf := newStateFile(stateDir)
	if err := sf.set("sleeper", ServiceRecord{
		Type:    "native",
		PID:     adoptedPID,
		Command: "sleep 300",
	}); err != nil {
		t.Fatalf("writing state: %v", err)
	}

	// Start daemon — it should adopt the running process, then redeploy it
	d := NewDaemon(dir, WithStateDir(stateDir))
	d.redeployWait = 1 * time.Millisecond // skip the normal 10s delay
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(5 * time.Second)

	// Verify the service was adopted
	if len(d.adopted) == 0 {
		t.Fatal("expected service to be in adopted list")
	}
	if d.adopted[0] != "sleeper" {
		t.Fatalf("expected adopted=[sleeper], got %v", d.adopted)
	}

	// Wait for redeploy to complete (redeployWait=1ms + stop/start cycle)
	time.Sleep(3 * time.Second)

	state, err := d.ServiceState("sleeper")
	if err != nil {
		t.Fatalf("ServiceState after redeploy: %v", err)
	}

	// After redeploy, PID should have changed (new process started)
	if state.PID == adoptedPID {
		t.Errorf("expected PID to change after redeploy, still %d", adoptedPID)
	}
	if state.State != "running" {
		t.Errorf("expected running after redeploy, got %v", state.State)
	}

	// Log capture should work now (NativeDriver, not AdoptedDriver)
	d.mu.RLock()
	ms := d.services["sleeper"]
	d.mu.RUnlock()
	logs := ms.Logs(10)
	// sleep produces no output, but LogLines should return empty slice, not nil
	// (NativeDriver returns []string{} from logbuf, AdoptedDriver returns nil)
	if logs == nil {
		t.Error("expected log capture to be restored (non-nil LogLines), got nil")
	}
}

func TestRedeployAdoptedSkipsExternal(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, "ext.yaml", `
service:
  name: ext-svc
  type: external

health:
  type: tcp
  port: 19997
  interval: 1s
  timeout: 500ms
`)

	d := NewDaemon(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(5 * time.Second)

	// External services are never adopted (adoption only triggers for native PIDs)
	if len(d.adopted) != 0 {
		t.Errorf("expected no adopted services for external type, got %v", d.adopted)
	}
}

func TestRedeployAdoptedDaemonShutdown(t *testing.T) {
	// Verify that redeployAdopted exits early when daemon context is cancelled
	dir := t.TempDir()
	d := NewDaemon(dir)
	ctx, cancel := context.WithCancel(context.Background())
	d.ctx = ctx

	// Populate adopted list with a name that doesn't exist in services —
	// if the loop runs, DeployService will fail. That's fine, we just check it doesn't hang.
	d.adopted = []string{"nonexistent"}
	d.redeployWait = 1 * time.Millisecond

	// Cancel context before redeploy runs
	cancel()

	done := make(chan struct{})
	go func() {
		d.redeployAdopted()
		close(done)
	}()

	select {
	case <-done:
		// success — exited promptly
	case <-time.After(2 * time.Second):
		t.Fatal("redeployAdopted did not exit after context cancellation")
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
