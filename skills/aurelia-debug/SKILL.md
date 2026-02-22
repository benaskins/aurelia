---
name: aurelia-debug
description: Use when an aurelia-managed service is misbehaving, returning errors, or unreachable. Work outside-in — routing, then service health, then logs.
---

# Aurelia Debug

Systematic debugging for services managed by aurelia. Work outside-in: can you reach it, is it running, what do the logs say.

## Outside-in

### Check routing

```bash
# Through Traefik (what users hit)
curl -sw "\n%{http_code}" https://<service>.studio.internal/health

# Direct (bypass Traefik)
curl -sw "\n%{http_code}" http://127.0.0.1:<port>/health
```

Direct works but Traefik doesn't → routing issue. Neither works → service is down.

### Check service status

```bash
aurelia status
```

Look at the service state — is it running, failed, or stopped? If failed, the logs will tell you why.

### Read logs

```bash
aurelia logs <service>
```

Scan for: `ERROR`, `panic`, `connection refused`, `timeout`, stack traces.

## Common issues

| Symptom | Likely cause |
|---|---|
| 502 from Traefik | Service is down or unhealthy — check `aurelia status` |
| Config changes ignored | Service needs restart — `aurelia restart <service>` |
| Stale behaviour after rebuild | Didn't redeploy — `just deploy-prod <service>` |
| Service won't start | Binary missing, bad permissions, or port conflict — check logs |
| Auth cookies not sent | Check domain (`.studio.internal`), Secure flag, HTTPS |

## Restart vs redeploy

```bash
aurelia restart <service>       # Restart with current binary/container
just deploy-prod <service>      # Rebuild + redeploy
```

Use restart when config changed. Use redeploy when code changed.
