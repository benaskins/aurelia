# Aurelia

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
- Automatic restart with fixed or exponential backoff
- Crash recovery — re-adopts running processes across daemon restarts
- Dynamic port allocation from a configurable range
- Zero-downtime blue-green deploys
- Traefik routing config generation
- macOS Keychain secret injection with audit logging
- Apple Silicon GPU/VRAM/thermal observability
- LaunchAgent install for auto-start on login

## Installation

Requires macOS and Go 1.22+ with cgo enabled. Docker or OrbStack required only for container services.

```bash
git clone https://github.com/benaskins/aurelia
cd aurelia
just build
```

Or without just:

```bash
go build -o aurelia ./cmd/aurelia/
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

## Documentation

- [CLI Reference](docs/cli-reference.md) — commands, flags, runtime files
- [API](docs/api.md) — REST endpoints over Unix socket or TCP
- [Service Spec](docs/service-spec.md) — full YAML format, field reference, examples
- [Architecture](docs/architecture.md) — layers, design approach, trade-offs
- [Security](docs/security.md) — trust model, authentication, network exposure

## License

MIT
