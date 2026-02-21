# Aurelia

A process supervisor for macOS developers — manages native processes and Docker containers with dependency ordering, health checks, and automatic restarts.

## Features

**Core:**

- **YAML service definitions** — one file per service in `~/.aurelia/services/`
- **Native processes and Docker containers** — run Go binaries natively and infrastructure in containers, under one supervisor
- **Dependency ordering** — topological startup, reverse shutdown, cascade-stop for hard dependencies
- **Health checks** — HTTP, TCP, and exec probes with configurable intervals, thresholds, and grace periods
- **Automatic restart** — on-failure, always, or never, with fixed or exponential backoff
- **Crash recovery** — re-adopts running processes across daemon restarts via PID persistence

**Operational:**

- **Dynamic port allocation** — assigns ports from a configurable range (default 20000–32000), injected as `PORT`
- **Live reload** — file watcher detects spec changes, only restarts services whose specs changed
- **LaunchAgent install** — `aurelia install` starts the daemon at login
- **Spec validation** — `aurelia check` validates specs without starting anything

**Advanced:**

- **Zero-downtime deploys** — blue-green deploy with health verification, routing switch, and drain period
- **Traefik routing** — generates dynamic config from service routing specs (hostname, TLS, mTLS)
- **macOS Keychain secrets** — store, inject, rotate, and audit secrets via the system Keychain
- **GPU observability** — Apple Silicon VRAM usage and thermal state via Metal/IOKit

## Installation

Requires macOS and Go 1.22+ with cgo enabled. Docker or OrbStack required only for container services.

Build from source:

```bash
git clone https://github.com/benaskins/aurelia
cd aurelia
just build    # injects version string via ldflags
```

Or without just:

```bash
go build -o aurelia ./cmd/aurelia/
```

Optionally, start the daemon at login:

```bash
aurelia install
```

## Quick Start

1. Create a service spec:

```yaml
# ~/.aurelia/services/api.yaml
service:
  name: api
  type: native
  command: ./bin/api
  working_dir: ~/myproject

network:
  port: 8080

health:
  type: http
  path: /healthz
  port: 8080
  interval: 10s
```

2. Start the daemon and bring up services:

```bash
aurelia daemon &
aurelia up
aurelia status
```

## Example: Multi-Service Stack

A typical setup — Go API and worker running natively, Postgres as a container:

```yaml
# ~/.aurelia/services/postgres.yaml
service:
  name: postgres
  type: container
  image: postgres:16

network:
  port: 5432

health:
  type: tcp
  port: 5432
  interval: 5s
  grace_period: 3s

env:
  POSTGRES_PASSWORD: dev
```

```yaml
# ~/.aurelia/services/api.yaml
service:
  name: api
  type: native
  command: go run ./cmd/api
  working_dir: ~/myproject

network:
  port: 0    # dynamic allocation

health:
  type: http
  path: /healthz
  interval: 10s
  grace_period: 5s

dependencies:
  after: [postgres]
  requires: [postgres]
```

```yaml
# ~/.aurelia/services/worker.yaml
service:
  name: worker
  type: native
  command: go run ./cmd/worker
  working_dir: ~/myproject

dependencies:
  after: [postgres, api]
  requires: [postgres]
```

`aurelia up` starts postgres first, waits for its health check to pass, then starts the API (on a dynamically allocated port), then the worker. If postgres stops, the API and worker cascade-stop automatically.

## Comparison

| | Aurelia | Overmind | Goreman | process-compose | docker-compose |
|---|---|---|---|---|---|
| Native processes | yes | yes | yes | yes | no |
| Containers | yes | no | no | no | yes |
| Dependency ordering | yes | no | no | yes | yes |
| Health checks | yes | no | no | yes | yes |
| Restart policies | yes | no | no | yes | yes |
| Live reload | yes | no | no | yes | no |
| Crash recovery | yes | no | no | no | yes |
| Zero-downtime deploy | yes | no | no | no | no |
| macOS Keychain secrets | yes | no | no | no | no |
| Config format | YAML | Procfile | Procfile | YAML | YAML |
| Platform | macOS | Linux/macOS | Linux/macOS | cross-platform | cross-platform |

## CLI Reference

| Command | Description |
|---|---|
| `aurelia daemon` | Run the supervisor daemon |
| `aurelia status` | Show service name, type, state, health, PID, port, uptime, restart count |
| `aurelia up [service...]` | Start one or more services (all if no args) |
| `aurelia down [service...]` | Stop one or more services (all if no args) |
| `aurelia restart <service>` | Restart a service |
| `aurelia deploy <service>` | Zero-downtime blue-green deploy |
| `aurelia logs <service>` | Show recent log output (`-n` to set line count) |
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

### Deploy flags

```
--drain string    Drain period before stopping old instance (default "5s")
```

## API

REST over Unix socket (`~/.aurelia/aurelia.sock`). Optional TCP listener with bearer token auth via `--api-addr`.

| Method | Path | Description |
|---|---|---|
| `GET` | `/v1/services` | List all services |
| `GET` | `/v1/services/{name}` | Get service state |
| `POST` | `/v1/services/{name}/start` | Start a service |
| `POST` | `/v1/services/{name}/stop` | Stop a service (cascades to hard dependents) |
| `POST` | `/v1/services/{name}/restart` | Restart a service |
| `POST` | `/v1/services/{name}/deploy` | Blue-green deploy (`?drain=5s`) |
| `GET` | `/v1/services/{name}/logs` | Get log lines (`?n=100`) |
| `POST` | `/v1/reload` | Re-read specs and reconcile |
| `GET` | `/v1/gpu` | GPU/VRAM/thermal state |
| `GET` | `/v1/health` | Daemon health check |

## Service Spec Format

Specs are YAML files placed in `~/.aurelia/services/`. Each file defines one service.

```yaml
service:
  name: myapp              # unique service name
  type: native             # "native", "container", or "external"

  # native only
  command: ./bin/myapp
  working_dir: /path/to/project

  # container only
  # image: myimage:latest
  # network_mode: host     # default "host"

network:
  port: 8080               # 0 = allocate dynamically from port range

health:
  type: http               # "http", "tcp", or "exec"
  path: /healthz           # http only
  port: 8080
  # command: pg_isready    # exec only
  interval: 10s
  timeout: 2s
  grace_period: 5s         # wait before first check
  unhealthy_threshold: 3   # failures before triggering restart

restart:
  policy: on-failure       # "always", "on-failure", or "never"
  max_attempts: 5
  delay: 1s
  backoff: exponential     # "fixed" or "exponential"
  max_delay: 30s
  reset_after: 5m

env:
  LOG_LEVEL: info
  APP_ENV: development

secrets:
  DATABASE_URL:
    keychain: myapp/db-url
    rotate_every: 90d
    rotate_command: ./scripts/rotate-db-url.sh

# Container only
volumes:
  /host/path: /container/path

args:
  - --some-flag

dependencies:
  after:
    - postgres
    - redis
  requires:
    - postgres             # cascade-stop if postgres stops
```

### Field reference

**`service`**

| Field | Type | Description |
|---|---|---|
| `name` | string | Unique service identifier (required) |
| `type` | string | `native`, `container`, or `external` (required) |
| `command` | string | Command to run, split on whitespace and executed directly — no shell (native only) |
| `working_dir` | string | Working directory for the process (native only) |
| `image` | string | Container image (container only) |
| `network_mode` | string | Docker network mode, default `host` (container only) |

**`dependencies`**

| Field | Description |
|---|---|
| `after` | Start this service only after the listed services are running |
| `requires` | Hard dependency: if any listed service stops, this service is cascade-stopped. All entries in `requires` must also appear in `after`. |

**`service.type` values:**

- `native` — fork/exec of a local binary
- `container` — Docker image managed via the Docker API
- `external` — Aurelia does not start or stop this service; it only monitors health. Useful for representing external dependencies (databases, APIs) in the dependency graph.

**`restart.policy` values:** `always`, `on-failure`, `never`

**`health.type` values:** `http` (GET to `path`, success on 2xx), `tcp` (connect to `port`), `exec` (runs `command`, success on exit 0)

**`restart.backoff` values:** `fixed`, `exponential`

Duration values (e.g. `interval`, `timeout`, `delay`) use Go duration syntax: `10s`, `1m`, `500ms`.

## Security Model

**Spec files have the same trust level as shell scripts.** Before loading any spec, you should understand what it will do:

- `service.command` for native services is split on whitespace and executed directly via `exec.Command`. Shell features such as pipes, redirects, and globbing are not available.
- `env` and injected secret values are passed directly to the process environment.
- `volumes` for container services are mounted as specified — including any host path.
- `args` are passed as additional arguments to the container runtime.

**Only load specs you trust.** Do not load specs from untrusted sources without reviewing them first. The spec directory (`~/.aurelia/services/`) should have permissions that prevent other users from writing to it.

**Unix socket authentication** is implicit: access to `~/.aurelia/aurelia.sock` is controlled by filesystem permissions (0600). Only processes running as the same user can connect to the daemon.

**TCP API authentication** is required when the daemon is started with `--api-addr`. A random bearer token is generated on startup and written to `~/.aurelia/api.token` (0600). All TCP API requests must include the `Authorization: Bearer <token>` header. The token file is removed on clean shutdown. The Unix socket does not require a token.

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
| `api.token` | Bearer token for TCP API auth (created when `--api-addr` is set) |
| `daemon.log` | Stdout/stderr when running as a LaunchAgent |

## Architecture

Aurelia is structured in layers:

1. **Spec** (`internal/spec`) — parses and validates YAML service definitions
2. **Driver** (`internal/driver`) — process lifecycle abstraction with three implementations: `NativeDriver` (fork/exec), `ContainerDriver` (Docker API), `AdoptedDriver` (attach to existing PID for crash recovery)
3. **Daemon** (`internal/daemon`) — orchestrates supervised services, manages the dependency graph, persists state to `~/.aurelia/state.json`, and writes Traefik routing config
4. **API** (`internal/api`) — REST over Unix socket using Go 1.22+ `http.ServeMux` pattern routing
5. **CLI** (`cmd/aurelia`) — cobra commands; `daemon` runs in-process, all other commands are HTTP clients to the API

Supporting packages: `internal/health` (health probes), `internal/keychain` (Keychain + audit log), `internal/gpu` (Metal/IOKit via cgo), `internal/routing` (Traefik config generation), `internal/port` (dynamic port allocation), `internal/logbuf` (ring buffer log capture).

## License

MIT
