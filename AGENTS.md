# AGENTS.md

Project context for AI coding agents working in this repository.

## Build & Test Commands

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
3. **Daemon** (`internal/daemon`) — orchestrates `ManagedService` instances, each running a supervision goroutine. Handles dependency graph (topological sort for startup/shutdown ordering, cascade-stop for hard deps), state persistence (`~/.aurelia/state.json`), and Traefik config generation
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
    Stdout() io.Reader
}

// Store — secret storage (internal/keychain)
type Store interface {
    Set(key, value string) error
    Get(key string) (string, error)
    List() ([]string, error)
    Delete(key string) error
    GetMultiple(keys []string) (map[string]string, error)
}
```

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
