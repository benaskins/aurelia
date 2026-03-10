# Managing Non-Go Services

Aurelia's examples are mostly Go services, but it works with any runtime. This guide covers the patterns for managing JVM, Python, and Node.js services effectively.

## 1. Build a standalone artifact

Don't use build tool dev commands (`gradle run`, `npm start`, `python manage.py runserver`) as your service command. These add build tool overhead on every restart and tie the service to the source tree.

Instead, build a distribution and point `command:` at the built artifact:

| Runtime | Build step | Command |
|---|---|---|
| JVM (Gradle) | `gradle installDist` | `command: ./build/install/myapp/bin/myapp` |
| JVM (Maven) | `mvn package` | `command: java -jar target/myapp.jar` |
| Node.js | `npm run build` | `command: node dist/server.js` |
| Python | `pip install -e .` | `command: myapp-server` (entry point script) |
| Python (uvicorn) | `pip install .` | `command: uvicorn myapp:app` |

This makes restarts fast and predictable — Aurelia restarts the process, not the build.

## 2. Find or add a health endpoint

Most frameworks include a health endpoint or make it trivial to add one:

| Framework | Built-in endpoint |
|---|---|
| Spring Boot Actuator | `/actuator/health` |
| Misk | `/_readiness` |
| Django | Add a simple view returning 200 |
| Express / Fastify | Add a `/health` route |
| Flask | Add a `/health` route |

Example spec with an HTTP health check:

```yaml
health:
  type: http
  path: /actuator/health
  port: 8080
  interval: 10s
  grace_period: 20s
```

If your service doesn't expose HTTP, use a TCP check (just verifies the port is accepting connections) or an exec check (runs a command).

## 3. Set appropriate grace periods

JVM and Python services take longer to start than Go binaries. If `grace_period` is too short, Aurelia health-checks before the service is ready and triggers a restart loop.

| Runtime | Suggested `grace_period` |
|---|---|
| Node.js | `5s` - `10s` |
| Python (uvicorn/gunicorn) | `5s` - `15s` |
| JVM (Spring Boot, Misk) | `15s` - `45s` |

When in doubt, start high and tune down. A grace period that's too long just delays the first health check; one that's too short causes restart loops.

## 4. Static vs dynamic ports

Aurelia supports dynamic port allocation (`port: 0`), where it picks a free port and injects it as the `PORT` environment variable. Your service must read `PORT` for this to work.

Many non-Go frameworks don't read `PORT` by default. If your framework doesn't, either:

- **Use a static port** — simpler, no code changes needed:

  ```yaml
  network:
    port: 8080
  ```

- **Use a wrapper script** that maps `PORT` to your framework's config:

  ```bash
  #!/bin/bash
  exec java -jar myapp.jar --server.port="$PORT"
  ```

  ```yaml
  service:
    command: /path/to/start-myapp.sh
  network:
    port: 0
  ```

## 5. Environment and PATH

Native services inherit the daemon's environment. If your service needs specific tools on `PATH` (a JVM, Python interpreter, Node.js runtime), make sure they're available:

- **System-wide install** — tools in `/usr/local/bin` or `/opt/homebrew/bin` are usually on the daemon's PATH
- **Full path in command** — use the absolute path to the runtime:

  ```yaml
  service:
    command: /opt/homebrew/bin/node dist/server.js
  ```

- **Wrapper script** — for complex environment setup:

  ```bash
  #!/bin/bash
  export JAVA_HOME=/Library/Java/JavaVirtualMachines/temurin-21.jdk/Contents/Home
  export PATH="$JAVA_HOME/bin:$PATH"
  exec java -jar /path/to/myapp.jar
  ```

If you run Aurelia as a LaunchAgent, the daemon's environment may be more restricted than your shell. Test by running `aurelia status` after a fresh login to confirm services start correctly.

## Full example: Spring Boot service

```yaml
# ~/.aurelia/services/billing-api.yaml
service:
  name: billing-api
  type: native
  command: ./build/install/billing-api/bin/billing-api
  working_dir: ~/projects/billing

network:
  port: 8080

health:
  type: http
  path: /actuator/health
  port: 8080
  interval: 10s
  grace_period: 30s
  unhealthy_threshold: 3

restart:
  policy: on-failure
  max_attempts: 3
  delay: 5s

dependencies:
  after: [postgres]
  requires: [postgres]

env:
  SPRING_PROFILES_ACTIVE: local
  JAVA_OPTS: -Xmx512m
```
