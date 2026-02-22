//go:build !darwin

package driver

import (
	"fmt"
	"os"
	"strconv"
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

// processStartTime returns the process start time in clock ticks since boot
// (field 22 of /proc/<pid>/stat). Combined with PID, this uniquely identifies
// a process and guards against PID reuse.
func processStartTime(pid int) (int64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, fmt.Errorf("read /proc/%d/stat: %w", pid, err)
	}

	// The comm field (field 2) is in parentheses and may contain spaces.
	// Find the closing paren to safely split the remaining fields.
	s := string(data)
	closeIdx := strings.LastIndex(s, ")")
	if closeIdx < 0 {
		return 0, fmt.Errorf("malformed /proc/%d/stat: no closing paren", pid)
	}
	// Fields after comm start at index closeIdx+2 (skip ") ")
	rest := strings.Fields(s[closeIdx+2:])
	// starttime is field 22 in the full stat, which is index 19 in rest
	// (fields 1-2 are before the split, field 3 is rest[0])
	const starttimeIdx = 19
	if len(rest) <= starttimeIdx {
		return 0, fmt.Errorf("malformed /proc/%d/stat: too few fields", pid)
	}
	starttime, err := strconv.ParseInt(rest[starttimeIdx], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse starttime for pid %d: %w", pid, err)
	}
	return starttime, nil
}
