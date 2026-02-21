package driver

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestNativeStartAndWait(t *testing.T) {
	d := NewNative(NativeConfig{
		Command: "echo hello",
	})

	ctx := context.Background()
	if err := d.Start(ctx); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	info := d.Info()
	if info.PID <= 0 {
		t.Errorf("expected positive PID, got %d", info.PID)
	}

	exitCode, err := d.Wait()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}

	info = d.Info()
	// After clean exit with no restart policy, state is failed (unexpected exit)
	// because the supervision layer isn't involved here
	if info.State != StateFailed {
		t.Errorf("expected state failed (unsupervised exit), got %v", info.State)
	}
}

func TestNativeStdoutCapture(t *testing.T) {
	d := NewNative(NativeConfig{
		Command: "echo hello world",
	})

	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	d.Wait()

	lines := d.LogLines(10)
	found := false
	for _, line := range lines {
		if strings.Contains(line, "hello world") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'hello world' in log lines, got %v", lines)
	}
}

func TestNativeStopGraceful(t *testing.T) {
	// Start a long-running process
	d := NewNative(NativeConfig{
		Command: "sleep 60",
	})

	ctx := context.Background()
	if err := d.Start(ctx); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	// Verify it's running
	info := d.Info()
	if info.State != StateRunning {
		t.Fatalf("expected running, got %v", info.State)
	}

	// Stop with timeout
	if err := d.Stop(ctx, 5*time.Second); err != nil {
		t.Fatalf("failed to stop: %v", err)
	}

	info = d.Info()
	if info.State != StateStopped {
		t.Errorf("expected stopped, got %v", info.State)
	}
}

func TestNativeFailedProcess(t *testing.T) {
	d := NewNative(NativeConfig{
		Command: "false", // exits with code 1
	})

	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	exitCode, _ := d.Wait()
	if exitCode != 1 {
		t.Errorf("expected exit code 1, got %d", exitCode)
	}

	info := d.Info()
	if info.State != StateFailed {
		t.Errorf("expected failed, got %v", info.State)
	}
}

func TestNativeEnvironment(t *testing.T) {
	// Use printenv which takes a single argument — no shell quoting issues
	d := NewNative(NativeConfig{
		Command: "printenv TEST_VAR",
		Env:     []string{"TEST_VAR=aurelia_test_value"},
	})

	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	d.Wait()

	lines := d.LogLines(10)
	if len(lines) == 0 {
		t.Fatal("expected log output")
	}
	output := strings.TrimSpace(lines[0])

	if output != "aurelia_test_value" {
		t.Errorf("expected 'aurelia_test_value', got %q", output)
	}
}

func TestNativeDoubleStart(t *testing.T) {
	d := NewNative(NativeConfig{
		Command: "sleep 60",
	})

	ctx := context.Background()
	if err := d.Start(ctx); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer d.Stop(ctx, 2*time.Second)

	if err := d.Start(ctx); err == nil {
		t.Error("expected error on double start")
	}
}

func TestNativeStopAlreadyStopped(t *testing.T) {
	d := NewNative(NativeConfig{
		Command: "true",
	})

	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	d.Wait()

	// Stopping an already-exited process should not error
	if err := d.Stop(context.Background(), 2*time.Second); err != nil {
		t.Errorf("unexpected error stopping exited process: %v", err)
	}
}

func TestNativeWaitNotStarted(t *testing.T) {
	d := NewNative(NativeConfig{
		Command: "echo hello",
	})

	_, err := d.Wait()
	if err == nil {
		t.Error("expected error waiting on unstarted process")
	}
}

func TestNativeStopReturnsAfterSIGKILL(t *testing.T) {
	// Verify that Stop() doesn't hang forever after SIGKILL.
	// Uses a normal sleep process — the fix adds a hard timeout after SIGKILL
	// so even zombie processes won't block Stop() indefinitely.
	d := NewNative(NativeConfig{
		Command: "sleep 60",
	})

	ctx := context.Background()
	if err := d.Start(ctx); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	// Stop with a very short SIGTERM timeout to force SIGKILL path
	done := make(chan error, 1)
	go func() {
		done <- d.Stop(ctx, 1*time.Millisecond)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Stop() hung after SIGKILL — expected it to return within hard timeout")
	}

	info := d.Info()
	if info.State != StateStopped && info.State != StateFailed {
		t.Errorf("expected stopped or failed state, got %v", info.State)
	}
}
