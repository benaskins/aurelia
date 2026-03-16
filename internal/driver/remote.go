package driver

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

// RemoteConfig holds configuration for a remote service driver.
type RemoteConfig struct {
	StartCmd   string
	StopCmd    string
	RestartCmd string
}

// RemoteDriver manages a remote service via shell hook commands.
// It has no PID — lifecycle is managed by executing hook commands
// (e.g. wrangler deploy, wrangler delete). Health is monitored
// separately by the health checker.
type RemoteDriver struct {
	cfg     RemoteConfig
	mu      sync.Mutex
	state   State
	started time.Time
	err     string
	done    chan struct{}
}

// NewRemote creates a RemoteDriver.
func NewRemote(cfg RemoteConfig) *RemoteDriver {
	return &RemoteDriver{
		cfg:   cfg,
		state: StateStopped,
		done:  make(chan struct{}),
	}
}

// Start executes the start hook command.
func (d *RemoteDriver) Start(ctx context.Context) error {
	d.mu.Lock()
	d.state = StateStarting
	d.done = make(chan struct{})
	d.mu.Unlock()

	if err := runHook(ctx, d.cfg.StartCmd); err != nil {
		d.mu.Lock()
		d.state = StateFailed
		d.err = err.Error()
		close(d.done)
		d.mu.Unlock()
		return fmt.Errorf("start hook failed: %w", err)
	}

	d.mu.Lock()
	d.state = StateRunning
	d.started = time.Now()
	d.err = ""
	d.mu.Unlock()

	return nil
}

// Stop executes the stop hook command if one is configured.
func (d *RemoteDriver) Stop(ctx context.Context, timeout time.Duration) error {
	d.mu.Lock()
	d.state = StateStopping
	d.mu.Unlock()

	if d.cfg.StopCmd != "" {
		stopCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		if err := runHook(stopCtx, d.cfg.StopCmd); err != nil {
			d.mu.Lock()
			d.state = StateFailed
			d.err = err.Error()
			close(d.done)
			d.mu.Unlock()
			return fmt.Errorf("stop hook failed: %w", err)
		}
	}

	d.mu.Lock()
	d.state = StateStopped
	close(d.done)
	d.mu.Unlock()

	return nil
}

// Info returns the current state. PID is always 0 for remote services.
func (d *RemoteDriver) Info() ProcessInfo {
	d.mu.Lock()
	defer d.mu.Unlock()
	return ProcessInfo{
		PID:       0,
		State:     d.state,
		StartedAt: d.started,
		Error:     d.err,
	}
}

// Wait blocks until the remote service is stopped.
func (d *RemoteDriver) Wait() (int, error) {
	<-d.done
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state == StateFailed {
		return 1, fmt.Errorf("remote service failed: %s", d.err)
	}
	return 0, nil
}

// LogLines returns nil — remote services don't have local log capture.
func (d *RemoteDriver) LogLines(n int) []string {
	return nil
}

func runHook(ctx context.Context, command string) error {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	return cmd.Run()
}
