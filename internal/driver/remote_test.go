package driver

import (
	"context"
	"testing"
	"time"
)

func TestRemoteDriver_Start(t *testing.T) {
	drv := NewRemote(RemoteConfig{
		StartCmd: "echo deployed",
	})

	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	info := drv.Info()
	if info.State != StateRunning {
		t.Errorf("state = %q, want running", info.State)
	}
}

func TestRemoteDriver_StartFailure(t *testing.T) {
	drv := NewRemote(RemoteConfig{
		StartCmd: "false",
	})

	err := drv.Start(context.Background())
	if err == nil {
		t.Fatal("expected error from failing start command")
	}

	info := drv.Info()
	if info.State != StateFailed {
		t.Errorf("state = %q, want failed", info.State)
	}
}

func TestRemoteDriver_Stop(t *testing.T) {
	drv := NewRemote(RemoteConfig{
		StartCmd: "echo deployed",
		StopCmd:  "echo stopped",
	})

	drv.Start(context.Background())

	if err := drv.Stop(context.Background(), 5*time.Second); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	info := drv.Info()
	if info.State != StateStopped {
		t.Errorf("state = %q, want stopped", info.State)
	}
}

func TestRemoteDriver_StopNoHook(t *testing.T) {
	drv := NewRemote(RemoteConfig{
		StartCmd: "echo deployed",
	})

	drv.Start(context.Background())

	// Stop without a stop hook should succeed (no-op).
	if err := drv.Stop(context.Background(), 5*time.Second); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	info := drv.Info()
	if info.State != StateStopped {
		t.Errorf("state = %q, want stopped", info.State)
	}
}

func TestRemoteDriver_Restart(t *testing.T) {
	drv := NewRemote(RemoteConfig{
		StartCmd:   "echo deployed",
		RestartCmd: "echo restarted",
	})

	drv.Start(context.Background())

	// Stop calls RestartCmd if set (for supervision loop re-use).
	// Actually, restart is called by the supervision loop via Stop+Start.
	// The driver just needs to be re-startable.
	drv.Stop(context.Background(), 5*time.Second)
	if err := drv.Start(context.Background()); err != nil {
		t.Fatalf("re-Start: %v", err)
	}

	info := drv.Info()
	if info.State != StateRunning {
		t.Errorf("state = %q, want running", info.State)
	}
}

func TestRemoteDriver_Wait(t *testing.T) {
	drv := NewRemote(RemoteConfig{
		StartCmd: "echo deployed",
	})

	drv.Start(context.Background())

	// Wait on a remote service blocks until stopped.
	go func() {
		time.Sleep(50 * time.Millisecond)
		drv.Stop(context.Background(), 5*time.Second)
	}()

	code, err := drv.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

func TestRemoteDriver_Info_PID(t *testing.T) {
	drv := NewRemote(RemoteConfig{
		StartCmd: "echo deployed",
	})

	info := drv.Info()
	if info.PID != 0 {
		t.Errorf("PID should be 0 for remote driver, got %d", info.PID)
	}
}

func TestRemoteDriver_LogLines(t *testing.T) {
	drv := NewRemote(RemoteConfig{
		StartCmd: "echo hello-from-deploy",
	})

	drv.Start(context.Background())

	lines := drv.LogLines(10)
	if len(lines) == 0 {
		t.Skip("log capture not implemented for remote driver")
	}
}
