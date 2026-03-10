# Service Spec Format

Specs are YAML files placed in `~/.aurelia/services/`. Each file defines one service.

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

## Full Spec Reference

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
  port: 8080               # 0 = allocate dynamically; injected as $PORT env var

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

env:
  LOG_LEVEL: info
  APP_ENV: development

secrets:
  DATABASE_URL:
    keychain: myapp/db-url

# Container only
volumes:
  /host/path: /container/path

# Container only
args:
  - --some-flag

dependencies:
  after:
    - postgres
    - redis
  requires:
    - postgres             # cascade-stop if postgres stops
```

## Field Reference

### `service`

| Field | Type | Description |
|---|---|---|
| `name` | string | Unique service identifier (required) |
| `type` | string | `native`, `container`, or `external` (required) |
| `command` | string | Command to run, split on whitespace and executed directly — no shell (native only). Pass arguments inline: `command: /usr/bin/myapp --flag value` |
| `working_dir` | string | Working directory for the process (native only) |
| `image` | string | Container image (container only) |
| `network_mode` | string | Docker network mode, default `host` (container only) |

### `network`

| Field | Type | Description |
|---|---|---|
| `port` | int | Listen port. Set to `0` for dynamic allocation — aurelia picks a free port and injects it as the `PORT` environment variable. Your binary must read `$PORT` to know which port to bind. |

### Dynamic port allocation and the `PORT` env var

When you set `port: 0`, Aurelia allocates a free port from its configured range and sets the `PORT` environment variable in the service's process environment before starting it. The service **must** read `PORT` and bind to that port. If it doesn't, Aurelia will health-check the allocated port while the service listens on its own hardcoded port, and the service will appear permanently unhealthy.

**How to read `PORT` in common frameworks:**

| Runtime | How to use `PORT` |
|---|---|
| Go | `os.Getenv("PORT")` and pass to your listener |
| Node.js | `process.env.PORT` — most frameworks (Express, Fastify) accept this directly |
| Python (uvicorn) | `uvicorn app:app --port $PORT` in a wrapper script |
| Python (gunicorn) | `gunicorn app:app --bind 0.0.0.0:$PORT` in a wrapper script |
| Spring Boot | Set `SERVER_PORT=$PORT` in the `env:` block, or pass `--server.port=$PORT` via a wrapper |
| JVM (Jetty, Misk) | Read `PORT` from the environment in your application bootstrap code |

**If your service can't easily read `PORT`**, use a static port instead:

```yaml
network:
  port: 8080    # fixed port — no PORT env var injected
```

This avoids the mismatch entirely. Dynamic allocation is most useful when running multiple instances of the same service or when you don't care which port a service gets.

### `dependencies`

| Field | Description |
|---|---|
| `after` | Start this service only after the listed services are running |
| `requires` | Hard dependency: if any listed service stops, this service is cascade-stopped. All entries in `requires` must also appear in `after`. |

### `service.type` values

- `native` — fork/exec of a local binary
- `container` — Docker image managed via the Docker API
- `external` — Aurelia does not start or stop this service; it only monitors health. Useful for representing external dependencies (databases, APIs) in the dependency graph.

### Native command arguments

Arguments are passed inline in `service.command`, not via the `args` field
(`args` is for container image entrypoint overrides only):

```yaml
# correct — arguments inline in command
service:
  type: native
  command: /opt/homebrew/bin/ollama serve

# wrong — args under service: is silently ignored by the YAML parser;
# the validator only catches top-level args, not misplaced ones
service:
  type: native
  command: /opt/homebrew/bin/ollama
  args: [serve]                         # no effect
```

For commands that need complex argument lists or environment variable setup,
a wrapper script keeps the spec readable:

```bash
# ~/start-ollama.sh
#!/bin/bash
export OLLAMA_HOST=0.0.0.0
exec /opt/homebrew/bin/ollama serve
```

```yaml
service:
  type: native
  command: /Users/you/start-ollama.sh
```

### `restart.policy` values

`always`, `on-failure`, `never`

### `health.type` values

`http` (GET to `path`, success on 2xx), `tcp` (connect to `port`), `exec` (runs `command`, success on exit 0)

### `restart.backoff` values

`fixed`, `exponential`

### Duration values

Fields like `interval`, `timeout`, `delay` use Go duration syntax: `10s`, `1m`, `500ms`.
