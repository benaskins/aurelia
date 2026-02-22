---
name: deploy-to-mesh
description: Use when adding a new service to the Aurelia service mesh. Also use when the user wants to make a local project available on studio.internal, or asks to create an aurelia service spec.
---

# Deploy to Mesh

Add a new service to the Aurelia service mesh with Traefik routing and TLS.

**Announce at start:** "I'm using the deploy-to-mesh skill to add this service to Aurelia."

## Workflow

### 1. Understand the service

Determine:
- **What serves HTTP?** A binary, a dev server (hugo, vite, etc.), a container?
- **How does it accept a port?** Flag (`--port`), env var, config file?
- **What's the health endpoint?** `/health`, `/api/index`, `/` — whatever returns 200.
- **Does it need a build step?** Go binary, static site build, etc.

### 2. Handle the $PORT contract

Aurelia allocates dynamic ports and injects them as the `PORT` environment variable. The service must read `$PORT` to know which port to bind.

**If the binary reads $PORT natively** (e.g., Go services using `os.Getenv("PORT")`):
- No wrapper needed. Set `port: 0` in the spec.

**If the binary uses a CLI flag** (e.g., `--port 8080`, `-p 1313`):
- Create a `bin/serve` shell wrapper that translates `$PORT` to the flag:

```sh
#!/bin/sh
exec /path/to/binary --port "${PORT}" [other flags]
```

- `chmod +x bin/serve`
- Point `service.command` at the wrapper.

### 3. Create the service spec

Write to `~/.aurelia/services/<name>.yaml`:

```yaml
service:
  name: <name>
  type: native
  command: /path/to/binary-or-wrapper

network:
  port: 0

routing:
  hostname: <name>.studio.internal
  tls: true

health:
  type: http
  path: /health
  interval: 10s
  timeout: 2s
  grace_period: 10s
  unhealthy_threshold: 3

restart:
  policy: on-failure
  max_attempts: 3
  delay: 5s
  backoff: exponential
  max_delay: 2m
```

Adjust `health.path` to match the service's actual health/readiness endpoint.

### 4. Validate, start, verify

```bash
aurelia check ~/.aurelia/services/<name>.yaml   # Validate spec
aurelia reload                                   # Pick up new spec
aurelia up <name>                                # Start service
sleep 10                                         # Wait for grace period
aurelia status                                   # Confirm: running + healthy
```

If state is `failed`:
```bash
aurelia logs <name>                              # Check what went wrong
```

### 5. Verify routing

```bash
curl -sk https://<name>.studio.internal/         # Through Traefik
```

## Common Patterns

| Service type | Command | Notes |
|---|---|---|
| Go binary | `/path/to/bin/serve` | Build with `go build -o bin/<name> ./cmd/<name>` first |
| Hugo dev server | `bin/serve` wrapper | Needs `--bind 127.0.0.1 --port "${PORT}" --appendPort=false --disableLiveReload` |
| Node/Vite dev server | `bin/serve` wrapper | Most respect `$PORT` natively, check first |
| Python (uvicorn, etc.) | `bin/serve` wrapper | `--port "${PORT}" --host 127.0.0.1` |
| Static files | Use a simple file server | Or `python3 -m http.server "${PORT}"` in a wrapper |

## Checklist

- [ ] Binary or server builds/runs locally
- [ ] `$PORT` contract handled (native or wrapper)
- [ ] Spec written and passes `aurelia check`
- [ ] Service starts and reaches `healthy` state
- [ ] `curl -sk https://<name>.studio.internal/` returns expected response
