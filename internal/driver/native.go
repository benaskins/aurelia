package driver

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/benaskins/aurelia/internal/logbuf"
)

// NativeDriver manages a native (fork/exec) process.
type NativeDriver struct {
	command    string
	args       []string
	env        []string
	workingDir string

	mu        sync.Mutex
	cmd       *exec.Cmd
	state     State
	startedAt time.Time
	exitCode  int
	exitErr   string
	buf       *logbuf.Ring
	done      chan struct{}
}

// NativeConfig holds configuration for a native process.
type NativeConfig struct {
	Command    string
	Env        []string
	WorkingDir string
	BufSize    int // log ring buffer size (lines), 0 for default
}

// NewNative creates a new native process driver.
func NewNative(cfg NativeConfig) *NativeDriver {
	parts := strings.Fields(cfg.Command)
	var command string
	var args []string
	if len(parts) > 0 {
		command = parts[0]
		args = parts[1:]
	}

	bufSize := cfg.BufSize
	if bufSize <= 0 {
		bufSize = 1000
	}

	return &NativeDriver{
		command:    command,
		args:       args,
		env:        cfg.Env,
		workingDir: cfg.WorkingDir,
		state:      StateStopped,
		buf:        logbuf.New(bufSize),
	}
}

func (d *NativeDriver) Start(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.state == StateRunning || d.state == StateStarting {
		return fmt.Errorf("process already running")
	}

	d.cmd = exec.CommandContext(ctx, d.command, d.args...)
	d.cmd.Env = d.env
	if d.workingDir != "" {
		d.cmd.Dir = d.workingDir
	}

	// Capture stdout and stderr into the ring buffer
	d.cmd.Stdout = d.buf
	d.cmd.Stderr = d.buf

	// Set process group so we can kill the whole tree
	d.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	d.state = StateStarting

	if err := d.cmd.Start(); err != nil {
		d.state = StateFailed
		d.exitErr = err.Error()
		return fmt.Errorf("starting process: %w", err)
	}

	d.state = StateRunning
	d.startedAt = time.Now()
	d.done = make(chan struct{})

	// Wait for process exit in background
	go func() {
		err := d.cmd.Wait()
		d.mu.Lock()
		defer d.mu.Unlock()

		if d.state == StateStopping {
			// Expected shutdown
			d.state = StateStopped
		} else {
			d.state = StateFailed
		}

		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				d.exitCode = exitErr.ExitCode()
			}
			d.exitErr = err.Error()
		} else {
			d.exitCode = 0
		}

		close(d.done)
	}()

	return nil
}

func (d *NativeDriver) Stop(ctx context.Context, timeout time.Duration) error {
	d.mu.Lock()

	if d.state != StateRunning {
		d.mu.Unlock()
		return nil
	}

	d.state = StateStopping
	pid := d.cmd.Process.Pid
	d.mu.Unlock()

	// Send SIGTERM to the process group
	_ = syscall.Kill(-pid, syscall.SIGTERM)

	// Wait for exit or timeout
	select {
	case <-d.done:
		return nil
	case <-time.After(timeout):
		// Force kill the process group
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		<-d.done
		return nil
	case <-ctx.Done():
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		<-d.done
		return ctx.Err()
	}
}

func (d *NativeDriver) Info() ProcessInfo {
	d.mu.Lock()
	defer d.mu.Unlock()

	info := ProcessInfo{
		State:     d.state,
		StartedAt: d.startedAt,
		ExitCode:  d.exitCode,
		Error:     d.exitErr,
	}

	if d.cmd != nil && d.cmd.Process != nil {
		info.PID = d.cmd.Process.Pid
	}

	return info
}

func (d *NativeDriver) Wait() (int, error) {
	if d.done == nil {
		return -1, fmt.Errorf("process not started")
	}
	<-d.done

	d.mu.Lock()
	defer d.mu.Unlock()
	return d.exitCode, nil
}

func (d *NativeDriver) Stdout() io.Reader {
	return d.buf.Reader()
}
