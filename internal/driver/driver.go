package driver

import (
	"context"
	"io"
	"time"
)

// State represents the lifecycle state of a managed process.
type State string

const (
	StateStopped  State = "stopped"
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateStopping State = "stopping"
	StateFailed   State = "failed"
)

// ProcessInfo holds runtime information about a managed process.
type ProcessInfo struct {
	PID       int
	State     State
	StartedAt time.Time
	ExitCode  int
	Error     string
}

// Driver is the interface for process lifecycle management.
// Native and container drivers both implement this.
type Driver interface {
	// Start launches the process and returns immediately.
	// The process runs in the background.
	Start(ctx context.Context) error

	// Stop sends a graceful shutdown signal, waits up to timeout,
	// then force-kills if still running.
	Stop(ctx context.Context, timeout time.Duration) error

	// Info returns current process state and metadata.
	Info() ProcessInfo

	// Wait blocks until the process exits and returns the exit code.
	Wait() (int, error)

	// Stdout returns a reader for the process's combined stdout/stderr output.
	Stdout() io.Reader
}
