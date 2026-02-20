package driver

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/benaskins/aurelia/internal/logbuf"
)

// AdoptedDriver monitors an existing process by PID (crash recovery).
type AdoptedDriver struct {
	pid int

	mu        sync.Mutex
	state     State
	startedAt time.Time
	exitCode  int
	exitErr   string
	buf       *logbuf.Ring
	done      chan struct{}
	stopCh    chan struct{} // signals monitor to stop polling
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
		buf:       logbuf.New(100),
		done:      make(chan struct{}),
		stopCh:    make(chan struct{}),
	}

	go d.monitor()
	return d, nil
}

func (d *AdoptedDriver) monitor() {
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
	if d.state != StateRunning {
		d.mu.Unlock()
		return nil
	}
	d.state = StateStopping
	d.mu.Unlock()

	// Stop the monitor — we'll handle exit detection ourselves
	close(d.stopCh)

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

func (d *AdoptedDriver) Stdout() io.Reader {
	return d.buf.Reader()
}

// VerifyProcess checks whether the process at the given PID matches the expected
// command name. This guards against PID reuse: if the OS recycled the PID for a
// different process, the command won't match and adoption should be skipped.
// Returns true if the process matches or if expectedCommand is empty (best effort).
func VerifyProcess(pid int, expectedCommand string) bool {
	if expectedCommand == "" {
		return true // no command recorded, best effort
	}

	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return false // process not found or ps failed
	}

	actual := strings.TrimSpace(string(out))
	if actual == "" {
		return false
	}

	// Compare base names — the spec command may be a full path
	expectedBase := expectedCommand
	if idx := strings.LastIndex(expectedCommand, "/"); idx >= 0 {
		expectedBase = expectedCommand[idx+1:]
	}

	actualBase := actual
	if idx := strings.LastIndex(actual, "/"); idx >= 0 {
		actualBase = actual[idx+1:]
	}

	return actualBase == expectedBase
}
