---
module: github.com/benaskins/aurelia
kind: supervisor
---

# AGENTS.md

Project context for AI coding agents working in this repository.

## Build & Test Commands

First-time setup: run `script/bootstrap` to install prerequisites (Go, just) via Homebrew.

```bash
just build            # Build binary locally
just install          # Build, install to ~/.local/bin, restart daemon
just test             # Unit tests
just test-all         # All tests including slow ones
just test-integration # Integration tests (require Docker/OrbStack)
just lint             # gofmt -l check + go vet
just fmt-check        # gofmt drift check only
just fmt              # go fmt
```

## Architecture

Aurelia is a **process supervisor with macOS-specific enhancements**, a developer-focused alternative to supervisord/launchd. Services are defined as YAML specs, managed by a daemon, and controlled via a CLI that communicates over a Unix socket.

### Layers (bottom-up)

1. **Spec** (`internal/spec`): YAML service definitions covering process type, health checks, restart policy, dependencies, routing, secrets
2. **Driver** (`internal/driver`): `Driver` interface with four implementations:
   - `NativeDriver`: fork/exec via `os/exec`
   - `ContainerDriver`: Docker via `docker/docker` client
   - `AdoptedDriver`: attaches to existing PID for crash recovery
   - `RemoteDriver`: manages remote services via shell hook commands (start/stop/restart), no local PID
3. **Daemon** (`internal/daemon`): orchestrates `ManagedService` instances, each running a supervision goroutine. Handles dependency graph (topological sort for startup/shutdown ordering, cascade-stop for hard deps), state persistence (`~/.aurelia/state.json`), Traefik config generation, and zero-downtime blue-green deploys (routed services only; non-routed services fall back to restart)
4. **API** (`internal/api`): REST over Unix socket (`~/.aurelia/aurelia.sock`), with optional TCP listener (`--api-addr`) protected by bearer token auth (`~/.aurelia/api.token`) or mTLS client certs. Per-source rate limiting (20 req/s, 100 burst). Uses Go 1.22+ `http.ServeMux` pattern syntax. Token rotation with two-phase commit (old token stays valid until peers confirm)
5. **CLI** (`cmd/aurelia`): cobra commands; `daemon` runs in-process, all others are HTTP clients to the API. Includes `diagnose` (LLM-powered diagnostics TUI) and `token rotate` (cluster-wide token rotation)

### Supporting packages

- `internal/health`: periodic health checking (http/tcp/exec), fires `onUnhealthy` callback to trigger restarts
- `internal/keychain`: `Store` interface with four implementations:
  - `SystemStore` (macOS Keychain, darwin build tag)
  - `BaoStore` (OpenBao KV v1 with auto-unseal)
  - `AuditedStore` (wraps any Store with audit logging, metadata tracking, and secret rotation)
  - `MemoryStore` (testing)
- `internal/gpu`: Apple Silicon GPU/VRAM/thermal observability via cgo (Metal/IOKit, darwin build tag)
- `internal/routing`: generates Traefik dynamic config YAML from running services with routing specs
- `internal/port`: dynamic port allocation in configurable range (default 20000-32000)
- `internal/logbuf`: thread-safe ring buffer for stdout/stderr capture
- `internal/audit`: append-only NDJSON audit log for secret operations (read, write, delete, rotate)
- `internal/config`: daemon config from `~/.aurelia/config.yaml` (includes TLS, OpenBao, node peers, diagnose config)
- `internal/node`: HTTP client for remote aurelia daemons (bearer token or mTLS), used for peer communication
- `internal/diagnose`: LLM-powered diagnostic engine with Bubble Tea TUI, read-only and action tools against the aurelia API, operator confirmation for mutations
- `internal/multinode`: test infrastructure for multi-node clusters, providing ephemeral CA, node cert issuance, Docker-based cluster harness, timing collector
- `internal/sysinfo`: system resource metrics (CPU, memory, disk)

### Key interfaces

```go
// Driver: core process lifecycle (internal/driver)
type Driver interface {
    Start(ctx context.Context) error
    Stop(ctx context.Context, timeout time.Duration) error
    Info() ProcessInfo
    Wait() (int, error)
    LogLines(n int) []string
}

// Store: secret storage (internal/keychain)
// Implementations: SystemStore (Keychain), BaoStore (OpenBao), MemoryStore (test)
type Store interface {
    Set(key, value string) error
    Get(key string) (string, error)
    List() ([]string, error)
    Delete(key string) error
}

// APIClient: diagnose package's interface to the aurelia API (internal/diagnose)
type APIClient interface {
    Get(path string) (*http.Response, error)
    Post(path string) (*http.Response, error)
}

// ConfirmFunc: operator confirmation for action tools (internal/diagnose)
type ConfirmFunc func(action, service, reason string) bool
```

### Restart policies

| Policy | Behavior |
|--------|----------|
| `always` | Restart on any exit |
| `on-failure` | Restart on non-zero exit only |
| `never` | Do not restart |
| `oneshot` | Run command once; on exit 0, enter health-monitoring phase (no process, health checker runs); on health failure, re-execute command. Requires a `health` block |

### Signal semantics

| Signal | Behavior | Use case |
|--------|----------|----------|
| SIGTERM | Orphan native children, preserve state, exit | `launchctl stop`, `just install` |
| SIGINT | Full teardown (kill all children, clear state) | Interactive Ctrl-C |
| API stop | Full teardown (kill all children, clear state) | `aurelia stop` CLI command |

Container services are always stopped on any shutdown. Native processes use `exec.Command` (not `CommandContext`) so their lifetime is not tied to the Go context.

### Runtime files

All under `~/.aurelia/`: `config.yaml` (daemon config for TLS, OpenBao, nodes, diagnose), `services/*.yaml` (service specs), `state.json` (PID/port persistence), `aurelia.sock` (Unix socket IPC), `api.token` (bearer token for TCP API), `audit.log`, `secret-metadata.json`, `logs/diagnose/*.log` (diagnostic session logs).

## Test Patterns

- Standard `testing` package, no external test framework
- Helpers use `t.Helper()` and `t.TempDir()`; specs written inline as YAML strings via `writeSpec(t, dir, name, content)`
- API tests spin up a real daemon + Unix socket in temp dir via `setupTestServer(t, specs)`
- Integration tests use `//go:build integration` tag and require Docker/OrbStack
- Multi-node integration tests use `internal/multinode.Cluster`, which spins up aurelia daemons in Docker containers with mTLS, ephemeral CA, and peer discovery
- `MemoryStore` serves as the test double for Keychain

## Platform Constraints

- cgo required for GPU package (Metal/IOKit); darwin-only build tags
- Keychain package has darwin build tag; uses `MemoryStore` elsewhere
- `Daemon` uses functional options pattern: `WithSecrets()`, `WithStateDir()`, `WithPortRange()`, `WithRouting()`, `WithPeers()`
- Secrets backend selection: OpenBao preferred when configured and reachable, falls back to macOS Keychain (`cmd/aurelia/secrets_backend.go`)
- Multi-node: `daemon.BuildPeers()` creates `node.Client` instances from config, excluding the local node; peer liveness goroutine pings every 10s

## Branching & PR Workflow

`main` is protected: force-push and deletion are blocked, linear history required. No PRs; commit directly to main.

**CI model:** Local-first. Run `just test-all && just lint` before pushing. GitHub Actions CI runs on every push to main (informational, not gating). CI gates PRs from forks.

**Day-to-day workflow (direct to main):**
```bash
# ... make changes, commit ...
just test-all && just lint        # Verify locally
git push origin main              # CI must pass
```

**Feature branches** (optional, for larger work or parallel agents):
```
feat/<description>       # New features
fix/<description>        # Bug fixes
refactor/<description>   # Code restructuring
docs/<description>       # Documentation
test/<description>       # Test additions/changes
infra/<description>      # Infrastructure changes
config/<description>     # Configuration changes
```

**Parallel agent work:** Each agent MUST use its own git worktree (`git worktree add`) to avoid conflicts. Never have parallel agents writing to the same working tree.

## Commit Conventions

Conventional commits: `feat:`, `fix:`, `refactor:`, `docs:`, `test:`, `infra:`, `config:`

## Skills

Skills are embedded in the binary and available from any repo via `aurelia skills`:

```bash
aurelia skills                    # List available skills with descriptions
aurelia skills deploy-aurelia     # Show deployment workflow
aurelia skills debug-aurelia      # Show debugging workflow
aurelia skills ground-aurelia     # Show orientation workflow
```

For Claude Code discovery within this repo, skills are also symlinked to `.claude/skills/` via `just install-skills`.

| Skill | Extends | Purpose |
|---|---|---|
| ground-aurelia | `/ground` | Orient in the aurelia service mesh |
| debug-aurelia | `/debug` | Diagnose problems with aurelia-managed services |
| deploy-aurelia | `/deploy` | Add a new service or ship changes to an existing one |

Generic skills (`/ground`, `/brainstorm`, `/iterate`, `/debug`, `/verify`, `/deploy`) are installed globally from [humanpowers](https://github.com/benaskins/humanpowers).
