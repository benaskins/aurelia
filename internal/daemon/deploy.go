package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/benaskins/aurelia/internal/driver"
	"github.com/benaskins/aurelia/internal/health"
	"github.com/benaskins/aurelia/internal/routing"
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
	d.mu.RLock()
	ms, ok := d.services[name]
	d.mu.RUnlock()
	if !ok {
		return fmt.Errorf("service %q not found", name)
	}

	// Concurrent deploy guard: reject if a deploy is already in progress
	if existing := d.ports.Port(name + "__" + deploySuffix); existing != 0 {
		return fmt.Errorf("deploy already in progress for %q (temp port %d)", name, existing)
	}

	// For services without routing, fall back to restart
	if ms.spec.Routing == nil {
		d.logger.Info("no routing config, falling back to restart", "service", name)
		return d.RestartService(name, DefaultStopTimeout)
	}

	d.logger.Info("starting blue-green deploy", "service", name)

	// Step 1: Allocate temporary port
	tempPort, err := d.ports.AllocateTemporary(name, deploySuffix)
	if err != nil {
		return fmt.Errorf("allocating temporary port: %w", err)
	}
	d.logger.Info("allocated deploy port", "service", name, "port", tempPort)

	// Cleanup helper — releases temp port on failure
	cleanup := func() {
		d.ports.ReleaseTemporary(name, deploySuffix)
	}

	// Step 2: Start new process on temporary port
	newDrv := ms.createDriverWithPort(tempPort)
	if err := newDrv.Start(d.ctx); err != nil {
		cleanup()
		return fmt.Errorf("starting new instance: %w", err)
	}
	d.logger.Info("new instance started", "service", name, "port", tempPort, "pid", newDrv.Info().PID)

	// Step 3: Run health checks against new port
	if ms.spec.Health != nil {
		if err := d.waitForHealthy(ms, tempPort); err != nil {
			d.logger.Error("new instance unhealthy, rolling back", "service", name, "error", err)
			newDrv.Stop(context.Background(), 10*time.Second)
			newDrv.Wait()
			cleanup()
			return fmt.Errorf("new instance failed health check: %w", err)
		}
		d.logger.Info("new instance healthy", "service", name, "port", tempPort)
	} else {
		// No health check — wait briefly for the process to settle
		time.Sleep(500 * time.Millisecond)
		if newDrv.Info().State != driver.StateRunning {
			cleanup()
			return fmt.Errorf("new instance exited immediately")
		}
	}

	// Step 4a: Regenerate Traefik config pointing to new port
	d.regenerateRoutingWithPortOverride(name, tempPort)
	d.logger.Info("routing switched to new instance", "service", name, "port", tempPort)

	// Step 4b: Wait drain period for in-flight requests on old instance
	d.logger.Info("draining old instance", "service", name, "drain", drainTimeout)
	time.Sleep(drainTimeout)

	// Step 4c: Cancel old supervision loop and stop old process
	d.mu.RLock()
	oldMs := d.services[name]
	d.mu.RUnlock()

	oldMonitor := oldMs.monitor
	if oldMonitor != nil {
		oldMonitor.Stop()
	}

	// Cancel the old supervision loop so it doesn't restart
	oldMs.mu.Lock()
	oldCancel := oldMs.cancel
	oldStopped := oldMs.stopped
	oldMs.mu.Unlock()

	// Stop the old driver
	if oldMs.drv != nil {
		oldMs.drv.Stop(context.Background(), DefaultStopTimeout)
		oldMs.drv.Wait()
	}

	// Cancel supervision loop
	if oldCancel != nil {
		oldCancel()
		// Wait for supervision loop to finish
		select {
		case <-oldStopped:
		case <-time.After(DefaultStopTimeout + 5*time.Second):
			d.logger.Warn("timed out waiting for old supervision loop", "service", name)
		}
	}

	d.logger.Info("old instance stopped", "service", name)

	// Step 4d: Promote — create new ManagedService wrapping the new driver
	newMs, err := NewManagedService(ms.spec, ms.secrets)
	if err != nil {
		// Should never happen — we just created one from the same spec
		cleanup()
		return fmt.Errorf("creating managed service wrapper: %w", err)
	}
	newMs.allocatedPort = tempPort
	newMs.drv = newDrv
	newMs.specHash = ms.specHash

	// Set up the onStarted callback for state persistence
	newMs.onStarted = func(pid int) {
		rec := ServiceRecord{
			Type:      ms.spec.Service.Type,
			PID:       pid,
			Port:      tempPort,
			StartedAt: time.Now().Unix(),
			Command:   ms.spec.Service.Command,
		}
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

	// Step 4e: Reassign temp port allocation to primary key
	// First release the old primary allocation, then reassign
	d.ports.Release(name)
	if err := d.ports.Reassign(name+"__"+deploySuffix, name); err != nil {
		// Non-fatal — the port is allocated, just under the wrong key
		d.logger.Warn("port reassign failed", "service", name, "error", err)
	}

	// Step 4f: Update state file
	rec := ServiceRecord{
		Type:      ms.spec.Service.Type,
		PID:       newDrv.Info().PID,
		Port:      tempPort,
		StartedAt: time.Now().Unix(),
		Command:   ms.spec.Service.Command,
	}
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

// regenerateRoutingWithPortOverride generates Traefik config with one service's
// port overridden. Used during deploy to point Traefik to the new instance
// before stopping the old one.
func (d *Daemon) regenerateRoutingWithPortOverride(serviceName string, overridePort int) {
	if d.routing == nil {
		return
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	var routes []routing.ServiceRoute
	for _, ms := range d.services {
		if ms.spec.Routing == nil {
			continue
		}
		state := ms.State()
		if state.State != driver.StateRunning {
			continue
		}

		port := ms.EffectivePort()
		if port == 0 && ms.spec.Health != nil {
			port = ms.spec.Health.Port
		}
		if port == 0 {
			continue
		}

		// Override the port for the service being deployed
		if ms.spec.Service.Name == serviceName {
			port = overridePort
		}

		routes = append(routes, routing.ServiceRoute{
			Name:       ms.spec.Service.Name,
			Hostname:   ms.spec.Routing.Hostname,
			Port:       port,
			TLS:        ms.spec.Routing.TLS,
			TLSOptions: ms.spec.Routing.TLSOptions,
		})
	}

	if err := d.routing.Generate(routes); err != nil {
		d.logger.Error("failed to regenerate routing config with override", "error", err)
	}
}
