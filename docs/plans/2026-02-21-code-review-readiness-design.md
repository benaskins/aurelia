# Code Review Readiness

**Date:** 2026-02-21
**Goal:** Prepare the Aurelia codebase for external code review by a Go engineer. Fix clear quality issues, document intentional trade-offs.

## Context

Aurelia is ~9,500 lines across 48 Go files. The codebase is already in good shape: clean package layout, idiomatic error handling (`%w` wrapping), proper concurrency (RWMutex, signal channels, context propagation), structured logging with `slog`, minimal dependencies, and 72-94% test coverage with fuzz tests.

The goal is not a rewrite — it's addressing the specific things a Go reviewer would flag, and documenting the trade-offs they'd question.

## Changes

### 1. Refactor supervision loop into explicit state machine

**File:** `internal/daemon/service.go:227-343`

**Problem:** The `supervise()` method is a 120-line `for` loop with nested `select` statements handling process start, exit, health failure, restart delay, and context cancellation. A Go reviewer will say: "This should be a state machine."

**Approach:** Extract a `supervisionState` type with explicit states:

```go
type supervisionState int

const (
    stateStarting supervisionState = iota
    stateRunning
    stateRestarting
    stateStopped
)
```

The `for` loop becomes a `switch` on the current state, with each case handling transitions. The logic stays the same — we're just making the states explicit instead of implicit in the control flow.

**Why this matters:** The supervision loop is the most complex code in the project. Making states explicit improves readability and makes it easier to reason about which transitions are possible. It also makes the code easier to test — you can verify state transitions independently.

**Test impact:** Existing supervision tests should pass unchanged. Add tests for individual state transitions.

### 2. Fix container log demultiplexing

**File:** `internal/driver/container.go:232-248`

**Problem:** Docker multiplexed log streams include 8-byte headers per frame. The current code writes raw bytes to the ring buffer with a comment saying "good enough for now." A reviewer will read that as unfinished work.

**Approach:** Use Docker's `stdcopy.StdCopy` to properly demux stdout/stderr. The `github.com/docker/docker/pkg/stdcopy` package is already a transitive dependency — we just need to use it.

```go
// Replace raw copy with proper demux
stdcopy.StdCopy(d.buf, d.buf, reader)
```

If the container uses TTY mode (no multiplexing), detect via container inspect and fall back to direct copy.

**Test impact:** Add a test with a known multiplexed stream to verify headers are stripped.

### 3. Replace `ps` parsing with syscall-based PID verification

**File:** `internal/driver/adopted.go:176-205`

**Problem:** `VerifyProcess` shells out to `ps -p <pid> -o comm=` and parses the output with `strings.Fields()`. A Go reviewer will flag this as fragile — forking a process to check a process is inelegant, and string parsing of CLI output is brittle.

**Approach:** On macOS, use `sysctl` via `golang.org/x/sys/unix` to get process info without shelling out:

```go
func getProcessName(pid int) (string, error) {
    proc, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
    if err != nil {
        return "", err
    }
    name := unix.ByteSliceToString(proc.Proc.P_comm[:])
    return name, nil
}
```

This is the idiomatic approach on macOS — no fork, no string parsing.

**Fallback:** Keep the `ps`-based approach behind a build tag for non-darwin platforms (if we ever need it), but the primary path should use sysctl.

**Test impact:** Existing `TestVerifyProcess` tests should pass with the new implementation. No behavioral change.

### 4. Document port allocation TOCTOU trade-off

**File:** `internal/port/allocator.go:50-51`

**Problem:** Between `isPortAvailable(port)` returning true and the service actually binding the port, another process could grab it. This is a classic TOCTOU race. A reviewer will notice.

**Approach:** Add a comment explaining the trade-off:

```go
// isPortAvailable performs a listen-and-close test. There's an inherent TOCTOU
// race between this check and the service binding the port — another process
// could claim it in between. This is acceptable because:
// 1. The port range (20000-32000) is unlikely to conflict with other services
// 2. If a collision occurs, the service start fails and the supervisor retries
// 3. Holding the listener open until handoff would require piping the fd to
//    the child process, adding significant complexity for a rare edge case
```

**Test impact:** None — comment only.

### 5. Document blue-green deploy separator convention

**File:** `internal/daemon/deploy.go:34`

**Problem:** Uses `name + "__" + deploySuffix` as a compound key in the port allocator. A reviewer will wonder if service names can contain `"__"`.

**Approach:** Add a comment at the usage site and a validation check in the spec parser:

```go
// Compound key for temporary port allocation during deploy. Service names
// are validated against [a-z0-9][a-z0-9._-]* in spec parsing, so "__"
// cannot appear in a service name — this separator is unambiguous.
```

Verify that spec validation actually prevents `"__"` in names. If it doesn't, add that constraint.

**Test impact:** Add a test confirming `"__"` is rejected in service names.

### 6. Add comments to intentionally ignored errors

**Files:** `internal/driver/native.go:140,148,152`, `internal/driver/adopted.go:130,136`

**Problem:** `syscall.Kill` return values are assigned to `_`. This is intentional — the process may already be dead — but uncommented ignores look like bugs to a reviewer.

**Approach:** The adopted driver (lines 130, 136) already has good context from surrounding code. The native driver needs brief comments:

```go
_ = syscall.Kill(-pid, syscall.SIGTERM) // Process group may already be exited
```

**Test impact:** None — comments only.

## Out of scope

- **API design changes** — the REST API is clean and follows Go 1.22+ mux patterns
- **CLI restructuring** — Cobra usage is standard
- **Performance optimization** — no hot paths identified
- **Additional test coverage** — current coverage is good; the state machine refactor will naturally add transition tests

## Implementation Plan

Tasks are ordered by dependency and impact. Items 4-6 are independent and can be done in parallel.

1. **Refactor supervision loop to state machine** (`internal/daemon/service.go`)
   - Extract `supervisionState` type and constants
   - Refactor `supervise()` to switch on state
   - Refactor `superviseExisting()` similarly if applicable
   - Verify all existing daemon tests pass
   - Add state transition unit tests
   - *This is the highest-impact change — do it first while context is fresh*

2. **Fix container log demultiplexing** (`internal/driver/container.go`)
   - Replace raw copy with `stdcopy.StdCopy`
   - Handle TTY vs multiplexed mode
   - Remove "good enough for now" comment
   - Add test with known multiplexed stream

3. **Replace ps-based PID verification with sysctl** (`internal/driver/adopted.go`)
   - Implement `getProcessName` using `unix.SysctlKinfoProc`
   - Replace `VerifyProcess` implementation
   - Verify existing tests pass
   - Depends on: `golang.org/x/sys/unix` (already in go.mod)

4. **Document port allocation TOCTOU** (`internal/port/allocator.go`) — comment only

5. **Document deploy separator and validate** (`internal/daemon/deploy.go`, `internal/spec/`)
   - Add explanatory comment
   - Verify spec validation rejects `"__"` in names
   - Add test if validation is missing

6. **Add error-ignore comments** (`internal/driver/native.go`) — comments only
