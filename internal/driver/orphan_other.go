//go:build !darwin

package driver

import (
	"fmt"
	"os"
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

	// Read /proc to find matching processes.
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}

	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}

		if pid == excludePID {
			continue
		}

		comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
		if err != nil {
			continue
		}

		name := strings.TrimSpace(string(comm))
		if name == expectedBin {
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

	// Parse /proc/net/tcp to find the socket, then map inode to PID.
	// The port is in hex at field 2 (local_address) as addr:port.
	hexPort := fmt.Sprintf("%04X", port)

	data, err := os.ReadFile("/proc/net/tcp")
	if err != nil {
		return 0
	}

	var targetInode string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		// field 1 is local_address (hex_ip:hex_port), field 3 is state (0A = LISTEN)
		localParts := strings.Split(fields[1], ":")
		if len(localParts) != 2 {
			continue
		}
		if localParts[1] == hexPort && fields[3] == "0A" {
			targetInode = fields[9]
			break
		}
	}

	if targetInode == "" || targetInode == "0" {
		return 0
	}

	// Walk /proc/*/fd to find which PID owns this inode.
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}

	socketLink := "socket:[" + targetInode + "]"
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}

		fdDir := fmt.Sprintf("/proc/%d/fd", pid)
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}

		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			if link == socketLink {
				return pid
			}
		}
	}

	return 0
}
