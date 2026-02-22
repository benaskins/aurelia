//go:build darwin

package driver

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// processName returns the executable name for a given PID using sysctl,
// avoiding the need to fork a process and parse CLI output.
func processName(pid int) (string, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", fmt.Errorf("sysctl kern.proc.pid.%d: %w", pid, err)
	}

	// P_comm is a null-terminated [17]byte — find the end of the string.
	name := unix.ByteSliceToString(kp.Proc.P_comm[:])
	if name == "" {
		return "", fmt.Errorf("empty process name for pid %d", pid)
	}
	return name, nil
}
