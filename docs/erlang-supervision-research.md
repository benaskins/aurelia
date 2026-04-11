# Erlang/OTP Supervision Patterns: Applicability to Aurelia

## Executive Summary

Erlang/OTP's supervision model is the gold standard for fault-tolerant systems. This document analyzes its core patterns, compares them against Aurelia's existing supervision model, and evaluates which ideas would be beneficial to adopt, which Aurelia already implements in spirit, and which don't translate well to an OS-level process supervisor.

**Bottom line:** Aurelia already implements several Erlang-inspired patterns (per-service supervision loops, restart policies, dependency ordering). The highest-value adoptions would be **restart intensity limiting** (max restarts within a time window) and **formalized supervision strategies** (`one_for_one`, `one_for_all`, `rest_for_one`). Other patterns like hierarchical supervision trees are less applicable because Aurelia manages OS processes (not lightweight Erlang processes), and the cost model is fundamentally different.

---

## 1. Erlang/OTP Supervision Model Overview

### 1.1 Core Concepts

In Erlang/OTP, a **supervisor** is a process whose sole job is to monitor child processes (workers or other supervisors) and react to failures:

- **Workers**: Processes that do actual work (equivalent to Aurelia services).
- **Supervisors**: Processes that monitor workers and other supervisors, forming a tree.
- **Supervision tree**: A hierarchical structure where supervisors own their children. The entire application is a tree of supervisors with workers at the leaves.

### 1.2 Restart Strategies

Each supervisor declares one of four strategies:

| Strategy | Behavior |
|----------|----------|
| `one_for_one` | Only the crashed child is restarted. Other children are unaffected. |
| `one_for_all` | If one child crashes, all children under this supervisor are terminated and restarted. |
| `rest_for_one` | If a child crashes, it and all children started *after* it are terminated and restarted (preserves start ordering). |
| `simple_one_for_one` | Specialized `one_for_one` for dynamically spawned homogeneous children (like a pool of identical workers). |

### 1.3 Restart Intensity

Every supervisor declares:

- **MaxRestarts**: Maximum number of restarts allowed...
- **MaxSeconds**: ...within this time window.

If a supervisor exceeds MaxRestarts within MaxSeconds, it gives up and terminates itself. This escalates the failure to its *parent* supervisor, which then applies its own restart strategy. This escalation mechanism is the key insight: **a failing subsystem that can't self-heal propagates its failure upward until something higher in the tree can handle it** (or the application terminates).

Example: `{one_for_one, 3, 60}` means "if more than 3 restarts happen in 60 seconds, this supervisor has failed."

### 1.4 Child Specifications

Each child in a supervisor has:

- **Start function**: How to start the process.
- **Restart type**: `permanent` (always restart), `transient` (restart on abnormal exit), `temporary` (never restart).
- **Shutdown**: Timeout for graceful shutdown, or `brutal_kill` for immediate termination.
- **Type**: `worker` or `supervisor`.

### 1.5 "Let it crash" Philosophy

Erlang processes are lightweight (~2KB each), so restarting them is cheap. The philosophy is:

1. Don't write defensive code to handle every possible error.
2. Let processes crash on unexpected errors.
3. The supervisor restarts them in a known-good state.
4. Isolate failure domains so one crash doesn't corrupt unrelated state.

---

## 2. Aurelia's Current Supervision Model

### 2.1 Architecture

Aurelia's supervision is a **flat, daemon-centric model**:

```
Daemon (top-level supervisor)
├── ManagedService "web-api"     (supervision goroutine)
├── ManagedService "postgres"    (supervision goroutine)
├── ManagedService "redis"       (supervision goroutine)
└── ManagedService "worker"      (supervision goroutine)
```

Each `ManagedService` runs an independent supervision goroutine with a 5-phase state machine:

```
Starting → Running → Evaluating → Restarting → (back to Starting)
                                 ↘ Monitoring (oneshot)
                                 ↘ Stopped (terminal)
```

### 2.2 Restart Policies (per-service)

| Aurelia Policy | Erlang Equivalent |
|----------------|-------------------|
| `always` | `permanent` |
| `on-failure` | `transient` |
| `never` | `temporary` |
| `oneshot` | No direct equivalent (health-monitor after clean exit) |

### 2.3 Restart Limiting

Aurelia supports `max_attempts` (total lifetime restart count) and backoff (fixed or exponential, with configurable delay and max_delay). However, it **does not** have a sliding time window for restart intensity — the counter is monotonically increasing and never resets (except on explicit `RestartService` API call).

### 2.4 Dependency Management

Aurelia uses a **dependency DAG** with two edge types:

- `after`: Ordering constraint (soft dependency — start A after B).
- `requires`: Hard dependency (cascade stop — if B dies, A is stopped too).

This provides topological startup ordering and cascade-stop propagation for hard dependencies, but no group-level restart strategies.

### 2.5 Crash Recovery

On daemon restart, Aurelia can **adopt** previously running processes by verifying PID identity (command match, start time, `AURELIA_SERVICE` env tag). This is unique to OS-level supervisors — Erlang supervisors don't need this because the VM manages process lifecycle entirely.

---

## 3. Gap Analysis

### 3.1 What Aurelia Already Has (Erlang-Equivalent)

| Erlang Pattern | Aurelia Implementation |
|----------------|----------------------|
| Per-child supervision | Per-service supervision goroutine with state machine |
| `permanent` / `transient` / `temporary` | `always` / `on-failure` / `never` |
| Graceful shutdown with timeout | `Stop()` sends SIGTERM, waits timeout, then SIGKILL |
| Start ordering | Topological sort of dependency DAG |
| Health monitoring | Independent health monitor with configurable threshold |
| Process isolation | OS-level process isolation (stronger than Erlang's) |

### 3.2 What Aurelia Lacks

| Erlang Pattern | Gap in Aurelia | Impact |
|----------------|---------------|--------|
| **Restart intensity (MaxR/MaxT)** | `max_attempts` is lifetime-total, no sliding window | A service that crashes 100 times over a month is treated the same as one that crashes 100 times in 10 seconds. No escalation mechanism for rapid-fire failures. |
| **Supervision strategies** | No `one_for_all` or `rest_for_one` | Services in a tightly coupled group can't be restarted atomically. The `requires` cascade only *stops* dependents; it doesn't restart them together. |
| **Hierarchical supervision** | Flat structure; Daemon is the single supervisor | Can't express "these 3 services form a subsystem with its own failure policy." No escalation beyond the daemon level. |
| **Failure escalation** | A service that exhausts restarts just stays stopped | No mechanism to escalate to a higher-level response (e.g., restart the entire service group, alert an operator, or shut down gracefully). |
| **Restart counter reset on stability** | Counter never resets after a period of stable running | A service that has been running fine for days still carries its historical restart count, reducing headroom. |

---

## 4. Recommendations

### 4.1 Adopt: Restart Intensity (MaxRestarts / MaxSeconds)

**Priority: High**

This is the single highest-value adoption from Erlang/OTP. Replace (or supplement) the lifetime `max_attempts` counter with a sliding-window intensity limit.

**Current behavior:**
```yaml
restart:
  policy: always
  max_attempts: 10      # Total lifetime — after 10 restarts, ever, service stays down
```

**Proposed behavior:**
```yaml
restart:
  policy: always
  max_restarts: 5       # Max restarts...
  within: 60s           # ...within this sliding window
  max_attempts: 0       # 0 = unlimited lifetime (default)
```

**Semantics:**
- Track restart timestamps in a ring buffer.
- On each restart, check: have there been `max_restarts` restarts in the last `within` duration?
- If exceeded: stop the service and fire an escalation event (log, webhook, etc.).
- If the service runs stably for the full `within` window, the oldest entries age out — the counter effectively resets.

**Why this matters:** Without this, Aurelia can't distinguish between a fundamentally broken service (crash-looping every second) and a service that has occasional hiccups over weeks. The sliding window makes exponential backoff more effective too — if a service stabilizes after a few restarts, the backoff state can reset.

**Implementation sketch:**

```go
type restartTracker struct {
    timestamps []time.Time
    maxR       int
    maxT       time.Duration
}

func (rt *restartTracker) allow() bool {
    now := time.Now()
    cutoff := now.Add(-rt.maxT)
    // Count restarts within window
    recent := 0
    for _, ts := range rt.timestamps {
        if ts.After(cutoff) {
            recent++
        }
    }
    return recent < rt.maxR
}

func (rt *restartTracker) record() {
    rt.timestamps = append(rt.timestamps, time.Now())
    // Prune old entries
    cutoff := time.Now().Add(-rt.maxT)
    rt.timestamps = slices.DeleteFunc(rt.timestamps, func(t time.Time) bool {
        return t.Before(cutoff)
    })
}
```

This integrates naturally into the existing `shouldRestart()` method in `service.go`.

### 4.2 Adopt: Backoff Reset on Stability

**Priority: High** (complements 4.1)

When a service has been running healthily for longer than the restart delay window (e.g., `within` duration or a configurable `reset_after`), reset the restart counter and backoff delay. This prevents a service from being permanently penalized for past instability.

**Current behavior:** `restartCount` only resets on explicit `RestartService()` API call.

**Proposed:** Reset `restartCount` to 0 when the service transitions from `phaseRunning` and has been running for longer than `reset_after` (default: the `within` window, or 5 minutes if no window is set).

This is standard in Erlang — a child that has been running long enough is considered stable, and its restart budget is refreshed.

### 4.3 Consider: Service Groups with Restart Strategies

**Priority: Medium**

Introduce an optional `group` concept that ties multiple services together under a shared restart strategy:

```yaml
# ~/.aurelia/groups/api-stack.yaml
group: api-stack
strategy: rest_for_one    # or one_for_all, one_for_one
max_restarts: 3
within: 120s
services:
  - postgres
  - redis
  - web-api
  - worker
```

**Strategies:**

| Strategy | Aurelia Behavior |
|----------|-----------------|
| `one_for_one` (default) | Only the failed service restarts. This is Aurelia's current behavior. |
| `one_for_all` | If any service in the group fails, all services in the group are restarted (in dependency order). Useful for services with shared mutable state. |
| `rest_for_one` | If a service fails, it and all services started after it (in the group's order) are restarted. Useful for pipelines. |

**Why this matters:** The existing `requires` cascade only propagates *stops*. Consider a scenario: `web-api` requires `postgres`. If `postgres` crashes, `web-api` is cascade-stopped. When `postgres` comes back, `web-api` is *not* automatically restarted. With a group strategy, `rest_for_one` on `[postgres, redis, web-api, worker]` would restart `web-api` and `worker` after `postgres` recovers.

**Trade-off:** This adds significant complexity. The existing `requires` + per-service restart policies handle most cases well enough. Groups are most valuable for tightly coupled service sets (e.g., a database + its dependent API + workers).

**Recommendation:** Defer implementation but design the spec format now. The `requires` cascade could be extended to restart (not just stop) dependents first, which covers the most common `rest_for_one` use case without full group support.

### 4.4 Consider: Cascade Restart (not just Cascade Stop)

**Priority: Medium-High** (simpler alternative to 4.3)

Extend the existing `requires` dependency to support restarting dependents when a dependency recovers. Currently:

1. Service B `requires` service A.
2. A crashes → B is cascade-stopped.
3. A restarts automatically (its own restart policy).
4. B stays stopped. **An operator must manually restart B.**

With cascade restart:

1. A crashes → B is cascade-stopped.
2. A restarts and becomes healthy.
3. B is automatically restarted.

This covers the most important `rest_for_one` use case without introducing groups. It could be opt-in:

```yaml
dependencies:
  requires: [postgres]
  restart_with: true    # Restart when required dependencies recover
```

Or simply made the default behavior for `requires`, since the current behavior (leave dependents stopped) is arguably a bug more than a feature.

### 4.5 Consider: Failure Escalation

**Priority: Medium**

When a service exhausts its restart budget (or exceeds restart intensity), Aurelia currently just stops the service and logs a message. Erlang escalates to the parent supervisor. Aurelia doesn't have a supervisor hierarchy, but it could support escalation hooks:

```yaml
restart:
  policy: always
  max_restarts: 5
  within: 60s
  on_exhausted: 
    - action: notify        # Log + emit event
    - action: restart_group # Restart the service's group
    - action: stop_group    # Stop the entire group
```

Or at the daemon level, a global escalation policy for services that enter a "permanently failed" state. This is less critical than restart intensity but completes the pattern.

### 4.6 Don't Adopt: Full Supervision Tree Hierarchy

**Priority: Low / Not Recommended**

Erlang's nested supervisor trees work because:

1. Erlang processes are extremely lightweight (~2KB, microsecond startup).
2. Supervisors are themselves processes — cheap to create and destroy.
3. The VM provides transparent process communication (message passing, links, monitors).
4. Restarting an Erlang process is essentially free.

Aurelia manages OS processes, where:

1. Processes are heavy (MB of memory, seconds to start).
2. A "supervisor" would be a daemon-level construct, not a separate process.
3. There's no transparent communication — services use TCP/HTTP/sockets.
4. Restarting is expensive and requires health-check grace periods.

A full supervision tree would add complexity without proportional benefit. The flat daemon + dependency DAG + groups (if adopted) provides enough structure for OS-level process management.

### 4.7 Don't Adopt: `simple_one_for_one` (Dynamic Child Pools)

Erlang's `simple_one_for_one` is for dynamically spawning many identical children (like a connection pool). Aurelia's services are defined statically via YAML specs. Dynamic process pools are better handled by the services themselves (e.g., a Go service manages its own goroutine pool, or a container orchestrator manages replicas).

### 4.8 Don't Adopt: "Let It Crash" Philosophy Directly

Erlang's "let it crash" works because restarting is cheap and state can be reconstructed. OS processes often have expensive startup (loading data, warming caches, establishing connections). Aurelia's existing approach — health checks, graceful shutdown, restart backoff — is more appropriate for the cost model.

---

## 5. Summary Table

| Erlang Pattern | Recommendation | Effort | Value |
|----------------|---------------|--------|-------|
| Restart intensity (MaxR/MaxT) | **Adopt** | Low | High |
| Backoff reset on stability | **Adopt** | Low | High |
| Cascade restart (requires) | **Adopt** | Medium | High |
| Service groups + strategies | **Consider** | High | Medium |
| Failure escalation hooks | **Consider** | Medium | Medium |
| Supervision tree hierarchy | **Don't adopt** | Very High | Low |
| simple_one_for_one pools | **Don't adopt** | N/A | N/A |
| "Let it crash" philosophy | **Don't adopt** | N/A | N/A |

---

## 6. Proposed Implementation Order

1. **Restart intensity** (sliding window) — Modify `shouldRestart()` and `restartTracker` in `service.go`. Add `max_restarts` and `within` to `RestartPolicy` in `spec.go`. ~100 lines of code.

2. **Backoff reset** — Add a timer in `handleRunning()` that resets `restartCount` after stable running. ~30 lines.

3. **Cascade restart** — Extend `startService()` to detect when a dependency has recovered and restart its cascade-stopped dependents. Requires tracking which services were cascade-stopped vs. explicitly stopped. ~150 lines.

4. **Escalation hooks** — Add `on_exhausted` to `RestartPolicy`. Fire callbacks when restart budget is exceeded. ~100 lines.

5. **Service groups** (if warranted) — New `group.go` in daemon package, new YAML spec format. ~500 lines.

---

## 7. References

- [Erlang/OTP Supervisor Behaviour](https://www.erlang.org/doc/design_principles/sup_princ.html)
- [Elixir Supervisor Documentation](https://hexdocs.pm/elixir/Supervisor.html)
- Joe Armstrong, "Making reliable distributed systems in the presence of software errors" (2003)
- Fred Hebert, "Learn You Some Erlang" — [Supervisors chapter](https://learnyousomeerlang.com/supervisors)
