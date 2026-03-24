# Aurelia

> Standalone tool · Part of the [lamina](https://github.com/benaskins/lamina-mono) workspace

A process supervisor for macOS developers — manages native processes and Docker containers with dependency ordering, health checks, and automatic restarts.

## Why Aurelia

If you run a mix of native services and containers on macOS, your options are limited. docker-compose doesn't manage native processes. Procfile runners like overmind don't do dependency ordering or health checks. You end up stitching together multiple tools or wrapping everything in containers.

Aurelia handles native processes, containers, dependencies, health checks, and routing under one supervisor. It integrates with macOS Keychain for secrets and Apple Silicon GPU APIs for observability — features that only make sense on macOS, but work well there.

| | Aurelia | process-compose | Overmind / Goreman | docker-compose |
|---|---|---|---|---|
| Sweet spot | macOS, mixed native + container stacks | Cross-platform process orchestration | Simple Procfile runner | All-container stacks |
| Native processes | yes | yes | yes | no |
| Containers | yes | no | no | yes |
| Dependency ordering | yes | yes | no | yes |
| Health checks | yes | yes | no | yes |

**Not** a production tool, container orchestrator, or cross-platform solution. See [Architecture](docs/architecture.md) for design rationale.

## Features

- YAML service definitions — one file per service in `~/.aurelia/services/`
- Native processes and Docker containers under one supervisor
- Dependency ordering with cascade-stop for hard dependencies
- HTTP, TCP, and exec health checks with configurable thresholds
- Automatic restart with fixed or exponential backoff, plus oneshot restart policy for run-once commands with ongoing health monitoring
- Crash recovery — re-adopts running processes across daemon restarts
- Dynamic port allocation from a configurable range
- Zero-downtime blue-green deploys for routed services
- Traefik routing config generation
- Multi-node clustering — mTLS peer connections, peer liveness monitoring, cross-node service visibility, token rotation with peer distribution
- macOS Keychain secret injection with audit logging
- OpenBao secrets backend — KV v1 with auto-unseal, falls back to macOS Keychain
- LLM-powered diagnostics — interactive TUI for reasoning about service state via tool calls
- Apple Silicon GPU/VRAM/thermal observability
- LaunchAgent install for auto-start on login

## Prerequisites

**Required:**

- **macOS** — Aurelia uses macOS-specific APIs (Keychain, Metal/IOKit) and is not cross-platform
- **Go 1.26+** with cgo enabled — for building from source

**Optional (needed only if you use the corresponding features):**

- **Docker or OrbStack** — for `type: container` services
- **Traefik** — for routing; Aurelia generates Traefik dynamic config files, Traefik serves them
- **just** — task runner used by `script/bootstrap` and development commands (installed automatically by bootstrap)

## Installation

```bash
git clone https://github.com/benaskins/aurelia
cd aurelia
script/bootstrap
```

Or without just:

```bash
go build -o aurelia ./cmd/aurelia/
```

To build and install to `~/.local/bin` (restarts the daemon if running):

```bash
just install
```

For a leaner binary without container or GPU support:

```bash
just build-lean
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

## Multi-node Clustering

Aurelia daemons on separate machines can form a cluster, giving you cross-node service visibility and coordinated token rotation from a single CLI.

```yaml
# ~/.aurelia/config.yaml
node_name: studio
api_addr: 0.0.0.0:9090

tls:
  cert: /etc/aurelia/tls/node.crt
  key:  /etc/aurelia/tls/node.key
  ca:   /etc/aurelia/tls/ca.crt

nodes:
  - name: mini
    addr: mini.local:9090
    token_file: ~/.aurelia/peers/mini.token
```

Peers authenticate via mTLS (mutual TLS with a shared CA). The TCP listener also accepts bearer token auth for CLI clients. Rate limiting (20 req/s sustained, 100 burst) is applied per source identity. Peer liveness is checked every 10 seconds with automatic connection pool cleanup on failure.

Token rotation is coordinated across the cluster:

```bash
aurelia token rotate           # Generate new token, push to all peers, keep old token valid until confirmed
aurelia token rotate --force   # Invalidate old token immediately
```

## LLM-powered Diagnostics

The `aurelia diagnose` command opens an interactive Bubble Tea TUI where an LLM reasons about your services using tool calls against the aurelia API.

```bash
aurelia diagnose               # Diagnose all services
aurelia diagnose api           # Focus on a specific service
```

The LLM has access to read-only tools (list services, inspect config, read logs, check health, query GPU metrics, view cluster state, check dependencies, get system resources) and action tools (start, stop, restart) that require operator confirmation through the TUI before execution.

Configure in `~/.aurelia/config.yaml`:

```yaml
diagnose:
  provider: anthropic          # or "ollama"
  model: claude-sonnet-4-20250514
  api_key_secret: anthropic-api-key  # resolved via aurelia secret, falls back to env var
```

## Oneshot Restart Policy

For services that run a command once and then rely on health checks to detect when they need to run again (e.g., `orbctl start` for OrbStack):

```yaml
restart:
  policy: oneshot

health:
  type: exec
  command: orbctl status
  interval: 30s
```

The command runs, and if it exits 0, aurelia enters a health-monitoring phase — no process, but the health checker keeps running. If the health check fails, the command is re-executed. A non-zero exit triggers normal restart logic. A health check block is required for oneshot services.

## OpenBao Secrets Backend

Aurelia can use [OpenBao](https://openbao.org/) (open-source Vault fork) as its secrets backend instead of macOS Keychain. When configured, OpenBao is preferred; if unreachable, aurelia falls back to Keychain automatically.

```yaml
# ~/.aurelia/config.yaml
openbao:
  addr: http://127.0.0.1:8200
  token_file: ~/.aurelia/bao-token
  mount: secret                # KV v1 mount path (default: "secret")
  unseal_file: ~/.aurelia/bao-unseal-key  # optional: auto-unseal on startup
```

Auto-unseal: if the server is sealed and `unseal_file` is configured, aurelia automatically unseals it on first access.

## Documentation

- [CLI Reference](docs/cli-reference.md) — commands, flags, runtime files
- [API](docs/api.md) — REST endpoints over Unix socket or TCP
- [Service Spec](docs/service-spec.md) — full YAML format, field reference, examples
- [Architecture](docs/architecture.md) — layers, design approach, trade-offs
- [Security](docs/security.md) — trust model, authentication, network exposure
- [Non-Go Services](docs/non-go-services.md) — managing JVM, Python, and Node.js services

## License

Apache License 2.0 — see [LICENSE](LICENSE) for details.

## Links

[![Hypercommit](https://img.shields.io/badge/Hypercommit-DB2475)](https://hypercommit.com/aurelia)
