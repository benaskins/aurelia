package driver

import (
	"os/exec"
	"testing"
)

func TestVerifyProcessStartTimeOverridesNameMismatch(t *testing.T) {
	// Start time + PID uniquely identify a process, so a matching start time must
	// verify even when the live process name differs from the recorded command —
	// which is the norm for wrapper/exec'd or long (>16 char) binary names.
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	pid := cmd.Process.Pid
	startTime, err := processStartTime(pid)
	if err != nil {
		t.Fatalf("processStartTime: %v", err)
	}

	// Recorded command's basename ("start-sleep.sh") does not match the live
	// process name ("sleep"), but the start time matches.
	if !VerifyProcess(pid, "/opt/wrappers/start-sleep.sh", startTime) {
		t.Error("VerifyProcess = false; want true (matching start time is authoritative)")
	}
}
