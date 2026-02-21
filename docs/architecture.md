# Architecture

Aurelia is structured in layers:

1. **Spec** (`internal/spec`) — parses and validates YAML service definitions
2. **Driver** (`internal/driver`) — process lifecycle abstraction with three implementations: `NativeDriver` (fork/exec), `ContainerDriver` (Docker API), `AdoptedDriver` (attach to existing PID for crash recovery)
3. **Daemon** (`internal/daemon`) — orchestrates supervised services, manages the dependency graph, persists state to `~/.aurelia/state.json`, and writes Traefik routing config
4. **API** (`internal/api`) — REST over Unix socket using Go 1.22+ `http.ServeMux` pattern routing
5. **CLI** (`cmd/aurelia`) — cobra commands; `daemon` runs in-process, all other commands are HTTP clients to the API

Supporting packages: `internal/health` (health probes), `internal/keychain` (Keychain + audit log), `internal/gpu` (Metal/IOKit via cgo), `internal/routing` (Traefik config generation), `internal/port` (dynamic port allocation), `internal/logbuf` (ring buffer log capture).

## Design Approach

Aurelia takes a vertically integrated approach: supervision, deployment, health checking, secret injection, and routing are handled by one tool rather than composed from separate ones. This has trade-offs.

### What this enables

- **Two-phase crash recovery.** When the daemon restarts after a crash, it first adopts orphaned processes by PID to preserve uptime, then redeploys each one in the background to restore full log capture and supervision. Most supervisors treat recovery as all-or-nothing — either you fully restore control or you don't. Aurelia treats it as a live migration.
- **Deploy reuses supervision.** Blue-green deploys and crash recovery redeployment both use the same code path (`DeployService`), which handles health verification, routing switches, and drain periods. There is no separate deploy tool to keep in sync with the supervisor.
- **Health checks inform restarts and deploys.** Because the supervisor owns both health checking and process lifecycle, unhealthy services are restarted automatically, and new instances during a deploy must pass health checks before traffic is switched. In tools where health checking is a separate plugin, these interactions require glue.
- **Secrets are available at process start.** Keychain-backed secrets are injected into the process environment by the supervisor itself, with audit logging. No sidecar or init script needed.

### What this costs

- **macOS only.** Keychain integration, GPU observability, and LaunchAgent support are platform-specific. A cross-platform version would lose the features that justify the integration.
- **Opinionated.** Traefik is the only supported reverse proxy. The health check types are fixed (HTTP, TCP, exec). The secret backend is macOS Keychain. If your stack doesn't align, the integration benefits don't apply.
- **Smaller ecosystem.** Supervisord has 20 years of plugins and battle-tested edge case handling. Aurelia is a single-developer project. The integrated design means you can't swap out individual components for more mature alternatives.
- **Local development scope.** The same integration that makes local development convenient (one tool, one config format, one daemon) would be a liability in production, where you want separation of concerns, redundancy, and operational maturity.
