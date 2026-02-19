package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/benaskins/aurelia/internal/driver"
	"github.com/benaskins/aurelia/internal/spec"
)

// ServiceState is the externally-visible state of a managed service.
type ServiceState struct {
	Name         string       `json:"name"`
	Type         string       `json:"type"`
	State        driver.State `json:"state"`
	PID          int          `json:"pid,omitempty"`
	Uptime       string       `json:"uptime,omitempty"`
	RestartCount int          `json:"restart_count"`
	LastError    string       `json:"last_error,omitempty"`
}

// ManagedService ties a spec to a running driver with restart logic.
type ManagedService struct {
	spec   *spec.ServiceSpec
	drv    driver.Driver
	logger *slog.Logger

	mu           sync.Mutex
	restartCount int
	cancel       context.CancelFunc
	stopped      chan struct{}
}

// NewManagedService creates a managed service from a spec.
func NewManagedService(s *spec.ServiceSpec) (*ManagedService, error) {
	if s.Service.Type != "native" {
		return nil, fmt.Errorf("only native services are supported (got %q)", s.Service.Type)
	}

	return &ManagedService{
		spec:   s,
		logger: slog.With("service", s.Service.Name),
	}, nil
}

// Start begins running the service with restart supervision.
func (ms *ManagedService) Start(ctx context.Context) error {
	ms.mu.Lock()
	if ms.cancel != nil {
		ms.mu.Unlock()
		return fmt.Errorf("service %s already running", ms.spec.Service.Name)
	}

	svcCtx, cancel := context.WithCancel(ctx)
	ms.cancel = cancel
	ms.stopped = make(chan struct{})
	ms.mu.Unlock()

	go ms.supervise(svcCtx)
	return nil
}

// Stop gracefully stops the service and its supervision loop.
func (ms *ManagedService) Stop(timeout time.Duration) error {
	ms.mu.Lock()
	cancel := ms.cancel
	stopped := ms.stopped
	drv := ms.drv
	ms.mu.Unlock()

	if cancel == nil {
		return nil
	}

	// Stop the driver first (graceful SIGTERM → SIGKILL)
	if drv != nil {
		drv.Stop(context.Background(), timeout)
	}

	// Then cancel the supervision loop
	cancel()

	// Wait for supervision loop to finish
	select {
	case <-stopped:
		return nil
	case <-time.After(timeout + 5*time.Second):
		return fmt.Errorf("timed out waiting for service %s to stop", ms.spec.Service.Name)
	}
}

// State returns the current service state.
func (ms *ManagedService) State() ServiceState {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	st := ServiceState{
		Name:         ms.spec.Service.Name,
		Type:         ms.spec.Service.Type,
		RestartCount: ms.restartCount,
	}

	if ms.drv != nil {
		info := ms.drv.Info()
		st.State = info.State
		st.PID = info.PID
		st.LastError = info.Error
		if info.State == driver.StateRunning && !info.StartedAt.IsZero() {
			st.Uptime = time.Since(info.StartedAt).Truncate(time.Second).String()
		}
	} else {
		st.State = driver.StateStopped
	}

	return st
}

// Logs returns the last n lines from the process output buffer.
func (ms *ManagedService) Logs(n int) []string {
	ms.mu.Lock()
	drv := ms.drv
	ms.mu.Unlock()

	if drv == nil {
		return nil
	}

	if nd, ok := drv.(*driver.NativeDriver); ok {
		_ = nd // access ring buffer through driver
	}
	return nil
}

func (ms *ManagedService) supervise(ctx context.Context) {
	defer func() {
		ms.mu.Lock()
		ms.cancel = nil
		close(ms.stopped)
		ms.mu.Unlock()
	}()

	for {
		drv := ms.createDriver()

		ms.mu.Lock()
		ms.drv = drv
		ms.mu.Unlock()

		ms.logger.Info("starting process")
		if err := drv.Start(ctx); err != nil {
			ms.logger.Error("failed to start", "error", err)

			if ctx.Err() != nil {
				return
			}

			if !ms.shouldRestart() {
				ms.logger.Info("restart policy exhausted, giving up")
				return
			}

			delay := ms.restartDelay()
			ms.logger.Info("restarting after delay", "delay", delay)
			select {
			case <-time.After(delay):
				continue
			case <-ctx.Done():
				return
			}
		}

		// Wait for process to exit
		exitCode, _ := drv.Wait()
		ms.logger.Info("process exited", "exit_code", exitCode)

		if ctx.Err() != nil {
			// We were asked to stop
			return
		}

		// Check restart policy
		if !ms.shouldRestart() {
			ms.logger.Info("restart policy exhausted, giving up")
			return
		}

		// Check if we should restart based on exit code and policy
		policy := "on-failure"
		if ms.spec.Restart != nil {
			policy = ms.spec.Restart.Policy
		}

		switch policy {
		case "never":
			ms.logger.Info("restart policy is 'never', stopping")
			return
		case "on-failure":
			if exitCode == 0 {
				ms.logger.Info("process exited cleanly, not restarting (policy: on-failure)")
				return
			}
		case "always":
			// Always restart
		}

		ms.mu.Lock()
		ms.restartCount++
		ms.mu.Unlock()

		delay := ms.restartDelay()
		ms.logger.Info("restarting after delay", "delay", delay, "restart_count", ms.restartCount)

		select {
		case <-time.After(delay):
			continue
		case <-ctx.Done():
			return
		}
	}
}

func (ms *ManagedService) createDriver() driver.Driver {
	env := os.Environ()
	for k, v := range ms.spec.Env {
		env = append(env, k+"="+v)
	}

	return driver.NewNative(driver.NativeConfig{
		Command:    ms.spec.Service.Command,
		Env:        env,
		WorkingDir: ms.spec.Service.WorkingDir,
	})
}

func (ms *ManagedService) shouldRestart() bool {
	if ms.spec.Restart == nil {
		return false
	}

	maxAttempts := ms.spec.Restart.MaxAttempts
	if maxAttempts <= 0 {
		return true // unlimited
	}

	ms.mu.Lock()
	count := ms.restartCount
	ms.mu.Unlock()

	return count < maxAttempts
}

func (ms *ManagedService) restartDelay() time.Duration {
	if ms.spec.Restart == nil {
		return 5 * time.Second
	}

	delay := ms.spec.Restart.Delay.Duration
	if delay <= 0 {
		delay = 5 * time.Second
	}

	if ms.spec.Restart.Backoff == "exponential" {
		ms.mu.Lock()
		count := ms.restartCount
		ms.mu.Unlock()

		for i := 0; i < count; i++ {
			delay *= 2
		}

		if maxDelay := ms.spec.Restart.MaxDelay.Duration; maxDelay > 0 && delay > maxDelay {
			delay = maxDelay
		}
	}

	return delay
}
