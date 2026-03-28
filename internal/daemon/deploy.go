package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/benaskins/aurelia/internal/driver"
	"github.com/benaskins/aurelia/internal/health"
)

const (
	// DefaultDrainTimeout is the default drain period before stopping the old instance.
	DefaultDrainTimeout = 5 * time.Second

	// deploySuffix is the key suffix used for temporary deploy port allocations.
	deploySuffix = "deploy"
)

// DeployService performs a zero-downtime blue-green deploy of a native service.
// It starts a new instance on a temporary port, verifies health, switches routing,
// drains the old instance, then promotes the new one.
// For services without routing config, it falls back to restart behavior.
func (d *Daemon) DeployService(name string, drainTimeout time.Duration) error {
	ms, err := d.getService(name)
	if err != nil {
		return err
	}

	// Concurrent deploy guard: reject if a deploy is already in progress.
	// The "__" separator is safe because service names are validated against
	// ^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$ — underscores are not permitted.
	if existing := d.ports.Port(name + "__" + deploySuffix); existing != 0 {
		return fmt.Errorf("deploy already in progress for %q (temp port %d)", name, existing)
	}

	// For services without routing, fall back to restart.
	// Release the old port first so the restart allocates a fresh one —
	// the old process may still be holding the port during shutdown.
	if ms.spec.Routing == nil {
		d.logger.Info("no routing config, falling back to restart", "service", name)
		if ms.spec.NeedsDynamicPort() {
			d.ports.Release(name)
		}
		return d.RestartService(name, DefaultStopTimeout)
	}

	// Services with a fixed port cannot use blue-green deploy — the new
	// instance would bind to the same port as the old one. Fall back to
	// restart, which stops the old instance first.
	if !ms.spec.NeedsDynamicPort() {
		d.logger.Info("fixed port service, falling back to restart", "service", name)
		return d.RestartService(name, DefaultStopTimeout)
	}

	d.logger.Info("starting blue-green deploy", "service", name)

	// Step 1: Allocate temporary port and start new instance
	tempPort, newDrv, err := d.deployStartNew(name, ms)
	if err != nil {
		return err
	}

	// Cleanup helper — releases temp port and stops new instance on failure
	rollback := func() {
		newDrv.Stop(context.Background(), 10*time.Second)
		newDrv.Wait()
		d.ports.ReleaseTemporary(name, deploySuffix)
	}

	// Step 2: Verify new instance is healthy
	if err := d.deployVerifyHealth(name, ms, tempPort, newDrv); err != nil {
		rollback()
		return err
	}

	// Step 3: Switch routing and drain old instance
	d.deployDrainOld(name, tempPort, drainTimeout)

	// Step 4: Promote new instance and clean up
	return d.deployPromote(name, ms, tempPort, newDrv)
}

// deployStartNew allocates a temporary port and starts the new process.
func (d *Daemon) deployStartNew(name string, ms *ManagedService) (int, driver.Driver, error) {
	tempPort, err := d.ports.AllocateTemporary(name, deploySuffix)
	if err != nil {
		return 0, nil, fmt.Errorf("allocating temporary port: %w", err)
	}
	d.logger.Info("allocated deploy port", "service", name, "port", tempPort)

	newDrv := ms.createDriverWithPort(tempPort)
	if err := newDrv.Start(d.ctx); err != nil {
		d.ports.ReleaseTemporary(name, deploySuffix)
		return 0, nil, fmt.Errorf("starting new instance: %w", err)
	}
	d.logger.Info("new instance started", "service", name, "port", tempPort, "pid", newDrv.Info().PID)

	return tempPort, newDrv, nil
}

// deployVerifyHealth runs health checks or waits for the new instance to settle.
func (d *Daemon) deployVerifyHealth(name string, ms *ManagedService, tempPort int, newDrv driver.Driver) error {
	if ms.spec.Health != nil {
		if err := d.waitForHealthy(ms, tempPort); err != nil {
			d.logger.Error("new instance unhealthy, rolling back", "service", name, "error", err)
			return fmt.Errorf("new instance failed health check: %w", err)
		}
		d.logger.Info("new instance healthy", "service", name, "port", tempPort)
		return nil
	}

	// No health check — wait briefly for the process to settle
	time.Sleep(500 * time.Millisecond)
	if newDrv.Info().State != driver.StateRunning {
		return fmt.Errorf("new instance exited immediately")
	}
	return nil
}

// deployDrainOld switches routing to the new port, then drains and stops the old instance.
func (d *Daemon) deployDrainOld(name string, tempPort int, drainTimeout time.Duration) {
	// Switch routing to new instance
	d.mu.RLock()
	d.regenerateRoutingLocked(map[string]int{name: tempPort})
	d.mu.RUnlock()
	d.logger.Info("routing switched to new instance", "service", name, "port", tempPort)

	// Wait drain period for in-flight requests on old instance
	d.logger.Info("draining old instance", "service", name, "drain", drainTimeout)
	time.Sleep(drainTimeout)

	// Stop old instance — use Stop() which handles detach + driver shutdown
	d.mu.RLock()
	oldMs := d.services[name]
	d.mu.RUnlock()

	if err := oldMs.Stop(DefaultStopTimeout); err != nil {
		d.logger.Warn("error stopping old instance during deploy", "service", name, "error", err)
	}
	d.logger.Info("old instance stopped", "service", name)
}

// deployPromote creates a new ManagedService wrapping the new driver and installs it.
func (d *Daemon) deployPromote(name string, ms *ManagedService, tempPort int, newDrv driver.Driver) error {
	newMs, err := NewManagedService(ms.spec, ms.secrets)
	if err != nil {
		d.ports.ReleaseTemporary(name, deploySuffix)
		return fmt.Errorf("creating managed service wrapper: %w", err)
	}
	newMs.allocatedPort = tempPort
	newMs.drv = newDrv
	newMs.specHash = ms.specHash

	// Set up the onStarted callback for state persistence
	newMs.onStarted = func(pid int) {
		rec := newServiceRecord(ms.spec.Service.Type, pid, tempPort, ms.spec.Service.Command)
		if err := d.state.set(name, rec); err != nil {
			d.logger.Warn("failed to save service state", "service", name, "error", err)
		}
		d.regenerateRouting()
	}

	// Start a new supervision loop for the new instance
	svcCtx, cancel := context.WithCancel(d.ctx)
	newMs.cancel = cancel
	newMs.stopped = make(chan struct{})

	// Start health monitoring for the promoted instance
	monitor := newMs.startHealthMonitor(d.ctx)
	newMs.monitor = monitor

	// Start supervision loop that watches the new process
	go newMs.superviseExisting(svcCtx, newDrv)

	// Reassign temp port allocation to primary key
	d.ports.Release(name)
	if err := d.ports.Reassign(name+"__"+deploySuffix, name); err != nil {
		d.logger.Warn("port reassign failed", "service", name, "error", err)
	}

	// Update state file
	rec := newServiceRecord(ms.spec.Service.Type, newDrv.Info().PID, tempPort, ms.spec.Service.Command)
	if err := d.state.set(name, rec); err != nil {
		d.logger.Warn("failed to save service state after deploy", "service", name, "error", err)
	}

	// Replace the managed service in the daemon
	d.mu.Lock()
	d.services[name] = newMs
	d.mu.Unlock()

	// Regenerate routing with the final state
	d.regenerateRouting()

	d.logger.Info("deploy complete", "service", name, "port", tempPort, "pid", newDrv.Info().PID)
	return nil
}

// waitForHealthy runs health checks in a loop until the service is healthy
// or the grace period + unhealthy threshold is exceeded.
func (d *Daemon) waitForHealthy(ms *ManagedService, port int) error {
	h := ms.spec.Health

	// Use the spec's explicit health port if set, otherwise use the deploy port
	healthPort := port
	if h.Port != 0 {
		healthPort = h.Port
	}

	cfg := health.Config{
		Type:    h.Type,
		Path:    h.Path,
		Port:    healthPort,
		Command: h.Command,
		Timeout: h.Timeout.Duration,
	}

	interval := h.Interval.Duration
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}

	gracePeriod := h.GracePeriod.Duration
	if gracePeriod > 0 {
		d.logger.Info("waiting for grace period", "service", ms.spec.Service.Name, "grace", gracePeriod)
		time.Sleep(gracePeriod)
	}

	threshold := h.UnhealthyThreshold
	if threshold <= 0 {
		threshold = 3
	}

	// Try up to threshold * 3 times (generous margin for slow starts)
	maxAttempts := threshold * 3
	if maxAttempts < 10 {
		maxAttempts = 10
	}

	for i := 0; i < maxAttempts; i++ {
		if err := health.SingleCheck(cfg); err == nil {
			return nil // healthy
		}
		time.Sleep(interval)
	}

	return fmt.Errorf("health check failed after %d attempts", maxAttempts)
}
