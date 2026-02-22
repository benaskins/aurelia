---
name: aurelia-deploy
description: Use when adding a new service to the Aurelia mesh or shipping changes to an existing service. Covers the full cycle — setup, build, deploy, and verification with evidence.
---

# Aurelia Deploy

Two modes: adding a new service to the mesh, or shipping changes to an existing one. Both end with verification.

## Adding a new service

### Understand the service

Before writing a spec, figure out:
- What serves HTTP? A binary, a dev server, a container?
- How does it accept a port? Flag, env var, config file?
- What's the health endpoint?
- Does it need a build step?

### Handle the $PORT contract

Aurelia allocates dynamic ports via the `PORT` environment variable. The service must read `$PORT` to bind.

If the binary reads `$PORT` natively — no wrapper needed, set `port: 0` in the spec.

If the binary uses a flag (e.g. `--port`), create a `bin/serve` wrapper:

```sh
#!/bin/sh
exec /path/to/binary --port "${PORT}" [other flags]
```

### Create the service spec

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

### Validate and start

```bash
aurelia check ~/.aurelia/services/<name>.yaml
aurelia reload
aurelia up <name>
```

Then verify as below.

## Shipping changes

### Pre-flight

- Confirm which service(s) to deploy
- Branch should be clean (`git status`)
- If multiple services, deploy sequentially to isolate failures

### Test, build, deploy

```bash
just ship-prod <service>    # test → build → deploy (full pipeline)
```

If tests fail — stop. Show failures. Do not deploy broken code.

> **Note:** `aurelia deploy` performs zero-downtime blue-green deploys only for services with a `routing:` config. Non-routed services fall back to a simple restart (brief downtime).

### Verify

After every deploy (new service or update), verify with evidence:

```bash
# Health check
curl -sf https://<service>.studio.internal/health

# Check logs for errors
aurelia logs <service>
```

Scan logs for: `ERROR`, `panic`, `connection refused`, `timeout`, stack traces.

**Report with evidence:**
```
Deployed: <service>
Health: 200 OK
Logs: clean (no errors)
```

If health check fails or logs show errors — show the evidence and ask the user whether to investigate or rollback.

### Multi-service deploys

Deploy one at a time, completing the full verify cycle for each. If one fails, stop — don't continue with the others.

## Common patterns

| Service type | Command | Notes |
|---|---|---|
| Go binary | `bin/serve` wrapper | Build with `go build -o bin/<name>` first |
| Hugo dev server | `bin/serve` wrapper | Needs `--bind 127.0.0.1 --port "${PORT}" --appendPort=false --disableLiveReload` |
| Node/Vite dev server | `bin/serve` wrapper | Most read `$PORT` natively, check first |
| Python (uvicorn) | `bin/serve` wrapper | `--port "${PORT}" --host 127.0.0.1` |
| Static files | Simple file server | Or `python3 -m http.server "${PORT}"` in a wrapper |
