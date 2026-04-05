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
// The alsoMatch parameter provides additional names to match against. This
// handles cases where the launched command differs from the actual process name
// (e.g. a shell script that uses exec to replace itself with a different binary).
//
// This is used during orphan recovery: when a saved PID has been reused by a
// different process, we search for the original service by command pattern.
func FindProcessByCommand(expectedCommand string, excludePID int, alsoMatch ...string) int {
	if expectedCommand == "" && len(alsoMatch) == 0 {
		return 0
	}

	names := make(map[string]bool)
	if expectedCommand != "" {
		parts := strings.Fields(expectedCommand)
		if len(parts) > 0 {
			names[filepath.Base(parts[0])] = true
		}
	}
	for _, alt := range alsoMatch {
		if alt != "" {
			names[filepath.Base(alt)] = true
		}
	}

	if len(names) == 0 {
		return 0
	}

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
		for name := range names {
			if namesMatch(comm, name) {
				return pid
			}
		}
	}

	return 0
}

// AureliaServiceTag reads the AURELIA_SERVICE environment variable from a
// running process. Returns the service name or empty string if not set.
// This is the most reliable way to identify aurelia-managed processes —
// the env var survives exec replacement and reparenting to PID 1.
func AureliaServiceTag(pid int) string {
	if pid <= 0 {
		return ""
	}

	// ps eww shows command + full environment, space-separated.
	out, err := exec.Command("ps", "eww", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return ""
	}

	for _, field := range strings.Fields(string(out)) {
		if strings.HasPrefix(field, "AURELIA_SERVICE=") {
			return field[len("AURELIA_SERVICE="):]
		}
	}
	return ""
}

// FindPortsForPID returns all TCP ports a process is listening on.
// Used to check if an orphan is in aurelia's dynamic port range.
func FindPortsForPID(pid int) []int {
	if pid <= 0 {
		return nil
	}

	// lsof -a -p <pid> -iTCP -sTCP:LISTEN -P -n -F n
	// -a: AND conditions, -F n: machine-readable (name field = port)
	out, err := exec.Command("lsof", "-a", "-p", strconv.Itoa(pid),
		"-iTCP", "-sTCP:LISTEN", "-P", "-n").Output()
	if err != nil {
		return nil
	}

	var ports []int
	seen := make(map[int]bool)
	for _, line := range strings.Split(string(out), "\n") {
		// Lines look like: "process PID user FD type device size/off node NAME"
		// NAME field is like "127.0.0.1:8090" or "*:8090" or "[::1]:8090"
		fields := strings.Fields(line)
		if len(fields) < 9 {
			continue
		}
		name := fields[len(fields)-1]
		if !strings.Contains(name, ":") || !strings.HasSuffix(fields[len(fields)-2], "TCP") {
			continue
		}
		// Extract port from the last colon-separated component
		idx := strings.LastIndex(name, ":")
		if idx < 0 {
			continue
		}
		portStr := strings.TrimSuffix(name[idx+1:], "(LISTEN)")
		portStr = strings.TrimSpace(portStr)
		if p, err := strconv.Atoi(portStr); err == nil && p > 0 && !seen[p] {
			ports = append(ports, p)
			seen[p] = true
		}
	}
	return ports
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
