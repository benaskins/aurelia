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
	Port         int           `json:"port,omitempty"`
	Uptime       string        `json:"uptime,omitempty"`
	RestartCount int           `json:"restart_count"`
	LastExitCode int           `json:"last_exit_code,omitempty"`
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
	// allocatedPort is set when the service uses dynamic port allocation
	allocatedPort int
	// specHash is the SHA-256 hash of the spec at startup, used for change detection on reload
	specHash string
}

// NewManagedService creates a managed service from a spec.
// The secrets store is optional — if nil, secret refs in the spec are skipped.
func NewManagedService(s *spec.ServiceSpec, secrets keychain.Store) (*ManagedService, error) {
	switch s.Service.Type {
	case "native", "container", "external":
		// supported
	default:
		return nil, fmt.Errorf("unsupported service type %q (expected native, container, or external)", s.Service.Type)
	}

	return &ManagedService{
		spec:        s,
		secrets:     secrets,
		logger:      slog.With("service", s.Service.Name),
		unhealthyCh: make(chan struct{}, 1),
	}, nil
}

// IsExternal returns true for external (unmanaged) services.
func (ms *ManagedService) IsExternal() bool {
	return ms.spec.Service.Type == "external"
}

// EffectivePort returns the dynamically allocated port if set,
// otherwise the static port from the spec.
func (ms *ManagedService) EffectivePort() int {
	if ms.allocatedPort != 0 {
		return ms.allocatedPort
	}
	if ms.spec.Network != nil {
		return ms.spec.Network.Port
	}
	return 0
}

// Start begins running the service with restart supervision.
// For external services, it starts health monitoring only (no process supervision).
func (ms *ManagedService) Start(ctx context.Context) error {
	ms.mu.Lock()
	if ms.cancel != nil {
		ms.mu.Unlock()
		return fmt.Errorf("service %s already running", ms.spec.Service.Name)
	}

	svcCtx, cancel := context.WithCancel(ctx)
	ms.cancel = cancel
	ms.stopped = make(chan struct{})

	if ms.IsExternal() {
		monitor := ms.startHealthMonitor(svcCtx)
		ms.monitor = monitor
		ms.mu.Unlock()
		go func() {
			<-svcCtx.Done()
			if monitor != nil {
				monitor.Stop()
			}
			ms.mu.Lock()
			ms.cancel = nil
			close(ms.stopped)
			ms.mu.Unlock()
		}()
		return nil
	}

	ms.mu.Unlock()

	go ms.supervise(svcCtx)
	return nil
}

// Stop gracefully stops the service and its supervision loop.
// For external services, it stops health monitoring only.
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

	// Stop the driver (graceful SIGTERM → SIGKILL) — skip for external
	if drv != nil {
		if err := drv.Stop(context.Background(), timeout); err != nil {
			ms.logger.Warn("error stopping service", "error", err)
		}
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

// Release detaches supervision without killing the underlying process.
// It stops health monitoring, cancels the supervision context (causing the
// supervise goroutine to exit), and waits for it to finish. Unlike Stop(),
// it does NOT call drv.Stop() — the process is left running as an orphan.
func (ms *ManagedService) Release(timeout time.Duration) error {
	ms.mu.Lock()
	cancel := ms.cancel
	stopped := ms.stopped
	monitor := ms.monitor
	ms.mu.Unlock()

	if cancel == nil {
		return nil
	}

	// Stop health monitoring
	if monitor != nil {
		monitor.Stop()
	}

	// Cancel the supervision context — this causes supervise() to exit
	// via ctx.Done() without killing the process
	cancel()

	// Wait for supervision goroutine to finish
	select {
	case <-stopped:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("timed out waiting for service %s to release", ms.spec.Service.Name)
	}
}

// Logs returns the last n lines from the service log buffer.
func (ms *ManagedService) Logs(n int) []string {
	ms.mu.Lock()
	drv := ms.drv
	ms.mu.Unlock()

	if drv == nil {
		return nil
	}
	return drv.LogLines(n)
}

// State returns the current service state.
// For external services, state is always "running" — we observe health, not lifecycle.
func (ms *ManagedService) State() ServiceState {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	st := ServiceState{
		Name:         ms.spec.Service.Name,
		Type:         ms.spec.Service.Type,
		Port:         ms.EffectivePort(),
		RestartCount: ms.restartCount,
		Health:       health.StatusUnknown,
	}

	if ms.monitor != nil {
		st.Health = ms.monitor.CurrentStatus()
	}

	if ms.IsExternal() {
		st.State = driver.StateRunning
		if ms.spec.Health != nil {
			st.Port = ms.spec.Health.Port
		}
		return st
	}

	if ms.drv != nil {
		info := ms.drv.Info()
		st.State = info.State
		st.PID = info.PID
		st.LastExitCode = info.ExitCode
		st.LastError = info.Error
		if info.State == driver.StateRunning && !info.StartedAt.IsZero() {
			st.Uptime = time.Since(info.StartedAt).Truncate(time.Second).String()
		}
	} else {
		st.State = driver.StateStopped
	}

	return st
}

// supervisionPhase represents a phase in the service supervision lifecycle.
type supervisionPhase int

const (
	phaseStarting   supervisionPhase = iota // Create driver and start the process
	phaseRunning                            // Wait for process exit or health failure
	phaseEvaluating                         // Decide whether to restart based on exit code and policy
	phaseRestarting                         // Wait for restart delay, then loop back to starting
	phaseStopped                            // Terminal — supervision is done
)

func (ms *ManagedService) supervise(ctx context.Context) {
	defer func() {
		ms.mu.Lock()
		ms.cancel = nil
		close(ms.stopped)
		ms.mu.Unlock()
	}()

	phase := phaseStarting
	var drv driver.Driver

	for phase != phaseStopped {
		switch phase {
		case phaseStarting:
			drv, phase = ms.handleStarting(ctx)
		case phaseRunning:
			phase = ms.handleRunning(ctx, drv)
		case phaseEvaluating:
			phase = ms.handleEvaluating(ctx, drv)
		case phaseRestarting:
			phase = ms.handleRestarting(ctx)
		}
	}
}

// superviseExisting enters the supervision loop with an already-running process.
// Used after blue-green deploy promotion to monitor the new instance.
func (ms *ManagedService) superviseExisting(ctx context.Context, drv driver.Driver) {
	defer func() {
		ms.mu.Lock()
		ms.cancel = nil
		close(ms.stopped)
		ms.mu.Unlock()
	}()

	phase := phaseRunning
	for phase != phaseStopped {
		switch phase {
		case phaseRunning:
			phase = ms.handleRunning(ctx, drv)
		case phaseEvaluating:
			phase = ms.handleEvaluating(ctx, drv)
		case phaseRestarting:
			phase = ms.handleRestarting(ctx)
		case phaseStarting:
			// After first restart, fall into the normal create-and-start path
			drv, phase = ms.handleStarting(ctx)
		}
	}
}

// handleStarting creates (or adopts) a driver and starts the process.
// Returns the driver and the next phase.
func (ms *ManagedService) handleStarting(ctx context.Context) (driver.Driver, supervisionPhase) {
	ms.mu.Lock()
	if ms.adoptedDrv != nil {
		drv := ms.adoptedDrv
		ms.adoptedDrv = nil
		ms.mu.Unlock()
		ms.logger.Info("adopted running process", "pid", drv.Info().PID)

		ms.mu.Lock()
		ms.drv = drv
		ms.mu.Unlock()
		return drv, phaseRunning
	}
	ms.mu.Unlock()

	drv := ms.createDriver()
	ms.mu.Lock()
	ms.drv = drv
	ms.mu.Unlock()

	ms.logger.Info("starting process")
	if err := drv.Start(ctx); err != nil {
		ms.logger.Error("failed to start", "error", err)

		if ctx.Err() != nil {
			return drv, phaseStopped
		}
		if !ms.shouldRestart() {
			ms.logger.Info("restart policy exhausted, giving up")
			return drv, phaseStopped
		}
		return drv, phaseRestarting
	}

	if ms.onStarted != nil {
		ms.onStarted(drv.Info().PID)
	}

	monitor := ms.startHealthMonitor(ctx)
	ms.mu.Lock()
	ms.monitor = monitor
	ms.mu.Unlock()

	return drv, phaseRunning
}

// handleRunning waits for the process to exit or a health check to trigger restart.
func (ms *ManagedService) handleRunning(ctx context.Context, drv driver.Driver) supervisionPhase {
	select {
	case <-ms.waitForExit(drv):
		ms.stopMonitor()
	case <-ms.unhealthyCh:
		ms.logger.Warn("restarting due to health check failure")
		ms.stopMonitor()
		drv.Stop(ctx, 30*time.Second)
		drv.Wait()
	case <-ctx.Done():
		return phaseStopped
	}
	return phaseEvaluating
}

// handleEvaluating checks the exit code and restart policy to decide the next phase.
func (ms *ManagedService) handleEvaluating(ctx context.Context, drv driver.Driver) supervisionPhase {
	exitCode := drv.Info().ExitCode

	if ctx.Err() != nil {
		return phaseStopped
	}

	ms.logger.Info("process exited", "exit_code", exitCode)

	if !ms.shouldRestart() {
		ms.logger.Info("restart policy exhausted, giving up")
		return phaseStopped
	}

	policy := "on-failure"
	if ms.spec.Restart != nil {
		policy = ms.spec.Restart.Policy
	}

	switch policy {
	case "never":
		ms.logger.Info("restart policy is 'never', stopping")
		return phaseStopped
	case "on-failure":
		if exitCode == 0 {
			ms.logger.Info("process exited cleanly, not restarting (policy: on-failure)")
			return phaseStopped
		}
	case "always":
		// Continue to restart
	}

	ms.mu.Lock()
	ms.restartCount++
	ms.mu.Unlock()

	return phaseRestarting
}

// handleRestarting waits for the restart delay before transitioning back to starting.
func (ms *ManagedService) handleRestarting(ctx context.Context) supervisionPhase {
	delay := ms.restartDelay()
	ms.logger.Info("restarting after delay", "delay", delay, "restart_count", ms.restartCount)

	select {
	case <-time.After(delay):
		return phaseStarting
	case <-ctx.Done():
		return phaseStopped
	}
}

// stopMonitor stops the health monitor if one is running.
func (ms *ManagedService) stopMonitor() {
	ms.mu.Lock()
	monitor := ms.monitor
	ms.mu.Unlock()
	if monitor != nil {
		monitor.Stop()
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
	if port == 0 {
		port = ms.EffectivePort()
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

// createDriverWithPort creates a driver configured to listen on the given port.
// Used during blue-green deploys.
func (ms *ManagedService) createDriverWithPort(port int) driver.Driver {
	env := ms.buildEnvWithPort(port)

	switch ms.spec.Service.Type {
	case "container":
		d, err := driver.NewContainer(driver.ContainerConfig{
			Name:        ms.spec.Service.Name + "-deploy",
			Image:       ms.spec.Service.Image,
			Env:         env,
			Cmd:         ms.spec.Args,
			NetworkMode: ms.spec.Service.NetworkMode,
			Volumes:     ms.spec.Volumes,
		})
		if err != nil {
			ms.logger.Error("failed to create container driver for deploy", "error", err)
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

func (ms *ManagedService) createDriver() driver.Driver {
	env := ms.buildEnv()

	switch ms.spec.Service.Type {
	case "container":
		d, err := driver.NewContainer(driver.ContainerConfig{
			Name:        ms.spec.Service.Name,
			Image:       ms.spec.Service.Image,
			Env:         env,
			Cmd:         ms.spec.Args,
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

// buildEnvWithPort builds the environment with an explicit port override.
// Used during blue-green deploys to start a new instance on a temporary port.
func (ms *ManagedService) buildEnvWithPort(port int) []string {
	// For native: inherit host env. For containers: clean env.
	var env []string
	if ms.spec.Service.Type == "native" {
		env = os.Environ()
	}

	// Use the provided port override
	env = append(env, fmt.Sprintf("PORT=%d", port))

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

func (ms *ManagedService) buildEnv() []string {
	// For native: inherit host env. For containers: clean env.
	var env []string
	if ms.spec.Service.Type == "native" {
		env = os.Environ()
	}

	// Inject port as PORT env var (dynamic or static)
	if ms.allocatedPort != 0 {
		env = append(env, fmt.Sprintf("PORT=%d", ms.allocatedPort))
	} else if ms.spec.Network != nil && ms.spec.Network.Port != 0 {
		env = append(env, fmt.Sprintf("PORT=%d", ms.spec.Network.Port))
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
			if delay <= 0 { // overflow
				delay = 24 * time.Hour
				break
			}
		}

		if maxDelay := ms.spec.Restart.MaxDelay.Duration; maxDelay > 0 && delay > maxDelay {
			delay = maxDelay
		}
	}

	return delay
}
