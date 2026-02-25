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
just lint             # go vet
just fmt              # go fmt
```

## Architecture

Aurelia is a **macOS-native process supervisor** — a developer-focused alternative to supervisord/launchd. Services are defined as YAML specs, managed by a daemon, and controlled via a CLI that communicates over a Unix socket.

### Layers (bottom-up)

1. **Spec** (`internal/spec`) — YAML service definitions: process type, health checks, restart policy, dependencies, routing, secrets
2. **Driver** (`internal/driver`) — `Driver` interface with three implementations:
   - `NativeDriver` — fork/exec via `os/exec`
   - `ContainerDriver` — Docker via `docker/docker` client
   - `AdoptedDriver` — attaches to existing PID for crash recovery
3. **Daemon** (`internal/daemon`) — orchestrates `ManagedService` instances, each running a supervision goroutine. Handles dependency graph (topological sort for startup/shutdown ordering, cascade-stop for hard deps), state persistence (`~/.aurelia/state.json`), Traefik config generation, and zero-downtime blue-green deploys (routed services only — non-routed services fall back to restart)
4. **API** (`internal/api`) — REST over Unix socket (`~/.aurelia/aurelia.sock`), with optional TCP listener (`--api-addr`) protected by bearer token auth (`~/.aurelia/api.token`). Uses Go 1.22+ `http.ServeMux` pattern syntax
5. **CLI** (`cmd/aurelia`) — cobra commands; `daemon` runs in-process, all others are HTTP clients to the API

### Supporting packages

- `internal/health` — periodic health checking (http/tcp/exec), fires `onUnhealthy` callback to trigger restarts
- `internal/keychain` — `Store` interface with `KeychainStore` (macOS Keychain, darwin build tag) and `MemoryStore` (testing)
- `internal/gpu` — Apple Silicon GPU/VRAM/thermal observability via cgo (Metal/IOKit, darwin build tag)
- `internal/routing` — generates Traefik dynamic config YAML from running services with routing specs
- `internal/port` — dynamic port allocation in configurable range (default 20000–32000)
- `internal/logbuf` — thread-safe ring buffer for stdout/stderr capture
- `internal/audit` — append-only NDJSON audit log for secret operations
- `internal/config` — daemon config from `~/.aurelia/config.yaml`

### Key interfaces

```go
// Driver — core process lifecycle (internal/driver)
type Driver interface {
    Start(ctx context.Context) error
    Stop(ctx context.Context, timeout time.Duration) error
    Info() ProcessInfo
    Wait() (int, error)
    LogLines(n int) []string
}

// Store — secret storage (internal/keychain)
type Store interface {
    Set(key, value string) error
    Get(key string) (string, error)
    List() ([]string, error)
    Delete(key string) error
}
```

### Signal semantics

| Signal | Behavior | Use case |
|--------|----------|----------|
| SIGTERM | Orphan native children, preserve state, exit | `launchctl stop`, `just install` |
| SIGINT | Full teardown — kill all children, clear state | Interactive Ctrl-C |
| API stop | Full teardown — kill all children, clear state | `aurelia stop` CLI command |

Container services are always stopped on any shutdown. Native processes use `exec.Command` (not `CommandContext`) so their lifetime is not tied to the Go context.

### Runtime files

All under `~/.aurelia/`: `config.yaml` (daemon config), `services/*.yaml` (service specs), `state.json` (PID/port persistence), `aurelia.sock` (Unix socket IPC), `audit.log`, `secret-metadata.json`.

## Test Patterns

- Standard `testing` package, no external test framework
- Helpers use `t.Helper()` and `t.TempDir()`; specs written inline as YAML strings via `writeSpec(t, dir, name, content)`
- API tests spin up a real daemon + Unix socket in temp dir via `setupTestServer(t, specs)`
- Integration tests use `//go:build integration` tag and require Docker/OrbStack
- `MemoryStore` serves as the test double for Keychain

## Platform Constraints

- cgo required for GPU package (Metal/IOKit) — darwin-only build tags
- Keychain package has darwin build tag; uses `MemoryStore` elsewhere
- `Daemon` uses functional options pattern: `WithSecrets()`, `WithStateDir()`, `WithPortRange()`, `WithRouting()`

## Branching & PR Workflow

`main` is protected: force-push and deletion are blocked, linear history required. No PRs — commit directly to main.

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
aurelia skills aurelia-deploy     # Show full deployment workflow
aurelia skills aurelia-debug      # Show debugging workflow
```

For Claude Code discovery within this repo, skills are also symlinked to `.claude/skills/` via `just install-skills`.

| Skill | Purpose |
|---|---|
| aurelia-deploy | Add a new service to the mesh or ship changes to an existing one |
| aurelia-debug | Diagnose problems with aurelia-managed services |

Generic skills (brainstorming, debugging, tdd, verify) are installed globally from `~/dev/skills`.
