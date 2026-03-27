//go:build darwin

package driver

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// FindProcessByCommand searches for a running process whose command matches the
// expected binary name. Returns the PID of the first match, or 0 if not found.
// The excludePID parameter allows skipping a known stale PID.
//
// This is used during orphan recovery: when a saved PID has been reused by a
// different process, we search for the original service by command pattern.
func FindProcessByCommand(expectedCommand string, excludePID int) int {
	if expectedCommand == "" {
		return 0
	}

	parts := strings.Fields(expectedCommand)
	if len(parts) == 0 {
		return 0
	}
	expectedBin := filepath.Base(parts[0])

	// Use ps to list all processes with their command.
	// -e: all processes, -o pid=,comm=: PID and command name only (no header).
	out, err := exec.Command("ps", "-eo", "pid=,comm=").Output()
	if err != nil {
		return 0
	}

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 0 {
			continue
		}

		if pid == excludePID {
			continue
		}

		comm := filepath.Base(fields[1])
		if comm == expectedBin {
			return pid
		}
	}

	return 0
}

// FindPIDOnPort returns the PID of the process listening on the given TCP port,
// or 0 if no process is found. Used to detect orphaned processes holding ports.
func FindPIDOnPort(port int) int {
	if port <= 0 {
		return 0
	}

	// Use lsof to find the process listening on the port.
	// -i: internet addresses, -P: no port names, -n: no hostname resolution,
	// -sTCP:LISTEN: only listening TCP sockets, -t: terse (PID only).
	out, err := exec.Command("lsof", "-iTCP:"+strconv.Itoa(port), "-P", "-n", "-sTCP:LISTEN", "-t").Output()
	if err != nil {
		return 0
	}

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err == nil && pid > 0 {
			return pid
		}
	}

	return 0
}
