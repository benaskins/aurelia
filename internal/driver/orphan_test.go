package driver

import (
	"net"
	"os"
	"os/exec"
	"testing"
)

func TestFindProcessByCommandFindsRunning(t *testing.T) {
	// Start a sleep process and verify we can find it by command
	cmd := exec.Command("sleep", "300")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting sleep: %v", err)
	}
	defer cmd.Process.Kill()

	pid := FindProcessByCommand("sleep 300", 0)
	if pid == 0 {
		t.Fatal("expected to find sleep process")
	}

	// The PID should match (or at least be a valid sleep process)
	// We started one, so it should find at least ours
	if pid != cmd.Process.Pid {
		// Another sleep process might exist; just verify we got a valid PID
		t.Logf("found PID %d instead of %d (another sleep process may exist)", pid, cmd.Process.Pid)
	}
}

func TestFindProcessByCommandExcludesPID(t *testing.T) {
	cmd := exec.Command("sleep", "300")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting sleep: %v", err)
	}
	defer cmd.Process.Kill()

	// Search for sleep but exclude our PID — should return 0 (unless another
	// sleep is running, but that's unlikely in test environments)
	pid := FindProcessByCommand("sleep 300", cmd.Process.Pid)
	if pid == cmd.Process.Pid {
		t.Error("expected FindProcessByCommand to exclude the given PID")
	}
}

func TestFindProcessByCommandEmptyCommand(t *testing.T) {
	pid := FindProcessByCommand("", 0)
	if pid != 0 {
		t.Errorf("expected 0 for empty command, got %d", pid)
	}
}

func TestFindProcessByCommandNoMatch(t *testing.T) {
	pid := FindProcessByCommand("definitely-not-running-binary-12345", 0)
	if pid != 0 {
		t.Errorf("expected 0 for non-existent binary, got %d", pid)
	}
}

func TestFindProcessByCommandFindsAbsolutePath(t *testing.T) {
	cmd := exec.Command("sleep", "300")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting sleep: %v", err)
	}
	defer cmd.Process.Kill()

	// Search with absolute path — should still match by base name
	pid := FindProcessByCommand("/bin/sleep 300", 0)
	if pid == 0 {
		t.Fatal("expected to find sleep process when searching with absolute path")
	}
}

func TestFindPIDOnPortFindsListener(t *testing.T) {
	// Start a TCP listener on a random port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port

	pid := FindPIDOnPort(port)
	if pid == 0 {
		t.Fatalf("expected to find PID on port %d", port)
	}

	// Should be our own PID
	if pid != os.Getpid() {
		t.Errorf("expected PID %d (self), got %d", os.Getpid(), pid)
	}
}

func TestFindPIDOnPortNoListener(t *testing.T) {
	// Find a port with nothing listening
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close() // close immediately so nothing is listening

	pid := FindPIDOnPort(port)
	if pid != 0 {
		t.Errorf("expected 0 for unused port %d, got PID %d", port, pid)
	}
}

func TestFindPIDOnPortZero(t *testing.T) {
	pid := FindPIDOnPort(0)
	if pid != 0 {
		t.Errorf("expected 0 for port 0, got %d", pid)
	}
}

func TestFindPIDOnPortNegative(t *testing.T) {
	pid := FindPIDOnPort(-1)
	if pid != 0 {
		t.Errorf("expected 0 for negative port, got %d", pid)
	}
}
