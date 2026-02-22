package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/benaskins/aurelia/internal/driver"
	"github.com/benaskins/aurelia/internal/keychain"
	"github.com/benaskins/aurelia/internal/port"
	"github.com/benaskins/aurelia/internal/routing"
	"github.com/benaskins/aurelia/internal/spec"
)

const (
	// DefaultStopTimeout is the default graceful shutdown timeout for services.
	DefaultStopTimeout = 30 * time.Second

	// defaultPortMin is the lower bound of the dynamic port allocation range.
	defaultPortMin = 20000

	// defaultPortMax is the upper bound of the dynamic port allocation range.
	defaultPortMax = 32000
)

// Daemon is the top-level process supervisor.
type Daemon struct {
	specDir      string
	stateDir     string
	secrets      keychain.Store
	routing      *routing.TraefikGenerator
	ports        *port.Allocator
	services     map[string]*ManagedService
	deps         *depGraph
	state        *stateFile
	mu           sync.RWMutex
	logger       *slog.Logger
	ctx          context.Context // daemon lifecycle context, set in Start()
	adopted      []string        // services adopted during crash recovery, pending redeploy
	redeployWait time.Duration   // delay before redeploying adopted services (default 10s)
}

// NewDaemon creates a new daemon that manages services from the given spec directory.
// The secrets store is optional — if nil, secret injection is disabled.
func NewDaemon(specDir string, opts ...Option) *Daemon {
	d := &Daemon{
		specDir:  specDir,
		stateDir: specDir, // default: same as spec dir
		ports:    port.NewAllocator(defaultPortMin, defaultPortMax),
		services: make(map[string]*ManagedService),
		logger:   slog.With("component", "daemon"),
	}
	for _, opt := range opts {
		opt(d)
	}
	d.state = newStateFile(d.stateDir)
	return d
}

// Option configures the daemon.
type Option func(*Daemon)

// WithSecrets sets the secret store for the daemon.
func WithSecrets(s keychain.Store) Option {
	return func(d *Daemon) {
		d.secrets = s
	}
}

// WithStateDir sets the directory for the daemon state file.
func WithStateDir(dir string) Option {
	return func(d *Daemon) {
		d.stateDir = dir
	}
}

// WithPortRange sets the dynamic port allocation range.
func WithPortRange(min, max int) Option {
	return func(d *Daemon) {
		d.ports = port.NewAllocator(min, max)
	}
}

// WithRouting enables Traefik config generation at the given output path.
func WithRouting(outputPath string) Option {
	return func(d *Daemon) {
		d.routing = routing.NewTraefikGenerator(outputPath)
	}
}

// Start loads all specs and starts all services in dependency order.
func (d *Daemon) Start(ctx context.Context) error {
	d.ctx = ctx

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

	// Load previous state for crash recovery
	prevState, err := d.state.load()
	if err != nil {
		d.logger.Warn("failed to load previous state", "error", err)
	}

	// Restore port allocations from previous state
	for name, rec := range prevState {
		if rec.Port > 0 {
			if err := d.ports.Reserve(name, rec.Port); err != nil {
				d.logger.Warn("failed to reserve previous port", "service", name, "port", rec.Port, "error", err)
			}
		}
	}

	for _, name := range order {
		s := g.specs[name]

		// Try to adopt a previously-running process
		if rec, ok := prevState[name]; ok && rec.Type == "native" && rec.PID > 0 {
			// Verify the PID still belongs to the expected process (guard against PID reuse)
			if !driver.VerifyProcess(rec.PID, rec.Command) {
				d.logger.Warn("PID reuse detected, skipping adoption",
					"service", name, "pid", rec.PID,
					"expected_command", rec.Command)
			} else {
				adopted, err := driver.NewAdopted(rec.PID)
				if err == nil {
					d.logger.Info("recovering running process", "service", name, "pid", rec.PID)
					if err := d.adoptService(ctx, s, adopted); err != nil {
						d.logger.Error("failed to adopt service", "service", name, "error", err)
					} else {
						d.adopted = append(d.adopted, name)
						continue
					}
				} else {
					d.logger.Info("previous process not running", "service", name, "pid", rec.PID)
				}
			}
		}

		if err := d.startService(ctx, s); err != nil {
			d.logger.Error("failed to start service", "service", name, "error", err)
			continue
		}

		// Wait for health if other services require this one
		if g.hasRequiredDependents(name) && s.Health != nil {
			d.mu.RLock()
			ms := d.services[name]
			d.mu.RUnlock()

			port := ms.EffectivePort()
			d.logger.Info("waiting for dependency to become healthy", "service", name)
			if err := d.waitForHealthy(ms, port); err != nil {
				d.logger.Error("dependency failed health check", "service", name, "error", err)
			}
		}
	}

	// Generate initial routing config
	d.regenerateRouting()

	// Redeploy adopted services in the background to restore log capture
	go d.redeployAdopted()

	// Start file watcher for auto-reload
	go func() {
		if err := d.StartWatcher(ctx); err != nil {
			d.logger.Error("spec file watcher failed", "error", err)
		}
	}()

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
			if err := d.state.save(map[string]ServiceRecord{}); err != nil {
				d.logger.Warn("failed to clear state on shutdown", "error", err)
			}
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
	if err := d.state.save(map[string]ServiceRecord{}); err != nil {
		d.logger.Warn("failed to clear state on shutdown", "error", err)
	}
}

// IsExternal returns true if the named service is an external (unmanaged) service.
func (d *Daemon) IsExternal(name string) bool {
	d.mu.RLock()
	ms, ok := d.services[name]
	d.mu.RUnlock()
	return ok && ms.IsExternal()
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

	err := ms.Stop(timeout)
	d.regenerateRouting()
	return err
}

// RestartService stops and restarts a service.
// It uses the daemon's lifecycle context (not the caller's) so the new
// service outlives short-lived request contexts.
func (d *Daemon) RestartService(name string, timeout time.Duration) error {
	if err := d.StopService(name, timeout); err != nil {
		return err
	}

	// Reset restart counter so the service gets a fresh set of attempts
	d.mu.RLock()
	ms, ok := d.services[name]
	d.mu.RUnlock()
	if ok {
		ms.mu.Lock()
		ms.restartCount = 0
		ms.mu.Unlock()
	}

	return d.StartService(d.ctx, name)
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

// ServiceLogs returns the last n log lines for a service.
func (d *Daemon) ServiceLogs(name string, n int) ([]string, error) {
	d.mu.RLock()
	ms, ok := d.services[name]
	d.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("service %q not found", name)
	}

	return ms.Logs(n), nil
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
// It uses the daemon's lifecycle context for starting services so they outlive
// short-lived request contexts.
func (d *Daemon) Reload(_ context.Context) (*ReloadResult, error) {
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
			ms.Stop(DefaultStopTimeout)
			d.ports.Release(name)
			delete(d.services, name)
			result.Removed = append(result.Removed, name)
		}
	}

	// Start new services
	for name, s := range newSpecs {
		if _, exists := d.services[name]; !exists {
			d.logger.Info("adding service", "service", name)
			if err := d.startServiceLocked(d.ctx, s); err != nil {
				d.logger.Error("failed to start new service", "service", name, "error", err)
			} else {
				result.Added = append(result.Added, name)
			}
		}
	}

	// Restart changed services (spec content differs)
	for name, ms := range d.services {
		newSpec, exists := newSpecs[name]
		if !exists {
			continue // already removed above
		}
		newHash := newSpec.Hash()
		if ms.specHash == newHash {
			continue // unchanged
		}
		d.logger.Info("restarting changed service", "service", name)
		ms.Stop(DefaultStopTimeout)
		d.ports.Release(name)
		delete(d.services, name)
		if err := d.startServiceLocked(d.ctx, newSpec); err != nil {
			d.logger.Error("failed to restart changed service", "service", name, "error", err)
		} else {
			result.Restarted = append(result.Restarted, name)
		}
	}

	// Regenerate routing after reconciliation (write lock is held, use locked variant)
	d.regenerateRoutingLocked()

	return result, nil
}

// ReloadResult summarizes what changed during a reload.
type ReloadResult struct {
	Added     []string `json:"added,omitempty"`
	Removed   []string `json:"removed,omitempty"`
	Restarted []string `json:"restarted,omitempty"`
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

	name := s.Service.Name

	// External services skip port allocation and state persistence
	if s.Service.Type != "external" {
		// Allocate a dynamic port if the spec requests one
		if s.NeedsDynamicPort() {
			p, err := d.ports.Allocate(name)
			if err != nil {
				return fmt.Errorf("allocating port for %s: %w", name, err)
			}
			ms.allocatedPort = p
			d.logger.Info("allocated dynamic port", "service", name, "port", p)
		}

		ms.onStarted = func(pid int) {
			rec := ServiceRecord{
				Type:      s.Service.Type,
				PID:       pid,
				Port:      ms.allocatedPort,
				StartedAt: time.Now().Unix(),
				Command:   s.Service.Command,
			}
			if err := d.state.set(name, rec); err != nil {
				d.logger.Warn("failed to save service state", "service", name, "error", err)
			}
			d.regenerateRouting()
		}
	}

	if err := ms.Start(ctx); err != nil {
		return err
	}

	ms.specHash = s.Hash()
	d.services[s.Service.Name] = ms
	d.logger.Info("started service", "service", s.Service.Name, "type", s.Service.Type)
	return nil
}

// regenerateRouting collects routing info from all running services and
// writes a Traefik dynamic config file. No-op if routing is not configured.
// It acquires RLock internally and is safe to call without any lock held.
func (d *Daemon) regenerateRouting() {
	if d.routing == nil {
		return
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	d.regenerateRoutingLocked()
}

// regenerateRoutingLocked is the lock-free variant of regenerateRouting.
// It must only be called by a goroutine that already holds d.mu (read or write).
func (d *Daemon) regenerateRoutingLocked() {
	if d.routing == nil {
		return
	}

	var routes []routing.ServiceRoute
	for _, ms := range d.services {
		if ms.spec.Routing == nil {
			continue
		}
		// Only include running services
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

		routes = append(routes, routing.ServiceRoute{
			Name:       ms.spec.Service.Name,
			Hostname:   ms.spec.Routing.Hostname,
			Port:       port,
			TLS:        ms.spec.Routing.TLS,
			TLSOptions: ms.spec.Routing.TLSOptions,
		})
	}

	if err := d.routing.Generate(routes); err != nil {
		d.logger.Error("failed to regenerate routing config", "error", err)
	} else {
		d.logger.Info("regenerated routing config", "routes", len(routes), "path", d.routing.OutputPath())
	}
}

func (d *Daemon) adoptService(ctx context.Context, s *spec.ServiceSpec, drv driver.Driver) error {
	ms, err := NewManagedService(s, d.secrets)
	if err != nil {
		return err
	}

	name := s.Service.Name
	ms.adoptedDrv = drv

	// Restore dynamic port from allocator (reserved during state load)
	if s.NeedsDynamicPort() {
		if p := d.ports.Port(name); p != 0 {
			ms.allocatedPort = p
		}
	}

	ms.onStarted = func(pid int) {
		rec := ServiceRecord{
			Type:      s.Service.Type,
			PID:       pid,
			Port:      ms.allocatedPort,
			StartedAt: time.Now().Unix(),
			Command:   s.Service.Command,
		}
		if err := d.state.set(name, rec); err != nil {
			d.logger.Warn("failed to save service state", "service", name, "error", err)
		}
		d.regenerateRouting()
	}

	if err := ms.Start(ctx); err != nil {
		return err
	}

	ms.specHash = s.Hash()

	d.mu.Lock()
	d.services[s.Service.Name] = ms
	d.mu.Unlock()

	d.logger.Info("adopted service", "service", s.Service.Name, "pid", drv.Info().PID)
	return nil
}

// redeployAdopted replaces adopted processes with fully-managed ones to restore
// log capture and full supervision. Routed services get zero-downtime blue-green
// deploys; non-routed services fall back to restart (brief downtime).
func (d *Daemon) redeployAdopted() {
	if len(d.adopted) == 0 {
		return
	}
	d.logger.Info("redeploying adopted services", "count", len(d.adopted))

	// Wait for health checks to converge before redeploying
	wait := d.redeployWait
	if wait == 0 {
		wait = 10 * time.Second
	}
	select {
	case <-time.After(wait):
	case <-d.ctx.Done():
		return
	}

	for _, name := range d.adopted {
		// Check context — daemon may be shutting down
		if d.ctx.Err() != nil {
			return
		}
		d.logger.Info("redeploying adopted service", "service", name)
		if err := d.DeployService(name, DefaultStopTimeout); err != nil {
			d.logger.Error("failed to redeploy adopted service", "service", name, "error", err)
		} else {
			d.logger.Info("adopted service redeployed", "service", name)
		}
	}
	d.adopted = nil
}
