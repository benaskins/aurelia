# Design: `aurelia logs --follow`

**Date:** 2026-04-08  
**Status:** Approved

## Problem

`aurelia logs <service>` returns a static JSON snapshot of the last N lines. There is no way to stream live output — passing `-f` produces `Error: unknown shorthand flag: 'f' in -f`. Developers watching live service output must resort to `watch` polling workarounds, which are awkward and introduce latency.

## Approach: Server-side streaming with generation counter

Add a monotonic `generation` counter to the ring buffer. The existing logs API endpoint grows a `?follow=true` query parameter that keeps the HTTP connection open, polls the buffer at 100ms intervals for new lines, and streams them as plain `text/plain` via `http.Flusher` (HTTP/1.1 chunked transfer encoding). The CLI gains `--follow`/`-f` flag that uses a no-timeout HTTP client and reads the streaming response body line-by-line until EOF or Ctrl+C.

No new dependencies required.

## Components

### `internal/logbuf/ring.go`

- Add `generation int` field, incremented on each `addLine` call.
- Add method:
  ```go
  // Since returns all lines written after gen, plus the new generation counter.
  // Pass gen=0 to get all currently buffered lines.
  func (r *Ring) Since(gen int) (lines []string, newGen int)
  ```
- Existing `Lines()`, `Last(n)`, and `Write()` behaviour unchanged.

### `internal/driver/driver.go`

- Add to `Driver` interface:
  ```go
  // LogLinesSince returns lines written after gen, plus the new generation counter.
  LogLinesSince(gen int) ([]string, int)
  ```

### `internal/driver/{native,container}.go`

- Implement `LogLinesSince` by delegating to `ring.Since(gen)`.

### `internal/driver/{adopted,remote}.go`

- Stub implementation returning `nil, 0` (no local log capture for these driver types).

### `internal/daemon/service.go`

- Add `LogsSince(gen int) ([]string, int)` to `ManagedService`.

### `internal/daemon/daemon.go`

- Add `ServiceLogsSince(name string, gen int) ([]string, int, error)`.

### `internal/api/server.go`

Extend `serviceLogs` handler:

```
if ?follow=true:
  Content-Type: text/plain
  assert http.Flusher supported
  gen = 0
  initial snapshot: lines, gen = ServiceLogsSince(name, 0) → write all lines, flush
  loop at 100ms:
    lines, gen = ServiceLogsSince(name, gen) → write new lines, flush if any
    exit on r.Context().Done()
```

### `cmd/aurelia/client.go`

- Add `apiStreamClient()` — same Unix socket transport, no `Timeout` on the `http.Client`.
- Add `--follow`/`-f` bool flag to `logsCmd`.
- When `--follow` is set:
  - If `--node` is also set, return error: `--follow is not supported with --node`
  - GET `/v1/services/{name}/logs?follow=true`
  - Read body with `bufio.Scanner`, print each line to stdout
  - Return on EOF (service stopped) or OS interrupt (Ctrl+C)
  - `--json` flag is silently ignored

## What's not in scope

- Restart survival — follow stops when service stops; re-run to follow the new instance
- Timestamps on streamed lines
- `--node` remote follow (returns an error with a clear message)

## Testing

- Ring buffer: unit tests for `Since` covering basic tracking, wrap-around, gen=0, concurrent access
- API: integration test — write lines to a service buffer, connect with `?follow=true`, verify lines received; verify connection closes on context cancel
- CLI: manual test with `aurelia logs -f <service>`
