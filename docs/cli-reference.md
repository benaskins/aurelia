# CLI Reference

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

## Daemon flags

```
--api-addr string        Optional TCP address for the API (e.g. 127.0.0.1:9090)
--routing-output string  Path to write Traefik dynamic config (enables routing)
```

These can also be set in `~/.aurelia/config.yaml` as `api_addr` and `routing_output`.

## Deploy flags

```
--drain string    Drain period before stopping old instance (default "5s")
```

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
