# Aurelia

Aurelia is a macOS-native process supervisor — a developer-focused alternative to supervisord and launchd. Services are defined as YAML specs, managed by a persistent daemon, and controlled via a CLI that communicates over a Unix socket.

## Features

- **Native processes and Docker containers** — run binaries or container images under a single supervisor
- **Health checks** — HTTP, TCP, and exec probes with configurable intervals, timeouts, and thresholds
- **Dependency management** — ordered startup and shutdown via topological sort; cascade-stop for hard dependencies
- **Crash recovery** — automatic restart with fixed or exponential backoff; re-adopts processes across daemon restarts
- **Dynamic port allocation** — assign ports at runtime from a configurable range (default 20000–32000)
- **Traefik routing** — generates dynamic config YAML from service routing specs
- **macOS Keychain secrets** — store and inject secrets via the system Keychain with an audit log and rotation policy
- **GPU observability** — Apple Silicon VRAM usage and thermal state via Metal/IOKit

## Requirements

- macOS (required for Keychain and GPU features)
- Go 1.22+ with cgo enabled
- Docker or OrbStack (required only for container services)

## Installation

Build from source:

```bash
git clone https://github.com/benaskins/aurelia
cd aurelia
go build -o aurelia ./cmd/aurelia/
```

To build with the version string injected (recommended), use:

```bash
make build
```

`make build` passes the version via ldflags so that `aurelia --version` reports the correct release.

To start the daemon automatically on login, install it as a LaunchAgent:

```bash
aurelia install
```

To remove it:

```bash
aurelia uninstall
```

## Quick Start

1. Create a spec file in `~/.aurelia/services/`:

```yaml
# ~/.aurelia/services/api.yaml
service:
  name: api
  type: native
  command: ./bin/api
  working_dir: /home/user/myproject

network:
  port: 8080

health:
  type: http
  path: /healthz
  port: 8080
  interval: 10s
  timeout: 2s

restart:
  policy: on-failure
  max_attempts: 5
  delay: 1s
  backoff: exponential
```

2. Start the daemon:

```bash
aurelia daemon
```

3. Manage services:

```bash
aurelia status          # show all services
aurelia up              # start all services
aurelia up api          # start a specific service
aurelia down api        # stop a specific service
aurelia restart api     # restart a specific service
aurelia reload          # re-read specs and reconcile (add new, remove deleted)
```

## CLI Reference

| Command | Description |
|---|---|
| `aurelia daemon` | Run the supervisor daemon (loads specs and manages lifecycle) |
| `aurelia status` | Show service name, type, state, health, PID, uptime, restart count |
| `aurelia up [service...]` | Start one or more services (all if no args) |
| `aurelia down [service...]` | Stop one or more services (all if no args) |
| `aurelia restart <service>` | Restart a service |
| `aurelia reload` | Re-read spec files and reconcile running services |
| `aurelia check [file-or-dir]` | Validate spec files without running them |
| `aurelia gpu` | Show Apple Silicon GPU/VRAM/thermal state |
| `aurelia install` | Install as a LaunchAgent (auto-start on login) |
| `aurelia uninstall` | Remove the LaunchAgent |
| `aurelia secret set <key> [value]` | Store a secret in macOS Keychain |
| `aurelia secret get <key>` | Retrieve a secret |
| `aurelia secret list` | List secrets with age and rotation status |
| `aurelia secret delete <key>` | Remove a secret |
| `aurelia secret rotate <key> -c <cmd>` | Rotate a secret using a shell command |
| `aurelia --version` | Show version information |

### Daemon flags

```
--api-addr string        Optional TCP address for the API (e.g. 127.0.0.1:9090)
--routing-output string  Path to write Traefik dynamic config (enables routing)
```

These can also be set in `~/.aurelia/config.yaml` as `api_addr` and `routing_output`.

## Service Spec Format

Specs are YAML files placed in `~/.aurelia/services/`. Each file defines one service.

```yaml
# Required: service identity
service:
  name: myapp              # unique service name
  type: native             # "native" or "container"

  # native only
  command: ./bin/myapp
  working_dir: /path/to/project

  # container only (alternative to command/working_dir)
  # image: myimage:latest
  # network_mode: host      # default "host"

# Static or dynamic port binding
network:
  port: 8080               # 0 = allocate dynamically from port range

# Health check (optional)
health:
  type: http               # "http", "tcp", or "exec"
  path: /healthz           # http only
  port: 8080
  # command: pg_isready    # exec only
  interval: 10s
  timeout: 2s
  grace_period: 5s         # wait before first check
  unhealthy_threshold: 3   # failures before triggering restart

# Restart policy (optional)
restart:
  policy: on-failure       # "always", "on-failure", or "never"
  max_attempts: 5
  delay: 1s
  backoff: exponential     # "fixed" or "exponential"
  max_delay: 30s
  reset_after: 5m

# Static environment variables (optional)
env:
  LOG_LEVEL: info
  APP_ENV: development

# Secrets from macOS Keychain, injected as env vars (optional)
secrets:
  DATABASE_URL:
    keychain: myapp/db-url
    rotate_every: 90d
    rotate_command: ./scripts/rotate-db-url.sh

# Container volume mounts (container services only)
volumes:
  /host/path: /container/path

# Additional container arguments (optional)
args:
  - --some-flag

# Dependency ordering (optional)
dependencies:
  after:
    - postgres             # start after postgres
    - redis
  requires:
    - postgres             # hard dependency: stop this service if postgres stops
```

### Field reference

**`service`**

| Field | Type | Description |
|---|---|---|
| `name` | string | Unique service identifier (required) |
| `type` | string | `native` or `container` (required) |
| `command` | string | Command to run, split on whitespace and executed directly — no shell (native only, required for native) |
| `working_dir` | string | Working directory for the process (native only) |
| `image` | string | Container image (container only, required for container) |
| `network_mode` | string | Docker network mode, default `host` (container only) |

**`dependencies`**

| Field | Description |
|---|---|
| `after` | Start this service only after the listed services are running |
| `requires` | Hard dependency: if any listed service stops, this service is cascade-stopped. All entries in `requires` must also appear in `after`. |

**`restart.policy` values:** `always`, `on-failure`, `never`

**`health.type` values:** `http` (checks `path` via GET), `tcp` (connects to `port`), `exec` (runs `command`, success = exit 0)

**`restart.backoff` values:** `fixed`, `exponential`

Duration values (e.g. `interval`, `timeout`, `delay`) use Go duration syntax: `10s`, `1m`, `500ms`.

## Architecture

Aurelia is structured in layers:

1. **Spec** (`internal/spec`) — parses and validates YAML service definitions
2. **Driver** (`internal/driver`) — process lifecycle abstraction with three implementations: `NativeDriver` (fork/exec), `ContainerDriver` (Docker API), `AdoptedDriver` (attach to existing PID for crash recovery)
3. **Daemon** (`internal/daemon`) — orchestrates supervised services, manages the dependency graph, persists state to `~/.aurelia/state.json`, and writes Traefik routing config
4. **API** (`internal/api`) — REST over a Unix socket (`~/.aurelia/aurelia.sock`) using Go 1.22+ `http.ServeMux` pattern routing
5. **CLI** (`cmd/aurelia`) — cobra commands; `daemon` runs in-process, all other commands are HTTP clients to the API

Supporting packages: `internal/health` (health probes), `internal/keychain` (Keychain + audit log), `internal/gpu` (Metal/IOKit via cgo), `internal/routing` (Traefik config generation), `internal/port` (dynamic port allocation), `internal/logbuf` (ring buffer log capture).

## Security Model

**Spec files have the same trust level as shell scripts.** Before loading any spec, you should understand what it will do:

- `service.command` for native services is split on whitespace and executed directly via `exec.Command`. Shell features such as pipes, redirects, and globbing are not available.
- `env` and injected secret values are passed directly to the process environment.
- `volumes` for container services are mounted as specified — including any host path.
- `args` are passed as additional arguments to the container runtime.

**Only load specs you trust.** Do not load specs from untrusted sources without reviewing them first. The spec directory (`~/.aurelia/services/`) should have permissions that prevent other users from writing to it.

**Unix socket authentication** is implicit: access to `~/.aurelia/aurelia.sock` is controlled by filesystem permissions. Only processes running as the same user can connect to the daemon. The socket is not exposed on the network by default.

**macOS Keychain** stores secrets in the user's login keychain, scoped to the aurelia process. Secret access is recorded in an append-only audit log at `~/.aurelia/audit.log`.

## Runtime Files

All runtime files are stored under `~/.aurelia/`:

| Path | Description |
|---|---|
| `config.yaml` | Daemon configuration (api_addr, routing_output) |
| `services/*.yaml` | Service spec files |
| `state.json` | PID and port persistence across restarts |
| `aurelia.sock` | Unix socket for CLI-to-daemon IPC |
| `audit.log` | Append-only NDJSON log of secret operations |
| `secret-metadata.json` | Secret rotation metadata |
| `daemon.log` | Stdout/stderr when running as a LaunchAgent |

## License

MIT
