---
name: aurelia-deploy
description: Use when adding a new service to the Aurelia mesh or shipping changes to an existing service. Extends /deploy with aurelia-specific tools.
---

# Aurelia Deploy

Follow `/deploy` — here's how each step works with aurelia.

## Shipping changes

```bash
just ship-prod <service>    # test → build → deploy (full pipeline)
```

`aurelia deploy` performs zero-downtime blue-green deploys for services with `routing:` config. Non-routed services fall back to a simple restart.

## Adding a new service

1. Understand the service: what serves HTTP, how does it accept a port, what's the health endpoint?
2. Handle the `$PORT` contract — aurelia allocates dynamic ports via `PORT` env var
3. Create a spec at `~/.aurelia/services/<name>.yaml`
4. Validate and start:

```bash
aurelia check ~/.aurelia/services/<name>.yaml
aurelia reload
aurelia up <name>
```

## Verifying

```bash
curl -sf https://<service>.studio.internal/health
aurelia logs <service>
```

Then `/verify` with evidence:
```
Deployed: <service>
Health: 200 OK
Logs: clean (no errors)
```

$ARGUMENTS
