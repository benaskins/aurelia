package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/benaskins/aurelia/internal/keychain"
	"github.com/benaskins/aurelia/internal/spec"
)

// Daemon is the top-level process supervisor.
type Daemon struct {
	specDir  string
	secrets  keychain.Store
	services map[string]*ManagedService
	deps     *depGraph
	mu       sync.RWMutex
	logger   *slog.Logger
}

// NewDaemon creates a new daemon that manages services from the given spec directory.
// The secrets store is optional — if nil, secret injection is disabled.
func NewDaemon(specDir string, opts ...DaemonOption) *Daemon {
	d := &Daemon{
		specDir:  specDir,
		services: make(map[string]*ManagedService),
		logger:   slog.With("component", "daemon"),
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// DaemonOption configures the daemon.
type DaemonOption func(*Daemon)

// WithSecrets sets the secret store for the daemon.
func WithSecrets(s keychain.Store) DaemonOption {
	return func(d *Daemon) {
		d.secrets = s
	}
}

// Start loads all specs and starts all services in dependency order.
func (d *Daemon) Start(ctx context.Context) error {
	specs, err := spec.LoadDir(d.specDir)
	if err != nil {
		return fmt.Errorf("loading specs: %w", err)
	}

	d.logger.Info("loaded service specs", "count", len(specs), "dir", d.specDir)

	g := newDepGraph(specs)
	d.mu.Lock()
	d.deps = g
	d.mu.Unlock()

	order, err := g.startOrder()
	if err != nil {
		return fmt.Errorf("dependency resolution: %w", err)
	}

	d.logger.Info("start order resolved", "order", order)

	for _, name := range order {
		s := g.specs[name]
		if err := d.startService(ctx, s); err != nil {
			d.logger.Error("failed to start service", "service", name, "error", err)
		}
	}

	return nil
}

// Stop gracefully stops all services in reverse dependency order.
func (d *Daemon) Stop(timeout time.Duration) {
	d.mu.RLock()
	g := d.deps
	d.mu.RUnlock()

	// If we have a dependency graph, stop in reverse order (dependents first)
	if g != nil {
		order, err := g.stopOrder()
		if err == nil {
			for _, name := range order {
				d.mu.RLock()
				ms, ok := d.services[name]
				d.mu.RUnlock()
				if !ok {
					continue
				}
				d.logger.Info("stopping service", "service", name)
				if err := ms.Stop(timeout); err != nil {
					d.logger.Error("error stopping service", "service", name, "error", err)
				}
			}
			d.logger.Info("all services stopped")
			return
		}
		d.logger.Warn("stop order failed, falling back to parallel stop", "error", err)
	}

	// Fallback: parallel stop (no dependency info)
	d.mu.RLock()
	services := make([]*ManagedService, 0, len(d.services))
	for _, ms := range d.services {
		services = append(services, ms)
	}
	d.mu.RUnlock()

	var wg sync.WaitGroup
	for _, ms := range services {
		wg.Add(1)
		go func(ms *ManagedService) {
			defer wg.Done()
			if err := ms.Stop(timeout); err != nil {
				d.logger.Error("error stopping service", "service", ms.spec.Service.Name, "error", err)
			}
		}(ms)
	}
	wg.Wait()

	d.logger.Info("all services stopped")
}

// StartService starts a single service by name.
func (d *Daemon) StartService(ctx context.Context, name string) error {
	d.mu.RLock()
	ms, ok := d.services[name]
	d.mu.RUnlock()

	if !ok {
		return fmt.Errorf("service %q not found", name)
	}

	return ms.Start(ctx)
}

// StopService stops a single service by name, cascading to hard dependents.
func (d *Daemon) StopService(name string, timeout time.Duration) error {
	d.mu.RLock()
	ms, ok := d.services[name]
	g := d.deps
	d.mu.RUnlock()

	if !ok {
		return fmt.Errorf("service %q not found", name)
	}

	// Cascade stop: first stop services that hard-depend on this one
	if g != nil {
		targets := g.cascadeStopTargets(name)
		for _, dep := range targets {
			d.mu.RLock()
			depMs, exists := d.services[dep]
			d.mu.RUnlock()
			if exists {
				d.logger.Info("cascade stopping dependent", "service", dep, "because", name)
				if err := depMs.Stop(timeout); err != nil {
					d.logger.Error("error cascade stopping", "service", dep, "error", err)
				}
			}
		}
	}

	return ms.Stop(timeout)
}

// RestartService stops and restarts a service.
func (d *Daemon) RestartService(ctx context.Context, name string, timeout time.Duration) error {
	if err := d.StopService(name, timeout); err != nil {
		return err
	}
	return d.StartService(ctx, name)
}

// ServiceStates returns the state of all managed services.
func (d *Daemon) ServiceStates() []ServiceState {
	d.mu.RLock()
	defer d.mu.RUnlock()

	states := make([]ServiceState, 0, len(d.services))
	for _, ms := range d.services {
		states = append(states, ms.State())
	}
	return states
}

// ServiceState returns the state of a single service.
func (d *Daemon) ServiceState(name string) (ServiceState, error) {
	d.mu.RLock()
	ms, ok := d.services[name]
	d.mu.RUnlock()

	if !ok {
		return ServiceState{}, fmt.Errorf("service %q not found", name)
	}

	return ms.State(), nil
}

// Reload re-reads specs and reconciles: start new, stop removed, restart changed.
func (d *Daemon) Reload(ctx context.Context) (*ReloadResult, error) {
	specs, err := spec.LoadDir(d.specDir)
	if err != nil {
		return nil, fmt.Errorf("loading specs: %w", err)
	}

	result := &ReloadResult{}

	// Rebuild dependency graph
	g := newDepGraph(specs)

	newSpecs := make(map[string]*spec.ServiceSpec)
	for _, s := range specs {
		newSpecs[s.Service.Name] = s
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.deps = g

	// Stop removed services
	for name, ms := range d.services {
		if _, exists := newSpecs[name]; !exists {
			d.logger.Info("removing service", "service", name)
			ms.Stop(30 * time.Second)
			delete(d.services, name)
			result.Removed = append(result.Removed, name)
		}
	}

	// Start new services
	for name, s := range newSpecs {
		if _, exists := d.services[name]; !exists {
			d.logger.Info("adding service", "service", name)
			if err := d.startServiceLocked(ctx, s); err != nil {
				d.logger.Error("failed to start new service", "service", name, "error", err)
			} else {
				result.Added = append(result.Added, name)
			}
		}
	}

	return result, nil
}

// ReloadResult summarizes what changed during a reload.
type ReloadResult struct {
	Added   []string `json:"added,omitempty"`
	Removed []string `json:"removed,omitempty"`
}

func (d *Daemon) startService(ctx context.Context, s *spec.ServiceSpec) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.startServiceLocked(ctx, s)
}

func (d *Daemon) startServiceLocked(ctx context.Context, s *spec.ServiceSpec) error {
	ms, err := NewManagedService(s, d.secrets)
	if err != nil {
		return err
	}

	if err := ms.Start(ctx); err != nil {
		return err
	}

	d.services[s.Service.Name] = ms
	d.logger.Info("started service", "service", s.Service.Name, "type", s.Service.Type)
	return nil
}
