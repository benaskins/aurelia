---
name: aurelia-debug
description: Use when an aurelia-managed service is misbehaving, returning errors, or unreachable. Extends /debug with aurelia-specific tools.
---

# Aurelia Debug

Follow `/debug` — here's how each step works with aurelia.

## Reproducing: work outside-in

```bash
# Through Traefik (what users hit)
curl -sw "\n%{http_code}" https://<service>.studio.internal/health

# Direct (bypass Traefik)
curl -sw "\n%{http_code}" http://127.0.0.1:<port>/health
```

Direct works but Traefik doesn't → routing issue. Neither works → service is down.

## Logs and status

```bash
aurelia status              # Is it running, failed, or stopped?
aurelia logs <service>      # Scan for ERROR, panic, connection refused, timeout
```

## Common hypotheses

| Symptom | Start here |
|---|---|
| 502 from Traefik | Service is down — `aurelia status` |
| Config changes ignored | Needs restart — `aurelia restart <service>` |
| Stale behaviour after rebuild | Needs redeploy — `just deploy-prod <service>` |
| Service won't start | Binary missing, bad permissions, or port conflict — check logs |

## Restart vs redeploy

`aurelia restart <service>` — config changed. `just deploy-prod <service>` — code changed.

$ARGUMENTS
