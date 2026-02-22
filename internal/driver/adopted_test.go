package driver

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestAdoptedDriverMonitorsProcess(t *testing.T) {
	// Start a real process to adopt
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting process: %v", err)
	}
	defer cmd.Process.Kill()

	pid := cmd.Process.Pid

	drv, err := NewAdopted(pid)
	if err != nil {
		t.Fatalf("NewAdopted: %v", err)
	}

	info := drv.Info()
	if info.PID != pid {
		t.Errorf("expected PID %d, got %d", pid, info.PID)
	}
	if info.State != StateRunning {
		t.Errorf("expected running, got %v", info.State)
	}

	// Stop it
	if err := drv.Stop(context.Background(), 5*time.Second); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	info = drv.Info()
	if info.State != StateStopped {
		t.Errorf("expected stopped, got %v", info.State)
	}
}

func TestAdoptedDriverDetectsExit(t *testing.T) {
	// Start a short-lived process
	cmd := exec.Command("sleep", "0.5")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting process: %v", err)
	}

	pid := cmd.Process.Pid

	drv, err := NewAdopted(pid)
	if err != nil {
		t.Fatalf("NewAdopted: %v", err)
	}

	// Reap the child (we're the parent) so it doesn't become a zombie
	go cmd.Wait()

	// Wait for the adopted driver to detect exit
	code, _ := drv.Wait()
	_ = code

	info := drv.Info()
	if info.State == StateRunning {
		t.Error("expected non-running state after process exit")
	}
}

func TestAdoptedDriverRejectsDeadPID(t *testing.T) {
	// Use a PID that's unlikely to exist
	_, err := NewAdopted(99999999)
	if err == nil {
		t.Error("expected error for dead PID")
	}
}

func TestAdoptedDriverStartIsNoop(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting process: %v", err)
	}
	defer cmd.Process.Kill()

	drv, err := NewAdopted(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("NewAdopted: %v", err)
	}
	defer drv.Stop(context.Background(), 5*time.Second)

	// Start should be a no-op
	if err := drv.Start(context.Background()); err != nil {
		t.Errorf("Start should be no-op, got: %v", err)
	}

	// Should still be running
	if drv.Info().State != StateRunning {
		t.Error("expected still running after Start()")
	}
}

func TestVerifyProcessMatchesSelf(t *testing.T) {
	// The test binary is a Go executable — verify we can match it
	pid := os.Getpid()

	// Should match when both expectedCommand and expectedStartTime are zero (best effort)
	if !VerifyProcess(pid, "", 0) {
		t.Error("expected match with empty command and zero start time")
	}

	// Should not match a completely wrong binary name
	if VerifyProcess(pid, "definitely-not-this-binary", 0) {
		t.Error("expected no match for wrong binary")
	}

	// Should fail for a dead PID
	if VerifyProcess(99999999, "sleep", 0) {
		t.Error("expected no match for dead PID")
	}
}

func TestVerifyProcessMatchesSleep(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting process: %v", err)
	}
	defer cmd.Process.Kill()

	pid := cmd.Process.Pid

	if !VerifyProcess(pid, "sleep 30", 0) {
		t.Error("expected match for 'sleep 30'")
	}
	if !VerifyProcess(pid, "/bin/sleep", 0) {
		t.Error("expected match for '/bin/sleep' (base name comparison)")
	}
	if VerifyProcess(pid, "bash", 0) {
		t.Error("expected no match for 'bash'")
	}
}

func TestVerifyProcessStartTime(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting process: %v", err)
	}
	defer cmd.Process.Kill()

	pid := cmd.Process.Pid

	// Get the real start time
	startTime, err := ProcessStartTime(pid)
	if err != nil {
		t.Fatalf("ProcessStartTime: %v", err)
	}
	if startTime == 0 {
		t.Fatal("expected non-zero start time")
	}

	// Should match with correct start time
	if !VerifyProcess(pid, "sleep 30", startTime) {
		t.Error("expected match with correct start time")
	}

	// Should reject with wrong start time (simulates PID reuse)
	if VerifyProcess(pid, "sleep 30", startTime-1000) {
		t.Error("expected no match with wrong start time")
	}

	// Should reject with wrong start time even if command is empty
	if VerifyProcess(pid, "", startTime-1000) {
		t.Error("expected no match with wrong start time and empty command")
	}

	// Should match with correct start time and empty command
	if !VerifyProcess(pid, "", startTime) {
		t.Error("expected match with correct start time and empty command")
	}
}

func TestProcessStartTime(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting process: %v", err)
	}
	defer cmd.Process.Kill()

	st1, err := ProcessStartTime(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("ProcessStartTime: %v", err)
	}

	// Calling again should return the same value (stable)
	st2, err := ProcessStartTime(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("ProcessStartTime (second call): %v", err)
	}
	if st1 != st2 {
		t.Errorf("start time not stable: %d != %d", st1, st2)
	}

	// Dead PID should fail
	if _, err := ProcessStartTime(99999999); err == nil {
		t.Error("expected error for dead PID")
	}
}

func TestAdoptedDriverLogLinesReturnsNil(t *testing.T) {
	drv, err := NewAdopted(os.Getpid())
	if err != nil {
		t.Fatalf("NewAdopted(self): %v", err)
	}

	if lines := drv.LogLines(10); lines != nil {
		t.Errorf("expected nil log lines for adopted driver, got %v", lines)
	}
}

func TestAdoptedDriverUsesCurrentPID(t *testing.T) {
	// Our own process should be adoptable
	drv, err := NewAdopted(os.Getpid())
	if err != nil {
		t.Fatalf("NewAdopted(self): %v", err)
	}

	if drv.Info().PID != os.Getpid() {
		t.Errorf("expected PID %d, got %d", os.Getpid(), drv.Info().PID)
	}

	// Don't stop our own process!
}
