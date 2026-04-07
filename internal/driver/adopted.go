package driver

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// AdoptedDriver monitors an existing process by PID (crash recovery).
type AdoptedDriver struct {
	pid int

	mu        sync.Mutex
	state     State
	startedAt time.Time
	exitCode  int
	exitErr   string
	done      chan struct{}
	stopCh    chan struct{}  // signals monitor to stop polling
	monitorWg sync.WaitGroup // tracks monitor goroutine lifetime
}

// NewAdopted creates a driver that monitors an already-running process.
// Returns an error if the PID is not alive.
func NewAdopted(pid int) (*AdoptedDriver, error) {
	// On Unix, FindProcess always succeeds. Use kill(pid, 0) to check liveness.
	if err := syscall.Kill(pid, 0); err != nil {
		return nil, fmt.Errorf("process %d not alive: %w", pid, err)
	}

	d := &AdoptedDriver{
		pid:       pid,
		state:     StateRunning,
		startedAt: time.Now(),
		done:      make(chan struct{}),
		stopCh:    make(chan struct{}),
	}

	d.monitorWg.Add(1)
	go d.monitor()
	return d, nil
}

func (d *AdoptedDriver) monitor() {
	defer d.monitorWg.Done()
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := syscall.Kill(d.pid, 0); err != nil {
				d.markExited(1, "process exited")
				return
			}
		case <-d.stopCh:
			return
		}
	}
}

func (d *AdoptedDriver) markExited(code int, errMsg string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.state != StateRunning && d.state != StateStopping {
		return
	}

	if d.state == StateStopping {
		d.state = StateStopped
	} else {
		d.state = StateFailed
	}
	d.exitCode = code
	d.exitErr = errMsg

	select {
	case <-d.done:
	default:
		close(d.done)
	}
}

func (d *AdoptedDriver) Start(ctx context.Context) error {
	return nil // no-op for adopted processes
}

func (d *AdoptedDriver) Stop(ctx context.Context, timeout time.Duration) error {
	d.mu.Lock()
	// If state is not StateRunning, the process is already gone: either Stop()
	// was already called, or the monitor goroutine detected exit via kill(pid,0)
	// and set StateFailed.  In both cases there is nothing to signal — the
	// underlying PID is dead.
	if d.state != StateRunning {
		d.mu.Unlock()
		return nil
	}
	d.state = StateStopping
	d.mu.Unlock()

	// Stop the monitor and wait for it to exit so the goroutine
	// doesn't leak when Stop returns.
	close(d.stopCh)
	d.monitorWg.Wait()

	// Send SIGTERM
	if err := syscall.Kill(d.pid, syscall.SIGTERM); err != nil {
		// Process already gone
		d.markExited(0, "")
		return nil
	}

	// Poll for death — we can't use wait() since we're not the parent.
	// After SIGTERM, poll aggressively; fall back to SIGKILL on timeout.
	deadline := time.After(timeout)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := syscall.Kill(d.pid, 0); err != nil {
				d.markExited(0, "")
				return nil
			}
		case <-deadline:
			_ = syscall.Kill(d.pid, syscall.SIGKILL)
			// Give SIGKILL a moment
			time.Sleep(100 * time.Millisecond)
			d.markExited(137, "killed")
			return nil
		case <-ctx.Done():
			_ = syscall.Kill(d.pid, syscall.SIGKILL)
			time.Sleep(100 * time.Millisecond)
			d.markExited(137, "killed")
			return ctx.Err()
		}
	}
}

func (d *AdoptedDriver) Info() ProcessInfo {
	d.mu.Lock()
	defer d.mu.Unlock()

	return ProcessInfo{
		PID:       d.pid,
		State:     d.state,
		StartedAt: d.startedAt,
		ExitCode:  d.exitCode,
		Error:     d.exitErr,
	}
}

func (d *AdoptedDriver) Wait() (int, error) {
	<-d.done
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.exitCode, nil
}

func (d *AdoptedDriver) LogLines(n int) []string {
	return nil
}

func (d *AdoptedDriver) LogLinesSince(gen int) ([]string, int) {
	return nil, 0
}

// VerifyProcess checks whether the process at the given PID matches the expected
// command name and start time. This guards against PID reuse: if the OS recycled
// the PID for a different process, the command or start time won't match and
// adoption should be skipped.
//
// expectedStartTime of 0 skips the start-time check (backward compat with old
// state files that don't have it). Returns true if all non-zero checks pass, or
// if both expectedCommand and expectedStartTime are zero (best effort).
func VerifyProcess(pid int, expectedCommand string, expectedStartTime int64) bool {
	if expectedCommand == "" && expectedStartTime == 0 {
		return true // no identity recorded, best effort
	}

	// Check start time first — it's the strongest signal against PID reuse.
	if expectedStartTime != 0 {
		actual, err := processStartTime(pid)
		if err != nil {
			return false
		}
		if actual != expectedStartTime {
			return false
		}
	}

	if expectedCommand == "" {
		return true // start time matched, no command to check
	}

	actual, err := processName(pid)
	if err != nil {
		return false
	}

	// Extract the binary name from the expected command (first word), then
	// compare base names. e.g. "sleep 10" → "sleep", "/usr/bin/python" → "python"
	parts := strings.Fields(expectedCommand)
	if len(parts) == 0 {
		return true
	}

	expected := filepath.Base(parts[0])
	return namesMatch(actual, expected)
}

// namesMatch compares two process names, handling platform quirks:
// - Case-insensitive comparison (macOS P_comm capitalisation varies)
// - Version-stripped comparison (python3.12 matches Python)
func namesMatch(actual, expected string) bool {
	if strings.EqualFold(actual, expected) {
		return true
	}

	// Strip version suffixes for comparison: python3.12 → python, ruby3.2 → ruby
	return strings.EqualFold(stripVersion(actual), stripVersion(expected))
}

// stripVersion removes trailing version numbers from a binary name.
// e.g. "python3.12" → "python", "ruby3.2" → "ruby", "node18" → "node"
// Returns the original name if stripping would produce an empty string.
func stripVersion(name string) string {
	for i, c := range name {
		if c >= '0' && c <= '9' {
			if i == 0 {
				return name // don't strip if name starts with a digit (e.g. "7zip")
			}
			return name[:i]
		}
	}
	return name
}

// ProcessStartTime returns the OS-reported start time for a process. The value
// is platform-specific (Unix epoch seconds on Darwin, clock ticks since boot on
// Linux) but is stable for the lifetime of the process and unique when combined
// with the PID.
func ProcessStartTime(pid int) (int64, error) {
	return processStartTime(pid)
}

// ProcessName returns the OS-reported executable name for a running process.
// This may differ from the command used to start the process (e.g. when a shell
// script uses exec to replace itself with a different binary).
func ProcessName(pid int) (string, error) {
	return processName(pid)
}
