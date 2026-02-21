# Open Source Positioning & README Design

## Context

Aurelia is ready for open-source release. The target audience is solo and small-team Go developers on macOS running 3-8 local services. The README needs to clearly communicate what Aurelia is, show a real multi-service example, and differentiate from existing tools — without marketing fluff.

## Positioning

**Tagline:** "A process supervisor for macOS developers"

**Tone:** Technical and understated. Factual descriptions, not pain-point narratives. Let the feature set do the talking.

**Key message in one sentence:** Aurelia supervises native processes and Docker containers with dependency ordering, health checks, and automatic restarts — configured as YAML, controlled from a single CLI.

## README Structure

### 1. Header + one-liner

```
# Aurelia

A process supervisor for macOS developers — manages native processes and
Docker containers with dependency ordering, health checks, and automatic restarts.
```

No badges initially. Add them once there are CI, releases, and docs to link to.

### 2. Features (reordered)

Lead with what developers care about on day one, defer advanced features:

**Core (why you install it):**
- YAML service definitions — one file per service
- Native processes and Docker containers under one supervisor
- Dependency ordering — topological startup, reverse shutdown, cascade-stop for hard deps
- Health checks — HTTP, TCP, exec probes with configurable thresholds
- Automatic restart — on-failure/always/never with fixed or exponential backoff
- Crash recovery — daemon restarts don't restart your services (PID re-adoption)

**Operational (why you keep using it):**
- Dynamic port allocation from a configurable range, injected as `PORT`
- Live reload — file watcher auto-detects spec changes, only restarts what changed
- LaunchAgent install — daemon starts at login
- Spec validation — `aurelia check` for CI or pre-commit

**Advanced (features you discover later):**
- Zero-downtime blue-green deploys with health verification
- Traefik routing config generation (hostname, TLS, mTLS)
- macOS Keychain secret storage with audit log and rotation
- Apple Silicon GPU/VRAM/thermal monitoring

### 3. Quick Start

Tighten to three steps: install, write a spec, start the daemon. Current quick start is close but can be more concise.

### 4. Real-world example

This is new and important. Show a 3-service stack that demonstrates the hybrid native/container value:

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
  port: 0  # dynamic

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

Then show:
```bash
aurelia daemon &
aurelia status
```

And the output: all three services running, with postgres started first, api after postgres is healthy, worker last. If postgres stops, api and worker cascade-stop.

### 5. Comparison table

Short, factual, respectful:

| | Aurelia | Overmind | Goreman | process-compose | docker-compose |
|---|---|---|---|---|---|
| Native processes | yes | yes | yes | yes | no |
| Containers | yes | no | no | no | yes |
| Dependency ordering | yes | no | no | yes | yes |
| Health checks | yes | no | no | yes | yes |
| Restart policies | yes | no | no | yes | yes |
| macOS Keychain secrets | yes | no | no | no | no |
| Daemon crash recovery | yes | no | no | no | yes |
| Zero-downtime deploy | yes | no | no | no | no |
| Config format | YAML (per-service) | Procfile | Procfile | YAML | YAML |
| Platform | macOS | Linux/macOS | Linux/macOS | cross-platform | cross-platform |

No editorializing in the table — the feature gaps are self-evident.

### 6. Reference sections (preserve existing)

Keep the current README's reference material largely as-is:
- CLI Reference (table format)
- Service Spec Format (full field reference)
- Architecture
- Security Model
- Runtime Files

These are well-written and complete. Minor adjustments:
- Move Architecture below the reference sections (most users won't read it first)
- Add a brief "API" section documenting the REST endpoints (currently only in code)

### 7. License

MIT (already set).

## What to cut or change from the current README

- **Cut:** "Requirements" section as a standalone block. Fold into installation ("requires macOS, Go 1.22+, Docker optional").
- **Change:** Move Architecture to the bottom — it's contributor-facing, not user-facing.
- **Change:** Reorder features list as described above.
- **Add:** Multi-service example.
- **Add:** Comparison table.
- **Add:** Brief API endpoint reference.

## Pre-launch checklist (not part of README)

Before open-sourcing, verify:
- [ ] `go install` works cleanly (currently build-from-source only)
- [ ] `aurelia --version` reports correctly from `just build`
- [ ] LICENSE file exists
- [ ] `.goreleaser.yml` or equivalent for binary releases / Homebrew tap
- [ ] CI (GitHub Actions) for `go test ./...` and `go vet ./...`
- [ ] No hardcoded paths or personal config in the repo

## Implementation Plan

1. **Draft the new README** — restructure per this design, keeping existing reference content
2. **Write the multi-service example** — test that the three specs (postgres/api/worker) actually work together, then include them
3. **Add comparison table** — verify feature claims against current versions of each tool
4. **Set up release infrastructure** — goreleaser config, GitHub Actions CI, Homebrew tap
5. **Review for open-source readiness** — scan for hardcoded paths, verify LICENSE, check `go install` path
6. **Ship it** — push to public repo

Steps 1-3 can happen in parallel. Step 4 is independent. Step 5 depends on 1-4. Step 6 depends on 5.
