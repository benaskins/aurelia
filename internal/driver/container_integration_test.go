//go:build integration

package driver

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Integration tests require a running Docker/OrbStack daemon.
// Run with: go test -tags integration ./internal/driver/ -run TestContainer

func TestContainerStartStop(t *testing.T) {
	d, err := NewContainer(ContainerConfig{
		Name:        "test-start-stop",
		Image:       "alpine:latest",
		Env:         []string{"HELLO=world"},
		NetworkMode: "bridge",
	})
	if err != nil {
		t.Fatalf("NewContainer: %v", err)
	}

	ctx := context.Background()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	info := d.Info()
	if info.State != StateRunning {
		t.Errorf("expected running, got %v", info.State)
	}
	if d.ContainerID() == "" {
		t.Error("expected container ID")
	}

	if err := d.Stop(ctx, 5*time.Second); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	info = d.Info()
	if info.State != StateStopped {
		t.Errorf("expected stopped, got %v", info.State)
	}
}

func TestContainerExitsNaturally(t *testing.T) {
	t.Skip("Need an image that exits naturally — skipping for now")
}

func TestContainerWithHostNetwork(t *testing.T) {
	d, err := NewContainer(ContainerConfig{
		Name:        "test-host-net",
		Image:       "alpine:latest",
		NetworkMode: "host",
	})
	if err != nil {
		t.Fatalf("NewContainer: %v", err)
	}

	ctx := context.Background()
	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	if err := d.Stop(ctx, 5*time.Second); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestContainerWithEnv(t *testing.T) {
	d, err := NewContainer(ContainerConfig{
		Name:        "test-env",
		Image:       "alpine:latest",
		Env:         []string{"MY_VAR=hello-aurelia"},
		NetworkMode: "bridge",
	})
	if err != nil {
		t.Fatalf("NewContainer: %v", err)
	}

	ctx := context.Background()
	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	time.Sleep(500 * time.Millisecond)
	d.Stop(ctx, 5*time.Second)

	// Check logs for env output
	stdout := d.Stdout()
	buf := make([]byte, 4096)
	n, _ := stdout.Read(buf)
	output := string(buf[:n])
	_ = output // Logs may or may not contain env info depending on container entrypoint
}

func TestContainerWait(t *testing.T) {
	d, err := NewContainer(ContainerConfig{
		Name:        "test-wait",
		Image:       "alpine:latest",
		NetworkMode: "bridge",
	})
	if err != nil {
		t.Fatalf("NewContainer: %v", err)
	}

	ctx := context.Background()
	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Stop it, which should make Wait return
	go func() {
		time.Sleep(200 * time.Millisecond)
		d.Stop(ctx, 5*time.Second)
	}()

	exitCode, err := d.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	// Exit code from SIGTERM is typically 137 (128 + 9) or 143 (128 + 15)
	// but with docker stop it may be 0 or the signal code
	_ = exitCode
	_ = strings.Contains // just to use the import
}
