//go:build !darwin

package driver

import (
	"fmt"
	"os"
	"strings"
)

// processName returns the executable name for a given PID by reading /proc.
func processName(pid int) (string, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return "", fmt.Errorf("read /proc/%d/comm: %w", pid, err)
	}
	name := strings.TrimSpace(string(data))
	if name == "" {
		return "", fmt.Errorf("empty process name for pid %d", pid)
	}
	return name, nil
}
