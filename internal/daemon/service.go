package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/benaskins/aurelia/internal/driver"
	"github.com/benaskins/aurelia/internal/health"
	"github.com/benaskins/aurelia/internal/keychain"
	"github.com/benaskins/aurelia/internal/spec"
)

// ServiceState is the externally-visible state of a managed service.
type ServiceState struct {
	Name         string        `json:"name"`
	Type         string        `json:"type"`
	State        driver.State  `json:"state"`
	Health       health.Status `json:"health"`
	PID          int           `json:"pid,omitempty"`
	Uptime       string        `json:"uptime,omitempty"`
	RestartCount int           `json:"restart_count"`
	LastError    string        `json:"last_error,omitempty"`
}

// ManagedService ties a spec to a running driver with restart and health monitoring.
type ManagedService struct {
	spec    *spec.ServiceSpec
	drv     driver.Driver
	monitor *health.Monitor
	secrets keychain.Store
	logger  *slog.Logger

	mu           sync.Mutex
	restartCount int
	cancel       context.CancelFunc
	stopped      chan struct{}
	// onStarted is called after a process starts successfully (for state persistence)
	onStarted func(pid int)

	// unhealthyCh signals the supervision loop to restart due to health failure
	unhealthyCh chan struct{}
	// adoptedDrv is set when recovering a previously-running process
	adoptedDrv driver.Driver
}

// NewManagedService creates a managed service from a spec.
// The secrets store is optional — if nil, secret refs in the spec are skipped.
func NewManagedService(s *spec.ServiceSpec, secrets keychain.Store) (*ManagedService, error) {
	switch s.Service.Type {
	case "native", "container":
		// supported
	default:
		return nil, fmt.Errorf("unsupported service type %q (expected native or container)", s.Service.Type)
	}

	return &ManagedService{
		spec:        s,
		secrets:     secrets,
		logger:      slog.With("service", s.Service.Name),
		unhealthyCh: make(chan struct{}, 1),
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
	monitor := ms.monitor
	ms.mu.Unlock()

	if cancel == nil {
		return nil
	}

	// Stop health monitoring first
	if monitor != nil {
		monitor.Stop()
	}

	// Stop the driver (graceful SIGTERM → SIGKILL)
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
		Health:       health.StatusUnknown,
	}

	if ms.monitor != nil {
		st.Health = ms.monitor.CurrentStatus()
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

func (ms *ManagedService) supervise(ctx context.Context) {
	defer func() {
		ms.mu.Lock()
		ms.cancel = nil
		close(ms.stopped)
		ms.mu.Unlock()
	}()

	for {
		// On first iteration, use adopted driver if recovering a running process
		var drv driver.Driver
		ms.mu.Lock()
		if ms.adoptedDrv != nil {
			drv = ms.adoptedDrv
			ms.adoptedDrv = nil
			ms.mu.Unlock()
			ms.logger.Info("adopted running process", "pid", drv.Info().PID)
		} else {
			ms.mu.Unlock()
			drv = ms.createDriver()
		}

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

		// Notify daemon of PID for state persistence
		if ms.onStarted != nil {
			ms.onStarted(drv.Info().PID)
		}

		// Start health monitoring if configured
		monitor := ms.startHealthMonitor(ctx)
		ms.mu.Lock()
		ms.monitor = monitor
		ms.mu.Unlock()

		// Wait for process to exit OR health check to trigger restart
		select {
		case <-ms.waitForExit(drv):
			// Process exited on its own
			if monitor != nil {
				monitor.Stop()
			}
		case <-ms.unhealthyCh:
			// Health check triggered restart
			ms.logger.Warn("restarting due to health check failure")
			if monitor != nil {
				monitor.Stop()
			}
			drv.Stop(ctx, 30*time.Second)
			// Drain the done channel
			drv.Wait()
		}

		exitCode := drv.Info().ExitCode

		if ctx.Err() != nil {
			return
		}

		ms.logger.Info("process exited", "exit_code", exitCode)

		// Check restart policy
		if !ms.shouldRestart() {
			ms.logger.Info("restart policy exhausted, giving up")
			return
		}

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

func (ms *ManagedService) waitForExit(drv driver.Driver) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		drv.Wait()
		close(ch)
	}()
	return ch
}

func (ms *ManagedService) startHealthMonitor(ctx context.Context) *health.Monitor {
	if ms.spec.Health == nil {
		return nil
	}

	h := ms.spec.Health
	port := h.Port
	if port == 0 && ms.spec.Network != nil {
		port = ms.spec.Network.Port
	}

	cfg := health.Config{
		Type:               h.Type,
		Path:               h.Path,
		Port:               port,
		Command:            h.Command,
		Interval:           h.Interval.Duration,
		Timeout:            h.Timeout.Duration,
		GracePeriod:        h.GracePeriod.Duration,
		UnhealthyThreshold: h.UnhealthyThreshold,
	}

	monitor := health.NewMonitor(cfg, ms.logger, func() {
		// Signal the supervision loop to restart
		select {
		case ms.unhealthyCh <- struct{}{}:
		default:
			// Already signaled
		}
	})

	monitor.Start(ctx)
	return monitor
}

func (ms *ManagedService) createDriver() driver.Driver {
	env := ms.buildEnv()

	switch ms.spec.Service.Type {
	case "container":
		d, err := driver.NewContainer(driver.ContainerConfig{
			Name:        ms.spec.Service.Name,
			Image:       ms.spec.Service.Image,
			Env:         env,
			NetworkMode: ms.spec.Service.NetworkMode,
			Volumes:     ms.spec.Volumes,
		})
		if err != nil {
			ms.logger.Error("failed to create container driver", "error", err)
			// Fall through — the start will fail gracefully
			return driver.NewNative(driver.NativeConfig{Command: "false"})
		}
		return d
	default:
		return driver.NewNative(driver.NativeConfig{
			Command:    ms.spec.Service.Command,
			Env:        env,
			WorkingDir: ms.spec.Service.WorkingDir,
		})
	}
}

func (ms *ManagedService) buildEnv() []string {
	// For native: inherit host env. For containers: clean env.
	var env []string
	if ms.spec.Service.Type == "native" {
		env = os.Environ()
	}

	for k, v := range ms.spec.Env {
		env = append(env, k+"="+v)
	}

	// Resolve secrets from Keychain and inject as env vars
	if ms.secrets != nil && len(ms.spec.Secrets) > 0 {
		for envVar, ref := range ms.spec.Secrets {
			val, err := ms.secrets.Get(ref.Keychain)
			if err != nil {
				ms.logger.Warn("secret not found, skipping", "env_var", envVar, "keychain_key", ref.Keychain, "error", err)
				continue
			}
			env = append(env, envVar+"="+val)
			ms.logger.Info("injected secret", "env_var", envVar)
		}
	}

	return env
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
