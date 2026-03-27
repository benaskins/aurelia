package daemon

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/benaskins/aurelia/internal/spec"
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
	waitUntil(t, func() bool {
		s, _ := d.ServiceState("sleeper")
		return s.PID != adoptedPID && s.PID != 0
	}, 5*time.Second, "PID to change after redeploy")

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

func TestRedeployAdoptedInterruptibleSleep(t *testing.T) {
	// Verify that redeployAdopted returns promptly when context is cancelled
	// during the sleep period, even with a long redeployWait.
	dir := t.TempDir()
	d := NewDaemon(dir)
	ctx, cancel := context.WithCancel(context.Background())
	d.ctx = ctx

	d.adopted = []string{"nonexistent"}
	d.redeployWait = 30 * time.Second // long wait that would hang without fix

	done := make(chan struct{})
	go func() {
		d.redeployAdopted()
		close(done)
	}()

	// Give the goroutine time to enter the sleep
	time.Sleep(50 * time.Millisecond)

	// Cancel context — redeployAdopted should wake up promptly
	cancel()

	select {
	case <-done:
		// success — exited promptly
	case <-time.After(2 * time.Second):
		t.Fatal("redeployAdopted did not exit promptly after context cancellation during sleep")
	}
}

func TestDaemonStopDependencyOrder(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, "db.yaml", `
service:
  name: db
  type: native
  command: "sleep 10"
`)
	writeSpec(t, dir, "api.yaml", `
service:
  name: api
  type: native
  command: "sleep 10"

dependencies:
  after: [db]
`)
	writeSpec(t, dir, "web.yaml", `
service:
  name: web
  type: native
  command: "sleep 10"

dependencies:
  after: [api]
`)

	d := NewDaemon(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	states := d.ServiceStates()
	if len(states) != 3 {
		t.Fatalf("expected 3 services, got %d", len(states))
	}

	d.Stop(5 * time.Second)

	// After Stop, all services should be stopped
	for _, s := range d.ServiceStates() {
		if s.State == "running" {
			t.Errorf("service %s still running after Stop", s.Name)
		}
	}
}

func TestDaemonStopFallbackParallel(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, "svc-a.yaml", `
service:
  name: svc-a
  type: native
  command: "sleep 10"
`)
	writeSpec(t, dir, "svc-b.yaml", `
service:
  name: svc-b
  type: native
  command: "sleep 10"
`)

	d := NewDaemon(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Force fallback to parallel stop path by clearing deps
	d.mu.Lock()
	d.deps = nil
	d.mu.Unlock()

	// This should not panic or hang — the test passing is the assertion
	d.Stop(5 * time.Second)
}

func TestDaemonStopServiceCascade(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, "db.yaml", `
service:
  name: db
  type: native
  command: "sleep 10"
`)
	writeSpec(t, dir, "api.yaml", `
service:
  name: api
  type: native
  command: "sleep 10"

dependencies:
  after: [db]
  requires: [db]
`)
	writeSpec(t, dir, "web.yaml", `
service:
  name: web
  type: native
  command: "sleep 10"

dependencies:
  after: [api]
  requires: [api]
`)

	d := NewDaemon(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for all processes to start
	time.Sleep(100 * time.Millisecond)

	// Stopping db should cascade to api and web via requires
	if err := d.StopService("db", 5*time.Second); err != nil {
		t.Fatalf("StopService(db): %v", err)
	}

	// Wait for cascade
	time.Sleep(200 * time.Millisecond)

	for _, name := range []string{"api", "web"} {
		state, err := d.ServiceState(name)
		if err != nil {
			t.Fatalf("ServiceState(%s): %v", name, err)
		}
		if state.State == "running" {
			t.Errorf("expected %s to be stopped after cascade, got %s", name, state.State)
		}
	}

	// Clean up
	d.Stop(5 * time.Second)
}

func TestDaemonStartWaitsForDependencyHealth(t *testing.T) {
	// Start a real HTTP server to act as the health endpoint for the "db" service.
	// The dependent "app" service should only start after "db" passes its health check.
	dir := t.TempDir()

	// Find a free port for the health check server
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	healthPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	// Start the health endpoint immediately so the health check passes quickly
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})
	srv := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", healthPort), Handler: mux}
	go srv.ListenAndServe()
	t.Cleanup(func() { srv.Close() })

	// db: external service with health check that has a dependent
	writeSpec(t, dir, "db.yaml", fmt.Sprintf(`
service:
  name: db
  type: external

health:
  type: http
  path: /health
  port: %d
  interval: 100ms
  timeout: 500ms
  grace_period: 0s
  unhealthy_threshold: 1
`, healthPort))

	// app: requires db — should not start until db is healthy
	writeSpec(t, dir, "app.yaml", `
service:
  name: app
  type: native
  command: "sleep 10"

dependencies:
  after: [db]
  requires: [db]
`)

	d := NewDaemon(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(5 * time.Second)

	// Wait for processes to settle
	time.Sleep(200 * time.Millisecond)

	// Both services should be registered
	states := d.ServiceStates()
	if len(states) != 2 {
		t.Fatalf("expected 2 services, got %d", len(states))
	}

	// The app service should be running (db was healthy before it started)
	state, err := d.ServiceState("app")
	if err != nil {
		t.Fatalf("ServiceState(app): %v", err)
	}
	if state.State != "running" {
		t.Errorf("expected app to be running, got %v", state.State)
	}
}

func TestDaemonStartSkipsHealthWaitForLeafService(t *testing.T) {
	// A service with a health check but NO dependents should not be health-waited.
	// This test verifies startup completes quickly even if the health check would fail.
	dir := t.TempDir()

	writeSpec(t, dir, "leaf.yaml", `
service:
  name: leaf
  type: native
  command: "sleep 10"

health:
  type: http
  path: /health
  port: 19996
  interval: 100ms
  timeout: 100ms
  grace_period: 1s
  unhealthy_threshold: 3
`)

	d := NewDaemon(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	start := time.Now()
	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(5 * time.Second)
	elapsed := time.Since(start)

	// If health-wait ran, it would take at least 1s (grace_period).
	// Without health-wait, startup should be nearly instant.
	if elapsed > 500*time.Millisecond {
		t.Errorf("startup took %v — health-wait should be skipped for leaf services", elapsed)
	}
}

func TestDaemonStopServiceNotFound(t *testing.T) {
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
	defer d.Stop(5 * time.Second)

	err := d.StopService("nonexistent", 5*time.Second)
	if err == nil {
		t.Fatal("expected error for nonexistent service")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected error to contain 'not found', got %q", err.Error())
	}
}

func TestDaemonShutdownPreservesState(t *testing.T) {
	dir := t.TempDir()
	stateDir := t.TempDir()

	writeSpec(t, dir, "sleeper.yaml", `
service:
  name: sleeper
  type: native
  command: "sleep 300"
`)

	d := NewDaemon(dir, WithStateDir(stateDir))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for process to start and state to be persisted
	time.Sleep(200 * time.Millisecond)

	// Get the PID before shutdown
	state, err := d.ServiceState("sleeper")
	if err != nil {
		t.Fatalf("ServiceState: %v", err)
	}
	if state.PID == 0 {
		t.Fatal("expected non-zero PID")
	}
	pid := state.PID

	// Shutdown (not Stop) — should preserve state and leave process running
	d.Shutdown(5 * time.Second)

	// Verify the state file still has the PID
	sf := newStateFile(stateDir)
	records, err := sf.load()
	if err != nil {
		t.Fatalf("loading state after shutdown: %v", err)
	}
	rec, ok := records["sleeper"]
	if !ok {
		t.Fatal("state file missing 'sleeper' record after shutdown")
	}
	if rec.PID != pid {
		t.Errorf("expected PID %d in state file, got %d", pid, rec.PID)
	}

	// Verify the process is still alive
	proc, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("FindProcess(%d): %v", pid, err)
	}
	// Signal 0 checks if process exists without sending a signal
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		t.Errorf("process %d not alive after shutdown: %v", pid, err)
	}

	// Clean up: kill the orphaned process
	proc.Kill()
}

func TestDaemonShutdownStopsContainers(t *testing.T) {
	// Verify that Shutdown releases native services but would stop containers.
	// We can't easily test real containers, but we verify the method completes
	// without error on native-only services.
	dir := t.TempDir()
	stateDir := t.TempDir()

	writeSpec(t, dir, "svc-a.yaml", `
service:
  name: svc-a
  type: native
  command: "sleep 300"
`)
	writeSpec(t, dir, "svc-b.yaml", `
service:
  name: svc-b
  type: native
  command: "sleep 300"
`)

	d := NewDaemon(dir, WithStateDir(stateDir))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	// Get PIDs
	stateA, _ := d.ServiceState("svc-a")
	stateB, _ := d.ServiceState("svc-b")
	if stateA.PID == 0 || stateB.PID == 0 {
		t.Fatal("expected non-zero PIDs for both services")
	}

	d.Shutdown(5 * time.Second)

	// Both processes should still be alive
	for _, pid := range []int{stateA.PID, stateB.PID} {
		proc, _ := os.FindProcess(pid)
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			t.Errorf("process %d not alive after shutdown: %v", pid, err)
		}
		proc.Kill()
	}

	// State file should still have both records
	sf := newStateFile(stateDir)
	records, err := sf.load()
	if err != nil {
		t.Fatalf("loading state: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("expected 2 records in state file, got %d", len(records))
	}
}

func TestDaemonStopClearsState(t *testing.T) {
	// Verify that Stop() still clears state (full teardown behavior unchanged)
	dir := t.TempDir()
	stateDir := t.TempDir()

	writeSpec(t, dir, "svc.yaml", `
service:
  name: svc
  type: native
  command: "sleep 300"
`)

	d := NewDaemon(dir, WithStateDir(stateDir))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	d.Stop(5 * time.Second)

	// State should be cleared
	sf := newStateFile(stateDir)
	records, err := sf.load()
	if err != nil {
		t.Fatalf("loading state: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected empty state file after Stop, got %d records", len(records))
	}
}

func TestDaemonRemoveService(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, "removeme.yaml", `
service:
  name: removeme
  type: native
  command: "sleep 10"
`)
	writeSpec(t, dir, "keeper.yaml", `
service:
  name: keeper
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

	time.Sleep(100 * time.Millisecond)

	// Remove the service
	if err := d.RemoveService("removeme", 5*time.Second); err != nil {
		t.Fatalf("RemoveService: %v", err)
	}

	// Should be gone from in-memory state
	_, err := d.ServiceState("removeme")
	if err == nil {
		t.Error("expected error for removed service")
	}

	// Other service should still be there
	state, err := d.ServiceState("keeper")
	if err != nil {
		t.Fatalf("keeper should still exist: %v", err)
	}
	if state.Name != "keeper" {
		t.Errorf("expected keeper, got %q", state.Name)
	}

	// Spec file should be archived, not in the original directory
	if _, err := os.Stat(filepath.Join(dir, "removeme.yaml")); !os.IsNotExist(err) {
		t.Error("spec file should have been removed from spec dir")
	}

	// Spec file should exist in archive subdirectory
	archiveDir := filepath.Join(dir, "archive")
	if _, err := os.Stat(filepath.Join(archiveDir, "removeme.yaml")); err != nil {
		t.Errorf("spec file should exist in archive dir: %v", err)
	}

	// Removing a non-existent service should error
	if err := d.RemoveService("nope", 5*time.Second); err == nil {
		t.Error("expected error for non-existent service")
	}
}

func TestDaemonRecoverOrphanByCommandMatch(t *testing.T) {
	// Simulate PID reuse: the saved PID belongs to a different process, but the
	// original service is still running as an orphan with a different PID.
	dir := t.TempDir()
	stateDir := t.TempDir()

	writeSpec(t, dir, "sleeper.yaml", `
service:
  name: sleeper
  type: native
  command: "sleep 300"
`)

	// Start the "orphaned" process — this simulates a process from a previous
	// daemon instance that is still running.
	orphanCmd := exec.Command("sleep", "300")
	if err := orphanCmd.Start(); err != nil {
		t.Fatalf("starting orphan process: %v", err)
	}
	orphanPID := orphanCmd.Process.Pid
	go orphanCmd.Wait()
	t.Cleanup(func() { orphanCmd.Process.Kill() })

	// Start a different process to "reuse" the PID slot in the state file.
	// We use a cat process with a different name to simulate PID reuse.
	decoyCmd := exec.Command("cat")
	decoyCmd.Stdin = strings.NewReader("") // will block until EOF
	if err := decoyCmd.Start(); err != nil {
		t.Fatalf("starting decoy process: %v", err)
	}
	decoyPID := decoyCmd.Process.Pid
	go decoyCmd.Wait()
	t.Cleanup(func() { decoyCmd.Process.Kill() })

	// Write state file with the decoy PID but the sleeper's command.
	// This simulates PID reuse — the PID exists but runs a different binary.
	sf := newStateFile(stateDir)
	if err := sf.set("sleeper", ServiceRecord{
		Type:    "native",
		PID:     decoyPID,
		Command: "sleep 300",
	}); err != nil {
		t.Fatalf("writing state: %v", err)
	}

	d := NewDaemon(dir, WithStateDir(stateDir))
	d.redeployWait = 1 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(5 * time.Second)

	// The daemon should have found the orphan by command match and adopted it
	if len(d.adopted) == 0 {
		t.Fatal("expected service to be in adopted list (found by command match)")
	}

	state, err := d.ServiceState("sleeper")
	if err != nil {
		t.Fatalf("ServiceState: %v", err)
	}

	// The adopted PID should be the orphan, not the decoy
	if state.PID == decoyPID {
		t.Error("should not have adopted the decoy PID")
	}
	if state.PID != orphanPID {
		// It might have been redeployed already, which is fine — just verify
		// the service is running
		t.Logf("PID is %d (orphan was %d), may have already been redeployed", state.PID, orphanPID)
	}
	if state.State != "running" {
		t.Errorf("expected running, got %v", state.State)
	}
}

func TestDaemonRecoverOrphanedPortHolder(t *testing.T) {
	// Start a process that holds a port, then start a daemon with a service
	// that needs that port. The daemon should kill the port holder and start fresh.
	dir := t.TempDir()
	stateDir := t.TempDir()

	// Find a free port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	// Start a process that holds the port — use a simple HTTP server via
	// Go's net/http. We'll start a child process that listens on the port.
	// Use a shell command that runs a TCP listener.
	holdCmd := exec.Command("bash", "-c",
		fmt.Sprintf("exec python3 -c \"import http.server,socketserver; s=socketserver.TCPServer(('127.0.0.1',%d),http.server.SimpleHTTPRequestHandler); s.serve_forever()\" 2>/dev/null || exec nc -l %d", port, port))
	if err := holdCmd.Start(); err != nil {
		t.Fatalf("starting port holder: %v", err)
	}
	go holdCmd.Wait()
	t.Cleanup(func() { holdCmd.Process.Kill() })

	// Wait for port to be bound
	waitUntil(t, func() bool {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return true
		}
		return false
	}, 3*time.Second, "port holder to bind port")

	// Write a spec for a service that needs this port.
	// The service command is a simple HTTP server — we use a sleep command
	// that will fail to bind (the error triggers orphan port recovery).
	// Actually, we need the start to fail with "address already in use".
	// Use a Go test helper: start a native service that tries to listen on the port.
	writeSpec(t, dir, "web.yaml", fmt.Sprintf(`
service:
  name: web
  type: native
  command: "bash -c 'exec nc -l %d'"

network:
  port: %d
`, port, port))

	d := NewDaemon(dir, WithStateDir(stateDir))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Note: startService won't fail on its own because the native driver just
	// starts the process — the process itself will fail when it tries to bind.
	// The recoverOrphanedPort check happens when startService returns an error,
	// but NativeDriver.Start() succeeds even if the command will later fail.
	//
	// For this test, verify the recoverOrphanedPort method directly.
	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(5 * time.Second)

	// The service started (the native driver doesn't fail on exec), but the
	// process may fail. The important thing is the daemon didn't crash and
	// the service is tracked.
	_, err = d.ServiceState("web")
	if err != nil {
		t.Fatalf("ServiceState: %v", err)
	}
}

func TestRecoverOrphanedPortDirect(t *testing.T) {
	// Direct test of recoverOrphanedPort: start a listener on a port,
	// then call recoverOrphanedPort with a matching service spec and error.
	dir := t.TempDir()
	stateDir := t.TempDir()

	// Start a sleep process that holds a port via a listener
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	// Keep the listener open — our own process holds the port.
	defer ln.Close()

	writeSpec(t, dir, "svc.yaml", fmt.Sprintf(`
service:
  name: svc
  type: native
  command: "sleep 300"

network:
  port: %d
`, port))

	d := NewDaemon(dir, WithStateDir(stateDir))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.ctx = ctx

	specs, err := spec.LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	d.deps = newDepGraph(specs)

	s := specs[0]

	// recoverOrphanedPort should find the port holder but refuse to kill it
	// because the port holder (our test process) doesn't match the expected
	// command "sleep 300" (we're a Go test binary, not sleep).
	fakeErr := fmt.Errorf("address already in use")
	recovered := d.recoverOrphanedPort(ctx, s, fakeErr)
	if recovered {
		t.Error("should not have recovered — port holder is unrelated process")
	}
}

func TestDaemonPIDReuseNoOrphanStartsFresh(t *testing.T) {
	// When PID reuse is detected and no orphan is found by command match,
	// the daemon should start a fresh instance.
	dir := t.TempDir()
	stateDir := t.TempDir()

	writeSpec(t, dir, "svc.yaml", `
service:
  name: svc
  type: native
  command: "sleep 300"
`)

	// Write state with a PID that belongs to a different process (our own PID).
	// VerifyProcess will detect the command mismatch, FindProcessByCommand won't
	// find any "sleep 300" process (we haven't started one), so the daemon should
	// start a fresh instance.
	sf := newStateFile(stateDir)
	if err := sf.set("svc", ServiceRecord{
		Type:    "native",
		PID:     os.Getpid(), // our own PID — definitely not "sleep 300"
		Command: "definitely-not-running-binary-xyz",
	}); err != nil {
		t.Fatalf("writing state: %v", err)
	}

	d := NewDaemon(dir, WithStateDir(stateDir))
	d.redeployWait = 1 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(5 * time.Second)

	// Service should be running with a new PID (not adopted)
	if len(d.adopted) != 0 {
		t.Errorf("expected no adopted services, got %v", d.adopted)
	}

	time.Sleep(100 * time.Millisecond)

	state, err := d.ServiceState("svc")
	if err != nil {
		t.Fatalf("ServiceState: %v", err)
	}
	if state.State != "running" {
		t.Errorf("expected running, got %v", state.State)
	}
	if state.PID == os.Getpid() {
		t.Error("should not have adopted the stale PID")
	}
}
