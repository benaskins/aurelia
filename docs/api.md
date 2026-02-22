# API

REST over Unix socket (`~/.aurelia/aurelia.sock`). Optional TCP listener with bearer token auth via `--api-addr`.

| Method | Path | Description |
|---|---|---|
| `GET` | `/v1/services` | List all services |
| `GET` | `/v1/services/{name}` | Get service state |
| `POST` | `/v1/services/{name}/start` | Start a service |
| `POST` | `/v1/services/{name}/stop` | Stop a service (cascades to hard dependents) |
| `POST` | `/v1/services/{name}/restart` | Restart a service |
| `POST` | `/v1/services/{name}/deploy` | Blue-green deploy for routed services (`?drain=5s`); falls back to restart for non-routed |
| `GET` | `/v1/services/{name}/logs` | Get log lines (`?n=100`) |
| `POST` | `/v1/reload` | Re-read specs and reconcile |
| `GET` | `/v1/gpu` | GPU/VRAM/thermal state |
| `GET` | `/v1/health` | Daemon health check |
