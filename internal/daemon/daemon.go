package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/benaskins/aurelia/internal/driver"
	"github.com/benaskins/aurelia/internal/health"
	"github.com/benaskins/aurelia/internal/keychain"
	"github.com/benaskins/aurelia/internal/node"
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
	specDir            string
	stateDir           string
	specSource         string // optional: source spec directory for drift detection
	secrets            keychain.Store
	routing            *routing.TraefikGenerator
	ports              *port.Allocator
	services           map[string]*ManagedService
	deps               *depGraph
	state              *stateFile
	mu                 sync.RWMutex
	logger             *slog.Logger
	ctx                context.Context         // daemon lifecycle context, set in Start()
	adopted            []string                // services adopted during crash recovery, pending redeploy
	redeployWait       time.Duration           // delay before redeploying adopted services (default 10s)
	peers              map[string]*node.Client // remote daemon peers
	peerStatus         map[string]bool         // peer name -> reachable
	certRenewal        *CertRenewal            // automatic node cert renewal (nil = disabled)
	serviceCertRenewal *ServiceCertRenewal     // automatic service cert renewal (nil = disabled)
}

// NewDaemon creates a new daemon that manages services from the given spec directory.
// The secrets store is optional — if nil, secret injection is disabled.
func NewDaemon(specDir string, opts ...Option) *Daemon {
	d := &Daemon{
		specDir:    specDir,
		stateDir:   specDir, // default: same as spec dir
		ports:      port.NewAllocator(defaultPortMin, defaultPortMax),
		services:   make(map[string]*ManagedService),
		peers:      make(map[string]*node.Client),
		peerStatus: make(map[string]bool),
		logger:     slog.With("component", "daemon"),
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

// WithSpecSource sets the source spec directory for drift detection.
// When set, the daemon logs a warning at startup if deployed specs
// differ from source specs.
func WithSpecSource(dir string) Option {
	return func(d *Daemon) {
		d.specSource = dir
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

	// Check for stale specs if a source directory is configured
	if d.specSource != "" {
		drifted, err := spec.DetectDrift(d.specDir, d.specSource)
		if err != nil {
			d.logger.Warn("spec drift check failed", "error", err)
		} else if len(drifted) > 0 {
			for _, dr := range drifted {
				if dr.Changed {
					d.logger.Warn("deployed spec differs from source", "spec", dr.Name)
				} else if !dr.DeployedIn {
					d.logger.Warn("source spec not deployed", "spec", dr.Name)
				}
			}
			d.logger.Warn("service specs out of sync with source",
				"stale_count", len(drifted),
				"source", d.specSource,
				"hint", "run 'just aurelia-sync' to update")
		}
	}

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
			// Verify the PID still belongs to the expected process (guard against PID reuse).
			// Try the spec command first, then the observed process name (handles
			// exec-replaced scripts where the binary name differs from the command).
			verified := driver.VerifyProcess(rec.PID, rec.Command, rec.StartTime)
			if !verified && rec.ProcessName != "" {
				verified = driver.VerifyProcess(rec.PID, rec.ProcessName, rec.StartTime)
			}
			if !verified {
				// AURELIA_SERVICE env tag survives exec and reparenting — definitive proof.
				if tag := driver.AureliaServiceTag(rec.PID); tag == name {
					verified = true
				}
			}
			if !verified {
				d.logger.Warn("PID reuse detected, searching for orphaned process",
					"service", name, "pid", rec.PID,
					"expected_command", rec.Command, "process_name", rec.ProcessName)

				// Search for the actual orphaned process by command pattern and
				// observed process name (handles exec-replaced scripts).
				if orphanPID := driver.FindProcessByCommand(rec.Command, rec.PID, rec.ProcessName); orphanPID > 0 {
					d.logger.Info("found orphaned process by command match",
						"service", name, "orphan_pid", orphanPID, "command", rec.Command)
					adopted, err := driver.NewAdopted(orphanPID)
					if err == nil {
						if err := d.adoptService(ctx, s, adopted); err != nil {
							d.logger.Error("failed to adopt orphaned process", "service", name, "error", err)
						} else {
							d.adopted = append(d.adopted, name)
							continue
						}
					} else {
						d.logger.Warn("orphaned process disappeared before adoption",
							"service", name, "orphan_pid", orphanPID)
					}
				} else {
					// Command-based search failed — try port-based detection.
					// This catches exec-replaced processes where the stored
					// process name doesn't match the running binary.
					adopted := false
					if rec.Port > 0 {
						if portPID := driver.FindPIDOnPort(rec.Port); portPID > 0 && portPID != rec.PID {
							d.logger.Info("found orphaned process by port match",
								"service", name, "orphan_pid", portPID, "port", rec.Port)
							drv, err := driver.NewAdopted(portPID)
							if err == nil {
								if err := d.adoptService(ctx, s, drv); err != nil {
									d.logger.Error("failed to adopt port-matched process", "service", name, "error", err)
								} else {
									d.adopted = append(d.adopted, name)
									adopted = true
								}
							}
						}
					}
					if adopted {
						continue
					}
					d.logger.Info("no orphaned process found, will start fresh",
						"service", name, "stale_pid", rec.PID)
				}
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
			// Check if the failure is due to an orphaned process holding a port
			var knownProcessName string
			if rec, ok := prevState[name]; ok {
				knownProcessName = rec.ProcessName
			}
			if d.recoverOrphanedPort(ctx, s, knownProcessName, err) {
				continue
			}
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

	// Start peer liveness checking
	d.startPeerLiveness(ctx)

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

// Shutdown exits gracefully without killing native processes, preserving the
// state file so the next daemon instance can adopt them. Container services
// are stopped (Docker manages their lifecycle independently). This is used
// for SIGTERM / launchctl stop to enable zero-downtime restarts.
func (d *Daemon) Shutdown(timeout time.Duration) {
	d.mu.RLock()
	g := d.deps
	d.mu.RUnlock()

	var order []string
	if g != nil {
		var err error
		order, err = g.stopOrder()
		if err != nil {
			d.logger.Warn("stop order failed for shutdown, using unordered", "error", err)
		}
	}

	// If no ordered list, collect all service names
	if order == nil {
		d.mu.RLock()
		order = make([]string, 0, len(d.services))
		for name := range d.services {
			order = append(order, name)
		}
		d.mu.RUnlock()
	}

	for _, name := range order {
		d.mu.RLock()
		ms, ok := d.services[name]
		d.mu.RUnlock()
		if !ok {
			continue
		}

		switch ms.spec.Service.Type {
		case "container":
			// Stop container services — Docker manages their restart independently
			d.logger.Info("stopping container service for shutdown", "service", name)
			if err := ms.Stop(timeout); err != nil {
				d.logger.Error("error stopping container service", "service", name, "error", err)
			}
		case "native":
			// Release native services — leave processes running for adoption
			d.logger.Info("releasing native service for shutdown", "service", name)
			if err := ms.Release(timeout); err != nil {
				d.logger.Error("error releasing native service", "service", name, "error", err)
			}
		default:
			// External services — just release supervision
			d.logger.Info("releasing external service for shutdown", "service", name)
			if err := ms.Release(timeout); err != nil {
				d.logger.Error("error releasing external service", "service", name, "error", err)
			}
		}
	}

	d.logger.Info("shutdown complete, state file preserved for adoption")
}

// getService returns the managed service with the given name, or an error if not found.
func (d *Daemon) getService(name string) (*ManagedService, error) {
	d.mu.RLock()
	ms, ok := d.services[name]
	d.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("service %q not found", name)
	}
	return ms, nil
}

// SetSecrets sets the secret store after the daemon has started.
// This allows the daemon to start services (like OpenBao) before the
// secrets backend is available, then inject secrets for later use.
func (d *Daemon) SetSecrets(s keychain.Store) {
	d.mu.Lock()
	d.secrets = s
	d.mu.Unlock()
}

// IsExternal returns true if the named service is an external (unmanaged) service.
func (d *Daemon) IsExternal(name string) bool {
	ms, err := d.getService(name)
	return err == nil && ms.IsExternal()
}

// StartService starts a single service by name.
func (d *Daemon) StartService(ctx context.Context, name string) error {
	ms, err := d.getService(name)
	if err != nil {
		return err
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

// RemoveService stops a service, archives its spec file, and removes it from the daemon.
func (d *Daemon) RemoveService(name string, timeout time.Duration) error {
	// Stop the service first (includes cascade logic)
	if err := d.StopService(name, timeout); err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	// Archive the spec file
	specFile := filepath.Join(d.specDir, name+".yaml")
	if _, err := os.Stat(specFile); os.IsNotExist(err) {
		// Try .yml extension
		specFile = filepath.Join(d.specDir, name+".yml")
	}

	if _, err := os.Stat(specFile); err == nil {
		archiveDir := filepath.Join(d.specDir, "archive")
		if err := os.MkdirAll(archiveDir, 0755); err != nil {
			return fmt.Errorf("creating archive directory: %w", err)
		}
		archivePath := filepath.Join(archiveDir, filepath.Base(specFile))
		if err := os.Rename(specFile, archivePath); err != nil {
			return fmt.Errorf("archiving spec file: %w", err)
		}
		d.logger.Info("archived spec file", "service", name, "path", archivePath)
	}

	// Remove from in-memory state
	d.ports.Release(name)
	delete(d.services, name)
	if d.deps != nil {
		d.deps.remove(name)
	}

	// Update state file
	if err := d.state.remove(name); err != nil {
		d.logger.Warn("failed to remove service from state file", "service", name, "error", err)
	}

	d.regenerateRoutingLocked(nil)
	d.logger.Info("removed service", "service", name)
	return nil
}

// RestartService stops and restarts a service.
// It uses the daemon's lifecycle context (not the caller's) so the new
// service outlives short-lived request contexts.
// After the target restarts, any cascade-stopped dependents are also restarted.
func (d *Daemon) RestartService(name string, timeout time.Duration) error {
	// Collect cascade targets before stopping — these will need restarting.
	var cascadeTargets []string
	d.mu.RLock()
	g := d.deps
	d.mu.RUnlock()
	if g != nil {
		cascadeTargets = g.cascadeStopTargets(name)
	}

	// Capture the OS-observed process name before stopping so we can match
	// exec-replaced processes (whose running name differs from the spec command).
	// Reading from the live driver while the process is still running is more
	// reliable than reading from the state file after stop.
	d.mu.RLock()
	ms, ok := d.services[name]
	d.mu.RUnlock()
	var knownProcessName string
	if ok {
		ms.mu.Lock()
		// drv is nil if the service has never started successfully.
		if ms.drv != nil {
			if pid := ms.drv.Info().PID; pid > 0 {
				knownProcessName, _ = driver.ProcessName(pid)
			}
		}
		ms.mu.Unlock()
	}

	if err := d.StopService(name, timeout); err != nil {
		return err
	}

	// Reset restart counter so the service gets a fresh set of attempts
	d.mu.RLock()
	ms, ok = d.services[name]
	d.mu.RUnlock()
	if ok {
		ms.mu.Lock()
		ms.restartCount = 0
		ms.mu.Unlock()
	}

	// Proactively kill any orphaned OS process still holding the service port.
	// The previously-supervised process may have survived SIGTERM (e.g. adopted
	// process from crash recovery); if it is still on the port the new start will
	// fail asynchronously with no recovery path.
	// ms.spec is safe to read without holding d.mu — it is set once at
	// construction and never reassigned.
	if ok {
		d.killOrphanOnPort(ms.spec, knownProcessName)
	}

	if err := d.StartService(d.ctx, name); err != nil {
		return err
	}

	// Restart cascade-stopped dependents
	for _, dep := range cascadeTargets {
		d.mu.RLock()
		depMs, exists := d.services[dep]
		d.mu.RUnlock()
		if !exists {
			continue
		}
		// Only restart if the service was actually stopped by the cascade
		state := depMs.State()
		if state.State == "stopped" || state.State == "failed" {
			d.logger.Info("cascade restarting dependent", "service", dep, "because", name)
			depMs.mu.Lock()
			depMs.restartCount = 0
			depMs.mu.Unlock()
			if err := d.StartService(d.ctx, dep); err != nil {
				d.logger.Error("error cascade restarting", "service", dep, "error", err)
			}
		}
	}

	return nil
}

// killOrphanOnPort kills any OS process holding s's port before a restart.
// Called from RestartService between StopService and StartService to prevent
// "address already in use" when the previously-supervised process survived.
// knownProcessName is the OS-reported name captured from the live driver before
// stop; it is used as a second match tier to handle exec-replaced processes whose
// running name differs from the spec command (mirrors recoverOrphanedPort).
// Errors are logged but not returned — the caller proceeds regardless.
func (d *Daemon) killOrphanOnPort(s *spec.ServiceSpec, knownProcessName string) {
	port := 0
	if s.Network != nil {
		port = s.Network.Port
	}
	if port == 0 && s.NeedsDynamicPort() {
		port = d.ports.Port(s.Service.Name)
	}
	if port <= 0 {
		return
	}

	holderPID := driver.FindPIDOnPort(port)
	if holderPID <= 0 {
		return
	}

	name := s.Service.Name

	// Guard: never kill the daemon's own process.
	if holderPID == os.Getpid() {
		d.logger.Error("port held by aurelia daemon itself, not killing",
			"service", name, "port", port)
		return
	}

	commandMatch := s.Service.Command != "" && driver.VerifyProcess(holderPID, s.Service.Command, 0)
	nameMatch := knownProcessName != "" && driver.VerifyProcess(holderPID, knownProcessName, 0)
	if (s.Service.Command != "" || knownProcessName != "") && !commandMatch && !nameMatch {
		holderName, _ := driver.ProcessName(holderPID)
		d.logger.Warn("port held by unrelated process during restart, skipping kill",
			"service", name, "port", port, "holder_pid", holderPID, "holder_name", holderName)
		return
	}

	// Kill to free the port for the incoming restart. Unlike recoverOrphanedPort
	// (which adopts a matching orphan when found during startup), here we have an
	// explicit restart in progress — adopting the old process would silently
	// preserve the old instance instead of starting the fresh one the caller
	// requested.
	holderName, _ := driver.ProcessName(holderPID)
	d.logger.Warn("orphaned process holding port before restart, killing",
		"service", name, "port", port, "orphan_pid", holderPID, "holder_name", holderName)

	orphan, err := driver.NewAdopted(holderPID)
	if err != nil {
		// Process disappeared between FindPIDOnPort and now — port is free.
		d.logger.Info("orphan disappeared before kill", "service", name, "orphan_pid", holderPID)
		return
	}

	killCtx, cancel := context.WithTimeout(d.ctx, 10*time.Second)
	defer cancel()
	if err := orphan.Stop(killCtx, 10*time.Second); err != nil {
		d.logger.Error("failed to kill orphan during restart",
			"service", name, "orphan_pid", holderPID, "error", err)
	} else {
		d.logger.Info("killed orphan before restart", "service", name, "killed_pid", holderPID)
	}
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
	ms, err := d.getService(name)
	if err != nil {
		return nil, err
	}
	return ms.Logs(n), nil
}

// ServiceLogsSince returns lines written after gen, plus the new generation counter.
func (d *Daemon) ServiceLogsSince(name string, gen int) ([]string, int, error) {
	ms, err := d.getService(name)
	if err != nil {
		return nil, 0, err
	}
	lines, newGen := ms.LogsSince(gen)
	return lines, newGen, nil
}

// ServiceState returns the state of a single service.
func (d *Daemon) ServiceState(name string) (ServiceState, error) {
	ms, err := d.getService(name)
	if err != nil {
		return ServiceState{}, err
	}
	return ms.State(), nil
}

// InspectService returns the full resolved config and runtime state of a service.
func (d *Daemon) InspectService(name string) (ServiceInspect, error) {
	ms, err := d.getService(name)
	if err != nil {
		return ServiceInspect{}, err
	}
	return ms.Inspect(), nil
}

// ServiceDeps returns dependency information for a service.
type ServiceDeps struct {
	After         []string `json:"after"`
	Requires      []string `json:"requires"`
	Dependents    []string `json:"dependents"`
	CascadeImpact []string `json:"cascade_impact"`
}

// ServiceDeps returns the dependency graph for a named service.
func (d *Daemon) ServiceDeps(name string) (ServiceDeps, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if _, ok := d.services[name]; !ok {
		return ServiceDeps{}, fmt.Errorf("service %q not found", name)
	}

	result := ServiceDeps{}
	if d.deps != nil {
		result.After = d.deps.after[name]
		result.Requires = d.deps.requires[name]
		result.Dependents = d.deps.dependents[name]
		result.CascadeImpact = d.deps.cascadeStopTargets(name)
	}
	// Ensure non-nil slices for clean JSON
	if result.After == nil {
		result.After = []string{}
	}
	if result.Requires == nil {
		result.Requires = []string{}
	}
	if result.Dependents == nil {
		result.Dependents = []string{}
	}
	if result.CascadeImpact == nil {
		result.CascadeImpact = []string{}
	}
	return result, nil
}

// GraphNode represents a service in the full dependency graph.
type GraphNode struct {
	Name         string        `json:"name"`
	Type         string        `json:"type"`
	State        driver.State  `json:"state"`
	Health       health.Status `json:"health"`
	Port         int           `json:"port,omitempty"`
	Uptime       string        `json:"uptime,omitempty"`
	RestartCount int           `json:"restart_count"`
	After        []string      `json:"after"`
	Requires     []string      `json:"requires"`
	Node         string        `json:"node,omitempty"`
}

// ServiceGraph returns all services with their state and dependency edges.
func (d *Daemon) ServiceGraph() []GraphNode {
	d.mu.RLock()
	defer d.mu.RUnlock()

	nodes := make([]GraphNode, 0, len(d.services))
	for _, ms := range d.services {
		st := ms.State()
		node := GraphNode{
			Name:         st.Name,
			Type:         st.Type,
			State:        st.State,
			Health:       st.Health,
			Port:         st.Port,
			Uptime:       st.Uptime,
			RestartCount: st.RestartCount,
			After:        []string{},
			Requires:     []string{},
		}
		if d.deps != nil {
			if after := d.deps.after[st.Name]; after != nil {
				node.After = after
			}
			if requires := d.deps.requires[st.Name]; requires != nil {
				node.Requires = requires
			}
		}
		nodes = append(nodes, node)
	}
	return nodes
}

// ServiceHealthHistory returns the recent health check records for a service.
func (d *Daemon) ServiceHealthHistory(name string) ([]health.CheckRecord, error) {
	ms, err := d.getService(name)
	if err != nil {
		return nil, err
	}
	return ms.HealthHistory(), nil
}

// CheckSpecDrift compares deployed specs against the source directory.
// Returns nil results if no source directory is configured or directories are in sync.
func (d *Daemon) CheckSpecDrift() ([]spec.DriftResult, error) {
	if d.specSource == "" {
		return nil, nil
	}
	return spec.DetectDrift(d.specDir, d.specSource)
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
	d.regenerateRoutingLocked(nil)

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
			rec := newServiceRecord(s.Service.Type, pid, ms.allocatedPort, s.Service.Command)
			if st, err := driver.ProcessStartTime(pid); err == nil {
				rec.StartTime = st
			}
			rec.ProcessName = resolveProcessName(pid)
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

	d.regenerateRoutingLocked(nil)
}

// regenerateRoutingLocked is the lock-free variant of regenerateRouting.
// It must only be called by a goroutine that already holds d.mu (read or write).
// portOverrides optionally maps service names to port overrides (e.g. during deploy).
func (d *Daemon) regenerateRoutingLocked(portOverrides map[string]int) {
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

		if override, ok := portOverrides[ms.spec.Service.Name]; ok {
			port = override
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
		rec := newServiceRecord(s.Service.Type, pid, ms.allocatedPort, s.Service.Command)
		if st, err := driver.ProcessStartTime(pid); err == nil {
			rec.StartTime = st
		}
		rec.ProcessName = resolveProcessName(pid)
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

// recoverOrphanedPort checks if a service start failure is due to an orphaned
// process holding the service's port. If so, it kills the orphan and retries
// the start. The knownProcessName is the OS-reported process name from a previous
// run (may be empty). Returns true if recovery succeeded and the service is now running.
func (d *Daemon) recoverOrphanedPort(ctx context.Context, s *spec.ServiceSpec, knownProcessName string, startErr error) bool {
	if startErr == nil {
		return false
	}

	// Determine the port the service needs
	port := 0
	if s.Network != nil {
		port = s.Network.Port
	}
	// For dynamic ports, check the allocator
	if port == 0 && s.NeedsDynamicPort() {
		port = d.ports.Port(s.Service.Name)
	}
	if port <= 0 {
		return false
	}

	// Check if something is holding the port
	holderPID := driver.FindPIDOnPort(port)
	if holderPID <= 0 {
		return false
	}

	// Check if the port holder looks like our orphaned service. Try the spec
	// command first, then the observed process name from the previous run.
	name := s.Service.Name
	commandMatch := s.Service.Command != "" && driver.VerifyProcess(holderPID, s.Service.Command, 0)
	nameMatch := knownProcessName != "" && driver.VerifyProcess(holderPID, knownProcessName, 0)

	if commandMatch || nameMatch {
		// The port holder matches — adopt it rather than killing and restarting.
		adopted, err := driver.NewAdopted(holderPID)
		if err != nil {
			d.logger.Warn("orphaned process disappeared before adoption",
				"service", name, "orphan_pid", holderPID)
		} else {
			d.logger.Info("adopting orphaned process holding port",
				"service", name, "port", port, "orphan_pid", holderPID)
			if err := d.adoptService(ctx, s, adopted); err != nil {
				d.logger.Error("failed to adopt orphaned process", "service", name, "error", err)
			} else {
				d.adopted = append(d.adopted, name)
				return true
			}
		}
	}

	// If the port holder is on our configured port but we can't verify by name,
	// it's still likely our orphan (exec-replaced scripts produce name mismatches).
	// Kill it and retry rather than leaving the service permanently broken.
	// Guard: never kill our own process.
	holderName, _ := driver.ProcessName(holderPID)
	if holderPID == os.Getpid() {
		d.logger.Error("port held by aurelia daemon itself, not killing",
			"service", name, "port", port)
		return false
	}
	d.logger.Warn("killing unverified process holding port",
		"service", name, "port", port, "holder_pid", holderPID, "holder_name", holderName)

	orphan, err := driver.NewAdopted(holderPID)
	if err != nil {
		d.logger.Error("process disappeared before kill",
			"service", name, "holder_pid", holderPID)
		return false
	}

	if err := orphan.Stop(ctx, 10*time.Second); err != nil {
		d.logger.Error("failed to kill process holding port",
			"service", name, "holder_pid", holderPID, "error", err)
		return false
	}

	d.logger.Info("killed process holding port, retrying service start",
		"service", name, "killed_pid", holderPID, "port", port)

	// Retry the start
	if err := d.startService(ctx, s); err != nil {
		d.logger.Error("failed to start service after clearing port",
			"service", name, "error", err)
		return false
	}

	return true
}

// resolveProcessName polls briefly to capture the post-exec process name.
// Shell scripts that use exec to replace themselves will initially report the
// shell name; after a few milliseconds the kernel reports the replacement binary.
// Returns the final observed name, or the initial name if it doesn't change.
func resolveProcessName(pid int) string {
	initial, err := driver.ProcessName(pid)
	if err != nil {
		return ""
	}

	// If it's not a shell, exec replacement is unlikely — return immediately.
	switch initial {
	case "bash", "sh", "zsh", "dash", "fish":
	default:
		return initial
	}

	// Poll briefly for the exec to complete.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		name, err := driver.ProcessName(pid)
		if err != nil {
			return initial
		}
		if name != initial {
			return name
		}
	}
	return initial
}
