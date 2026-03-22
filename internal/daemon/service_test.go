package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/benaskins/aurelia/internal/driver"
	"github.com/benaskins/aurelia/internal/keychain"
	"github.com/benaskins/aurelia/internal/spec"
)

// waitUntil polls condition every 10ms until it returns true or timeout is reached.
func waitUntil(t *testing.T, condition func() bool, timeout time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting: %s", msg)
}

func TestManagedServiceStartStop(t *testing.T) {
	s := &spec.ServiceSpec{
		Service: spec.Service{
			Name:    "test-sleep",
			Type:    "native",
			Command: "sleep 60",
		},
		Restart: &spec.RestartPolicy{
			Policy: "never",
		},
	}

	ms, err := NewManagedService(s, nil)
	if err != nil {
		t.Fatalf("failed to create: %v", err)
	}

	ctx := context.Background()
	if err := ms.Start(ctx); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	waitUntil(t, func() bool {
		return ms.State().State == driver.StateRunning
	}, 2*time.Second, "state to become running")

	state := ms.State()
	if state.PID <= 0 {
		t.Errorf("expected positive PID, got %d", state.PID)
	}

	if err := ms.Stop(5 * time.Second); err != nil {
		t.Fatalf("failed to stop: %v", err)
	}

	state = ms.State()
	if state.State != driver.StateStopped {
		t.Errorf("expected stopped, got %v", state.State)
	}
}

func TestManagedServiceRestartOnFailure(t *testing.T) {
	s := &spec.ServiceSpec{
		Service: spec.Service{
			Name:    "test-restart",
			Type:    "native",
			Command: "false", // exits immediately with code 1
		},
		Restart: &spec.RestartPolicy{
			Policy:      "on-failure",
			MaxAttempts: 2,
			Delay:       spec.Duration{Duration: 10 * time.Millisecond},
		},
	}

	ms, err := NewManagedService(s, nil)
	if err != nil {
		t.Fatalf("failed to create: %v", err)
	}

	ctx := context.Background()
	if err := ms.Start(ctx); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	waitUntil(t, func() bool {
		return ms.State().RestartCount >= 1
	}, 2*time.Second, "at least 1 restart")

	state := ms.State()
	if state.RestartCount > 2 {
		t.Errorf("expected at most 2 restarts, got %d", state.RestartCount)
	}
}

func TestManagedServiceNoRestartOnCleanExit(t *testing.T) {
	s := &spec.ServiceSpec{
		Service: spec.Service{
			Name:    "test-clean",
			Type:    "native",
			Command: "true", // exits with code 0
		},
		Restart: &spec.RestartPolicy{
			Policy:      "on-failure",
			MaxAttempts: 3,
			Delay:       spec.Duration{Duration: 10 * time.Millisecond},
		},
	}

	ms, err := NewManagedService(s, nil)
	if err != nil {
		t.Fatalf("failed to create: %v", err)
	}

	ctx := context.Background()
	if err := ms.Start(ctx); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	// Wait for process to exit (it runs "true" which exits immediately)
	waitUntil(t, func() bool {
		return ms.State().State != driver.StateRunning
	}, 2*time.Second, "process to exit")

	// Give a small window to ensure no restarts fire
	time.Sleep(50 * time.Millisecond)

	state := ms.State()
	if state.RestartCount != 0 {
		t.Errorf("expected 0 restarts for clean exit, got %d", state.RestartCount)
	}
}

func TestManagedServiceAlwaysRestart(t *testing.T) {
	s := &spec.ServiceSpec{
		Service: spec.Service{
			Name:    "test-always",
			Type:    "native",
			Command: "true", // exits cleanly
		},
		Restart: &spec.RestartPolicy{
			Policy:      "always",
			MaxAttempts: 2,
			Delay:       spec.Duration{Duration: 10 * time.Millisecond},
		},
	}

	ms, err := NewManagedService(s, nil)
	if err != nil {
		t.Fatalf("failed to create: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := ms.Start(ctx); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	waitUntil(t, func() bool {
		return ms.State().RestartCount >= 1
	}, 2*time.Second, "at least 1 restart with 'always' policy")

	cancel()
	waitUntil(t, func() bool {
		s := ms.State().State
		return s == driver.StateStopped || s == driver.StateFailed
	}, 2*time.Second, "service to stop after cancel")

	state := ms.State()
	if state.RestartCount < 1 {
		t.Errorf("expected restarts with 'always' policy, got %d", state.RestartCount)
	}
}

func TestManagedServiceNeverRestart(t *testing.T) {
	s := &spec.ServiceSpec{
		Service: spec.Service{
			Name:    "test-never",
			Type:    "native",
			Command: "false",
		},
		Restart: &spec.RestartPolicy{
			Policy: "never",
		},
	}

	ms, err := NewManagedService(s, nil)
	if err != nil {
		t.Fatalf("failed to create: %v", err)
	}

	if err := ms.Start(context.Background()); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	waitUntil(t, func() bool {
		s := ms.State().State
		return s == driver.StateFailed || s == driver.StateStopped
	}, 2*time.Second, "process to exit")

	state := ms.State()
	if state.RestartCount != 0 {
		t.Errorf("expected 0 restarts with 'never' policy, got %d", state.RestartCount)
	}
}

func TestManagedServiceExponentialBackoff(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: exercises real backoff timing")
	}

	s := &spec.ServiceSpec{
		Service: spec.Service{
			Name:    "test-backoff",
			Type:    "native",
			Command: "false",
		},
		Restart: &spec.RestartPolicy{
			Policy:      "on-failure",
			MaxAttempts: 3,
			Delay:       spec.Duration{Duration: 50 * time.Millisecond},
			Backoff:     "exponential",
			MaxDelay:    spec.Duration{Duration: 500 * time.Millisecond},
		},
	}

	ms, err := NewManagedService(s, nil)
	if err != nil {
		t.Fatalf("failed to create: %v", err)
	}

	start := time.Now()
	if err := ms.Start(context.Background()); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	// Wait for all restarts to exhaust
	time.Sleep(1 * time.Second)

	elapsed := time.Since(start)
	// With 50ms base, exponential: 50ms + 100ms + 200ms = 350ms minimum
	// Should take at least 300ms (some slack for process startup)
	if elapsed < 300*time.Millisecond {
		t.Errorf("exponential backoff too fast, elapsed: %v", elapsed)
	}
}

func TestManagedServiceHealthState(t *testing.T) {
	// Start a service with an HTTP health check against a port nothing listens on
	s := &spec.ServiceSpec{
		Service: spec.Service{
			Name:    "test-health",
			Type:    "native",
			Command: "sleep 60",
		},
		Health: &spec.HealthCheck{
			Type:               "tcp",
			Port:               19876, // nothing listening
			Interval:           spec.Duration{Duration: 50 * time.Millisecond},
			Timeout:            spec.Duration{Duration: 100 * time.Millisecond},
			UnhealthyThreshold: 2,
		},
		Restart: &spec.RestartPolicy{
			Policy:      "on-failure",
			MaxAttempts: 1,
			Delay:       spec.Duration{Duration: 100 * time.Millisecond},
		},
	}

	ms, err := NewManagedService(s, nil)
	if err != nil {
		t.Fatalf("failed to create: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := ms.Start(ctx); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	waitUntil(t, func() bool {
		return ms.State().Health == "unhealthy"
	}, 2*time.Second, "health to become unhealthy")

	cancel()
	waitUntil(t, func() bool {
		s := ms.State().State
		return s == driver.StateStopped || s == driver.StateFailed
	}, 2*time.Second, "service to stop after cancel")
}

func TestManagedServiceRejectsUnknownType(t *testing.T) {
	s := &spec.ServiceSpec{
		Service: spec.Service{
			Name: "test-unknown",
			Type: "potato",
		},
	}

	_, err := NewManagedService(s, nil)
	if err == nil {
		t.Error("expected error for unknown service type")
	}
}

func TestManagedServiceExternalStartStop(t *testing.T) {
	s := &spec.ServiceSpec{
		Service: spec.Service{
			Name: "test-external",
			Type: "external",
		},
		Health: &spec.HealthCheck{
			Type:               "tcp",
			Port:               19877,
			Interval:           spec.Duration{Duration: 50 * time.Millisecond},
			Timeout:            spec.Duration{Duration: 100 * time.Millisecond},
			UnhealthyThreshold: 2,
		},
	}

	ms, err := NewManagedService(s, nil)
	if err != nil {
		t.Fatalf("failed to create: %v", err)
	}

	if !ms.IsExternal() {
		t.Error("expected IsExternal() to return true")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := ms.Start(ctx); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	waitUntil(t, func() bool {
		return ms.State().State == driver.StateRunning
	}, 2*time.Second, "external service to become running")

	state := ms.State()
	if state.PID != 0 {
		t.Errorf("expected no PID for external service, got %d", state.PID)
	}
	if state.Port != 19877 {
		t.Errorf("expected port 19877, got %d", state.Port)
	}

	if err := ms.Stop(5 * time.Second); err != nil {
		t.Fatalf("failed to stop: %v", err)
	}
}

func TestManagedServiceStaticPortInjection(t *testing.T) {
	s := &spec.ServiceSpec{
		Service: spec.Service{
			Name:    "test-static-port",
			Type:    "native",
			Command: "printenv PORT",
		},
		Network: &spec.Network{Port: 8080},
		Restart: &spec.RestartPolicy{
			Policy: "never",
		},
	}

	ms, err := NewManagedService(s, nil)
	if err != nil {
		t.Fatalf("failed to create: %v", err)
	}

	ctx := context.Background()
	if err := ms.Start(ctx); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	// Wait for the process to run and produce log output
	waitUntil(t, func() bool {
		if ms.drv == nil {
			return false
		}
		return len(ms.drv.LogLines(1)) > 0
	}, 2*time.Second, "process to produce log output")

	ms.Stop(5 * time.Second)

	lines := ms.drv.LogLines(10)
	found := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "8080" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected PORT=8080 in env, log output: %v", lines)
	}
}

func TestManagedServiceSecretInjection(t *testing.T) {
	secrets := keychain.NewMemoryStore()
	secrets.Set("chat/database-url", "postgres://secret@localhost/db")

	s := &spec.ServiceSpec{
		Service: spec.Service{
			Name:    "test-secret",
			Type:    "native",
			Command: "printenv DATABASE_URL",
		},
		Secrets: map[string]spec.SecretRef{
			"DATABASE_URL": {Keychain: "chat/database-url"},
		},
		Restart: &spec.RestartPolicy{
			Policy: "never",
		},
	}

	ms, err := NewManagedService(s, secrets)
	if err != nil {
		t.Fatalf("failed to create: %v", err)
	}

	ctx := context.Background()
	if err := ms.Start(ctx); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	// Wait for the process to run and produce log output
	waitUntil(t, func() bool {
		if ms.drv == nil {
			return false
		}
		return len(ms.drv.LogLines(1)) > 0
	}, 2*time.Second, "process to produce log output")

	ms.Stop(5 * time.Second)

	lines := ms.drv.LogLines(10)
	expected := "postgres://secret@localhost/db"
	found := false
	for _, line := range lines {
		if strings.TrimSpace(line) == expected {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected secret %q in log output, got %v", expected, lines)
	}
}

func TestManagedServiceEnvVarInterpolation(t *testing.T) {
	s := &spec.ServiceSpec{
		Service: spec.Service{
			Name:    "test-interpolation",
			Type:    "native",
			Command: "printenv SERVER_PORT",
		},
		Network: &spec.Network{Port: 9090},
		Env: map[string]string{
			"SERVER_PORT": "${PORT}",
		},
		Restart: &spec.RestartPolicy{
			Policy: "never",
		},
	}

	ms, err := NewManagedService(s, nil)
	if err != nil {
		t.Fatalf("failed to create: %v", err)
	}

	ctx := context.Background()
	if err := ms.Start(ctx); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	waitUntil(t, func() bool {
		if ms.drv == nil {
			return false
		}
		return len(ms.drv.LogLines(1)) > 0
	}, 2*time.Second, "process to produce log output")

	ms.Stop(5 * time.Second)

	lines := ms.drv.LogLines(10)
	found := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "9090" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected SERVER_PORT=9090 (interpolated from PORT), log output: %v", lines)
	}
}

func TestManagedServiceEnvVarServiceName(t *testing.T) {
	s := &spec.ServiceSpec{
		Service: spec.Service{
			Name:    "my-web-app",
			Type:    "native",
			Command: "printenv APP_NAME",
		},
		Env: map[string]string{
			"APP_NAME": "${SERVICE_NAME}",
		},
		Restart: &spec.RestartPolicy{
			Policy: "never",
		},
	}

	ms, err := NewManagedService(s, nil)
	if err != nil {
		t.Fatalf("failed to create: %v", err)
	}

	ctx := context.Background()
	if err := ms.Start(ctx); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	waitUntil(t, func() bool {
		if ms.drv == nil {
			return false
		}
		return len(ms.drv.LogLines(1)) > 0
	}, 2*time.Second, "process to produce log output")

	ms.Stop(5 * time.Second)

	lines := ms.drv.LogLines(10)
	found := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "my-web-app" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected APP_NAME=my-web-app (interpolated from SERVICE_NAME), log output: %v", lines)
	}
}

func TestManagedServiceStopNotRunning(t *testing.T) {
	s := &spec.ServiceSpec{
		Service: spec.Service{
			Name:    "test-stop-idle",
			Type:    "native",
			Command: "sleep 60",
		},
		Restart: &spec.RestartPolicy{
			Policy: "never",
		},
	}

	ms, err := NewManagedService(s, nil)
	if err != nil {
		t.Fatalf("failed to create: %v", err)
	}

	// Do NOT call Start — Stop on a never-started service should return nil
	if err := ms.Stop(5 * time.Second); err != nil {
		t.Errorf("expected nil error stopping idle service, got %v", err)
	}
}

func TestManagedServiceInspect(t *testing.T) {
	secrets := keychain.NewMemoryStore()
	secrets.Set("book/database-url", "postgres://aurelia:aurelia@localhost/book")

	s := &spec.ServiceSpec{
		Service: spec.Service{
			Name:    "book",
			Type:    "native",
			Command: "sleep 60",
		},
		Network: &spec.Network{Port: 8080},
		Routing: &spec.Routing{
			Hostname: "book.hestia.internal",
			TLS:      true,
		},
		Health: &spec.HealthCheck{
			Type:     "http",
			Path:     "/health",
			Interval: spec.Duration{Duration: 10 * time.Second},
			Timeout:  spec.Duration{Duration: 2 * time.Second},
		},
		Dependencies: &spec.Dependencies{
			After:    []string{"postgres", "auth"},
			Requires: []string{"postgres"},
		},
		Restart: &spec.RestartPolicy{
			Policy:      "on-failure",
			MaxAttempts: 3,
			Delay:       spec.Duration{Duration: 15 * time.Second},
			Backoff:     "exponential",
		},
		Env: map[string]string{
			"BASE_CURRENCY": "AUD",
			"AUTH_URL":      "https://auth.hestia.internal",
		},
		Secrets: map[string]spec.SecretRef{
			"DATABASE_URL": {Keychain: "book/database-url"},
		},
	}

	ms, err := NewManagedService(s, secrets)
	if err != nil {
		t.Fatalf("failed to create: %v", err)
	}

	// Inspect without starting — should show stopped state + full spec
	si := ms.Inspect()

	if si.Name != "book" {
		t.Errorf("name = %q, want book", si.Name)
	}
	if si.Type != "native" {
		t.Errorf("type = %q, want native", si.Type)
	}
	if si.State != "stopped" {
		t.Errorf("state = %q, want stopped", si.State)
	}
	if si.Command != "sleep 60" {
		t.Errorf("command = %q, want sleep 60", si.Command)
	}
	if si.Port != 8080 {
		t.Errorf("port = %d, want 8080", si.Port)
	}

	// Env
	if si.Env["BASE_CURRENCY"] != "AUD" {
		t.Errorf("env BASE_CURRENCY = %q, want AUD", si.Env["BASE_CURRENCY"])
	}

	// Secrets resolved
	if si.Secrets["DATABASE_URL"] != "postgres://aurelia:aurelia@localhost/book" {
		t.Errorf("secret DATABASE_URL = %q, want postgres://aurelia:aurelia@localhost/book", si.Secrets["DATABASE_URL"])
	}

	// Routing
	if si.Routing == nil || si.Routing.Hostname != "book.hestia.internal" {
		t.Errorf("routing hostname = %v, want book.hestia.internal", si.Routing)
	}

	// Dependencies
	if si.Dependencies == nil || len(si.Dependencies.After) != 2 {
		t.Errorf("dependencies after = %v, want [postgres auth]", si.Dependencies)
	}

	// Restart
	if si.Restart == nil || si.Restart.Policy != "on-failure" {
		t.Errorf("restart policy = %v, want on-failure", si.Restart)
	}
}

func TestManagedServiceInspectMissingSecret(t *testing.T) {
	secrets := keychain.NewMemoryStore()
	// Don't set the secret — should show error

	s := &spec.ServiceSpec{
		Service: spec.Service{
			Name:    "test-missing-secret",
			Type:    "native",
			Command: "sleep 60",
		},
		Secrets: map[string]spec.SecretRef{
			"DATABASE_URL": {Keychain: "missing/key"},
		},
		Restart: &spec.RestartPolicy{Policy: "never"},
	}

	ms, err := NewManagedService(s, secrets)
	if err != nil {
		t.Fatalf("failed to create: %v", err)
	}

	si := ms.Inspect()
	if !strings.Contains(si.Secrets["DATABASE_URL"], "<error:") {
		t.Errorf("expected error marker for missing secret, got %q", si.Secrets["DATABASE_URL"])
	}
}

func TestManagedServiceStopExternal(t *testing.T) {
	s := &spec.ServiceSpec{
		Service: spec.Service{
			Name: "test-ext-stop",
			Type: "external",
		},
		Health: &spec.HealthCheck{
			Type:               "tcp",
			Port:               19879,
			Interval:           spec.Duration{Duration: 50 * time.Millisecond},
			Timeout:            spec.Duration{Duration: 100 * time.Millisecond},
			UnhealthyThreshold: 2,
		},
	}

	ms, err := NewManagedService(s, nil)
	if err != nil {
		t.Fatalf("failed to create: %v", err)
	}

	ctx := context.Background()
	if err := ms.Start(ctx); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	// Wait for health monitoring to begin
	time.Sleep(100 * time.Millisecond)

	// Stop should return nil and not hang
	if err := ms.Stop(5 * time.Second); err != nil {
		t.Errorf("expected nil error stopping external service, got %v", err)
	}
}
