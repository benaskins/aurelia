# Security Model

**Spec files have the same trust level as shell scripts.** Before loading any spec, you should understand what it will do:

- `service.command` for native services is split on whitespace and executed directly via `exec.Command`. Shell features such as pipes, redirects, and globbing are not available.
- `env` and injected secret values are passed directly to the process environment.
- `volumes` for container services are mounted as specified — including any host path.
- `args` are passed as additional arguments to the container runtime.

**Only load specs you trust.** Do not load specs from untrusted sources without reviewing them first. The spec directory (`~/.aurelia/services/`) should have permissions that prevent other users from writing to it.

**Unix socket authentication** is implicit: access to `~/.aurelia/aurelia.sock` is controlled by filesystem permissions (0600). Only processes running as the same user can connect to the daemon.

**TCP API authentication** is required when the daemon is started with `--api-addr`. A random bearer token is generated on startup and written to `~/.aurelia/api.token` (0600). All TCP API requests must include the `Authorization: Bearer <token>` header. The token file is removed on clean shutdown. The Unix socket does not require a token.

**macOS Keychain** stores secrets in the user's login keychain, scoped to the aurelia process. Secret access is recorded in an append-only audit log at `~/.aurelia/audit.log`.

## Trust Boundaries

**Trusted inputs** — service specs (`~/.aurelia/services/*.yaml`) and daemon config (`~/.aurelia/config.yaml`) are loaded from user-writable directories and executed with the user's privileges. Treat these files with the same caution as shell scripts.

**Untrusted inputs** — the TCP API (`--api-addr`) accepts requests from the network. All TCP requests require bearer token authentication; however, binding to a non-loopback address exposes the API beyond localhost. Prefer `127.0.0.1:<port>` unless remote access is specifically needed, and rotate the token in `~/.aurelia/api.token` if you suspect it has been compromised.

## Network Exposure Guidance

- The Unix socket (`~/.aurelia/aurelia.sock`) is protected by filesystem permissions (0600) and is only accessible to the current user. This is the default and recommended API transport.
- The TCP listener is opt-in via `--api-addr` and should bind to `127.0.0.1` for local-only access. Binding to `0.0.0.0` or a non-loopback interface exposes the API to other machines on the network.
- Error messages returned over TCP are generic (e.g., "service not found") to avoid leaking internal state to remote clients. Unix socket clients receive full error details.

## Runtime Input Validation

Service names in API requests are used as map keys, never interpolated into shell commands. Port numbers are validated by the OS `net.Listen` call. Spec fields are parsed by Go's YAML decoder with no custom deserialization.
