# AGENTS.md

Project context for AI coding agents working in this repository.

## Build & Test Commands

```bash
# Build
go build -o aurelia ./cmd/aurelia/

# Test
go test ./...                                          # all unit tests
go test ./internal/daemon/                             # single package
go test ./internal/daemon/ -run TestDaemonStartStop    # single test
go test -v ./...                                       # verbose output
go test -tags integration ./...                        # integration tests (require Docker/OrbStack)

# Format & vet
go fmt ./...
go vet ./...
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

The `skills/` directory contains reusable workflow documents (markdown with YAML frontmatter) for common tasks. Each skill lives in its own subdirectory with a `SKILL.md` file:

- **`skills/deploy-to-mesh/`** — Add a new service to the Aurelia service mesh with Traefik routing and TLS
