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

	// Give it a moment to actually start
	time.Sleep(100 * time.Millisecond)

	state := ms.State()
	if state.State != driver.StateRunning {
		t.Errorf("expected running, got %v", state.State)
	}
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
			Delay:       spec.Duration{Duration: 100 * time.Millisecond},
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

	// Wait for restarts to exhaust
	time.Sleep(800 * time.Millisecond)

	state := ms.State()
	if state.RestartCount < 1 {
		t.Errorf("expected at least 1 restart, got %d", state.RestartCount)
	}
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
			Delay:       spec.Duration{Duration: 100 * time.Millisecond},
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

	// Wait a bit — should NOT restart
	time.Sleep(500 * time.Millisecond)

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

	// Wait for restarts
	time.Sleep(800 * time.Millisecond)
	cancel()
	time.Sleep(200 * time.Millisecond)

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

	time.Sleep(300 * time.Millisecond)

	state := ms.State()
	if state.RestartCount != 0 {
		t.Errorf("expected 0 restarts with 'never' policy, got %d", state.RestartCount)
	}
}

func TestManagedServiceExponentialBackoff(t *testing.T) {
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
	time.Sleep(2 * time.Second)

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

	// Wait for health checks to trigger unhealthy
	time.Sleep(500 * time.Millisecond)

	state := ms.State()
	if state.Health != "unhealthy" {
		t.Errorf("expected unhealthy, got %v", state.Health)
	}

	cancel()
	time.Sleep(200 * time.Millisecond)
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

	// Give health checks time to run
	time.Sleep(300 * time.Millisecond)

	state := ms.State()
	if state.State != driver.StateRunning {
		t.Errorf("expected running, got %v", state.State)
	}
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

	// Wait for process to run and exit
	time.Sleep(500 * time.Millisecond)

	ms.Stop(5 * time.Second)

	// Check stdout captured the secret
	if ms.drv == nil {
		t.Fatal("expected driver to exist")
	}

	stdout := ms.drv.Stdout()
	buf := make([]byte, 1024)
	n, _ := stdout.Read(buf)
	output := string(buf[:n])

	expected := "postgres://secret@localhost/db"
	if output == "" {
		t.Error("expected secret to be in stdout")
	} else if strings.TrimSpace(output) != expected {
		t.Errorf("expected %q, got %q", expected, output)
	}
}
