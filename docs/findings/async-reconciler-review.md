---
date: March 7, 2026
status: resolved
component: Provider / Async Reconciler (internal/provider, internal/provider/processor/plan)

resolutions:
  F1: acked — March 8, 2026
  F2: resolved — March 8, 2026
  F3: resolved — March 8, 2026
  F4: resolved — March 8, 2026
  F5: resolved — March 8, 2026
  F8: resolved — March 8, 2026
  F9: resolved — March 8, 2026
---

# Findings: Async Phase-Driven Reconciler — Architecture Review

## Problem Description

A thorough end-to-end architecture review of the async phase-driven reconciler was conducted
across `internal/provider`, `internal/provider/processor/plan`, `internal/message`, and
`pkg/keyedworker`. The goal was to surface design mismatches, missing implementations, and
overengineered components that add complexity without commensurate value, with a particular
focus on premature optimizations.

The review produced **nine findings** grouped into three categories:

| ID  | Category | Title | Severity |
|-----|----------|-------|----------|
| F1  | Missing impl | `reconcileTruth()` documented as fixed, never implemented | Critical |
| F2  | Off-chart | `ExecutionProgress` causes double Job fetch with no benefit | High |
| F3  | Dead code | ~~`Worker.Resources` field is never accessed~~ | Low |
| F4  | Behavioral mismatch | ~~`recoveryState` retries based on current schedule, not failed operation~~ | Medium |
| F5  | Overengineered | ~~`Config` builder (13 methods) used in exactly one callsite~~ | Low |
| F6  | Overengineered | `StateResult` has two timer fields mapping to the same underlying timer | Low |
| F7  | Premature opt | `watchable.Map` for 1-subscriber fan-out | Medium |
| F8  | DRY violation | ~~Duplicate per-key goroutine pool: `Coordinator` vs `keyedworker.Pool`~~ | Medium |
| F9  | Design risk | ~~Recursive `handle()` on `StateResult.Requeue` — unbounded stack depth~~ | Low |

---

## F1: `reconcileTruth()` Documented as Fixed, Never Implemented

**Status: acked** _(March 8, 2026 — risk accepted; reconciler is the source of truth)_

### Problem Description

RFC-0008 "Issues & Resolutions" marks H3 as resolved:

> H3: No divergence detection between optimistic and persisted state
> ✅ Fixed: `reconcileTruth()` every 5th poll cycle

Searching the entire codebase finds exactly **one reference** to `reconcileTruth` — a comment
inside `worker.go`'s `mergeIncoming()`:

```go
// reconcileTruth() provides the correction path if the optimistic status ever
// genuinely diverges from the persisted state.
```

The function is never defined or called anywhere in the codebase.

### Root Cause Analysis

`mergeIncoming()` applies an optimistic merge strategy: it carries the Worker's in-memory
`plan.Status` forward onto every incoming watchable delivery to prevent informer lag from
reverting in-flight mutations. This is correct and intentional. However, the design relies on
`reconcileTruth()` as an eventual-correction path for the case where the in-memory status
genuinely diverges (e.g., after a StatusWriter failure or an operator restart where
`cachedCtx` is cold).

### Resolution — Acked

`reconcileTruth()` will **not** be implemented. The reconciler itself is the source of truth
for plan status maintenance.

The RFC claim "Fixed: `reconcileTruth()` every 5th poll cycle" overstated the solution. The
actual and sufficient correction mechanism is the Provider reconcile loop:

1. The Provider reconciles on every schedule tick and on every object change event.
2. Each reconcile delivers a fresh informer snapshot through the watchable map to the
   Coordinator → Worker via `mergeIncoming()`.
3. Once the StatusWriter successfully writes the mutated status to the API server, the
   informer cache is updated. The next reconcile cycle then delivers a snapshot that reflects
   the persisted status. `mergeIncoming()` carries _that_ optimistic status forward, which
   now matches the persisted state — the divergence window naturally collapses.
4. On operator restart, `cachedCtx` is nil; the first delivery unconditionally adopts the
   informer snapshot (the correct persisted state), so cold-start divergence cannot occur.

The only residual risk is a StatusWriter failure that prevents persisted-state catchup.
This is bounded by the StatusWriter's retry logic and the operator's reconcile frequency; it
does not require a dedicated `reconcileTruth()` call to correct.

**Action taken**: Remove the stale `reconcileTruth()` comment from `mergeIncoming()` and
update the inline doc to reflect the reconciler-as-source-of-truth contract. The RFC comment
"✅ Fixed" is retracted; the correct statement is that the reconciler loop provides the
correction path implicitly.
### Historical Options (Appendix)

These were the options considered during investigation. Option B was chosen, with the
rationale documented in the Resolution section above.

#### Option A: Implement minimal `reconcileTruth()` _(not taken)_

On every Nth slot delivery (e.g., N=5), replace the optimistic status with an authoritative
read from the API server:

```go
func (s *Worker) reconcileTruth(ctx context.Context) {
    var live hibernatorv1alpha1.HibernatePlan
    if err := s.APIReader.Get(ctx, s.key, &live); err != nil {
        s.log.Error(err, "reconcileTruth: failed to fetch live plan")
        return
    }
    s.cachedCtx.Plan.Status = live.Status
}
```

- **Pros**: Completes the stated design; bounded correction window (5 poll cycles max)
- **Cons**: Extra direct API read per 5 deliveries (bypasses cache); adds state to Worker;
  duplicates what the reconciler loop already provides for free

#### Option B: Remove the comment; document the reconciler-as-source-of-truth contract _(chosen)_

Remove the `reconcileTruth()` reference from `mergeIncoming()` and document explicitly that
the Provider reconcile loop is the correction mechanism. The optimistic merge remains valid
because the reconciler re-delivers a fresh snapshot on every cycle, and once the StatusWriter
completes a write the next delivery naturally closes the divergence window.

- **Pros**: No new code; honest and accurate about the actual correction mechanism
- **Cons**: If StatusWriter failures accumulate, the divergence window extends until the
  StatusWriter recovers — mitigated by the StatusWriter's retry logic

### Impact

Acked: the residual risk of extended divergence on StatusWriter failure is accepted.
The reconcile loop provides the correction path without a dedicated `reconcileTruth()` function.

---

## F2: `ExecutionProgress` Causes Double Job Fetch

**Status: resolved** _(March 8, 2026 — `ExecutionProgress` removed; `DeliveryNonce int64` is the final change-detection signal)_

### Problem Description

`PlanReconciler.computeExecutionProgress()` fetches a `batchv1.JobList` from the informer
cache to build an `ExecutionProgress{CycleID, Completed, Failed}`:

```go
if err := r.List(ctx, &jobList,
    client.InNamespace(plan.Namespace),
    client.MatchingLabels{...},
); err != nil { ... }
```

This value is stored in `PlanContext.ExecutionProgress`. The code comment is explicit:

> "used purely as a watchable change signal — Workers never read this field; they perform
> their own authoritative `APIReader.List`"

The Worker independently fetches the same Jobs via `getCurrentCycleJobs()` using
`s.APIReader.List()`:

```go
// getCurrentCycleJobs fetches an authoritative view of all runner Jobs for the
// current cycle directly from the API server (bypassing the informer cache).
func (s *state) getCurrentCycleJobs(ctx context.Context, plan *hibernatorv1alpha1.HibernatePlan) ([]batchv1.Job, error) {
    var jobList batchv1.JobList
    if err := s.cfg.APIReader.List(ctx, &jobList, ...); err != nil { ... }
    ...
}
```

### Root Cause Analysis

`watchable.Map.Store()` performs a deep-equality check on the stored value. If the new
`PlanContext` is equal to the old one, the delivery is suppressed. During a Job execution
phase, the plan's `Spec`, `Status`, and all other fields are static — nothing changes between
reconcile cycles. Without `ExecutionProgress`, every Provider reconcile triggered by a Job
status change would be suppressed because the `PlanContext` equality check would return `true`.

The `ExecutionProgress` field was introduced as a workaround: by embedding mutable running
counts, every Job completion breaks `PlanContext` equality and forces a watchable delivery,
giving the Worker an event-driven wake-up.

**This is a leaky abstraction.** The Provider performs extra I/O to compute a value that has
no semantic meaning to the Worker — it exists solely to trick the watchable equality check.
The result is:

1. Provider fetches Jobs from informer cache (potentially stale, O(n) list per reconcile)
2. Worker fetches the same Jobs from API server (authoritative, O(n) list per handle)
3. Worker ignores the Provider's result entirely

This is worst-of-both-worlds: extra Provider work and stale data that is immediately
discarded.

### Resolution

`ExecutionProgress` and `computeExecutionProgress()` are deleted. `PlanContext` gains a
`DeliveryNonce int64` field set to a monotonically incrementing counter on `PlanReconciler`:

```go
// PlanReconciler
deliveryNonce atomic.Int64

// In Reconcile()
planCtx := &message.PlanContext{
    ...
    DeliveryNonce: r.deliveryNonce.Add(1),
}
```

The nonce serves two complementary roles:

1. **Change-detection signal** — because it increments on every reconcile, the watchable
   equality check always returns `false`, re-delivering the context to the Worker for any
   external change that triggers the provider (Job status update, ConfigMap write, etc.).
   This gives the Worker a fast event-driven reaction path in addition to the
   `requeueTimer`-based self-poll.

2. **Correctness under external dependencies** — resources outside the `PlanContext`
   equality check (restore-point ConfigMaps, runner Job status) can change without
   bumping any field the equality function inspects. The nonce guarantees those changes
   still re-drive the Worker.

The unconditional increment is safe because all state handlers are idempotent — an extra
Handle() call with unchanged plan state is a no-op. The nonce never needs to be read by
any handler.

**Overflow** is not a concern: at 1 reconcile/second `int64` exhausts in ~292 years, and
even at wrap-around two consecutive `Add(1)` calls always return values differing by 1
(non-zero), so the `!=` equality check remains correct indefinitely.

### Historical Options (Appendix)

#### Option A: `ResourceVersion`-based equality only _(not chosen as sole mechanism)_

An intermediate approach relied purely on `ResourceVersion` checks in `planEqual()` and
`exceptionsEqual()`, combined with Worker self-polling via `requeueTimer`. This was
discarded because:

- External dependencies (restore ConfigMaps, Job status) do not bump `plan.ResourceVersion`.
  A Job completing mid-stage would not re-deliver the `PlanContext` to the Worker, making
  reaction purely timer-driven instead of event-driven.
- It required Active-only exception filtering to prevent expired exception status writes
  from causing spurious re-deliveries — which is still in place, but as a correctness
  measure independent of the nonce.

#### Option B: Accept as pragmatic trade-off _(not taken)_

Keep `ExecutionProgress` and document the double-fetch. Not taken — the leaky abstraction
would become a permanent fixture.

### Impact

Resolved: the extra `r.List()` call during execution phases is eliminated. The Worker
receives a watchable re-delivery on every provider reconcile cycle regardless of which
external resource triggered the reconcile, enabling the fastest possible reaction path.

---

## F3: `Worker.Resources` Field is Dead Code

**Status: resolved** _(March 8, 2026 — field is actively used; finding was stale)_

### Problem Description

The `Worker` struct declares:

```go
Resources *message.ControllerResources
```

The original finding stated that no Worker method ever dereferences `s.Resources`, and
proposed removing the field.

### Root Cause Analysis

The finding was stale. `Worker.Resources` is threaded through `buildConfig()` into
`state.Config.Resources`, and `state_lifecycle.go` uses that field to delete the plan
from the watchable map during plan deletion cleanup:

```go
// state_lifecycle.go
state.Resources.PlanResources.Delete(state.Key)
```

The field is not dead — it carries the `ControllerResources` handle needed by the lifecycle
state handler to deregister the plan from the Provider's watchable map when Kubernetes
deletes the `HibernatePlan` object.

### Resolution

No action required. The field is correctly used. The finding was written before (or missed)
the `state_lifecycle.go` deletion path.

### Impact

None — the field is load-bearing. Removing it would break plan deletion cleanup.

---

## F4: `recoveryState` Retries Based on Current Schedule, Not Failed Operation

**Status: resolved** _(March 8, 2026 — operation derived from `CurrentOperation`, Option A)_

### Problem Description

When `recoveryState.handleRetry()` fired, the operation was chosen from the current
`ScheduleResult.ShouldHibernate`. If a plan failed mid-hibernation and the operator
applied the `retry-now` annotation after the window moved to wakeup time, recovery
dispatched a wakeup against never-hibernated resources — likely producing another error
with a confusing message.

### Resolution

`handleRetry` now derives the operation from `plan.Status.CurrentOperation` (Option A):

```go
operation := hibernatorv1alpha1.PlanOperation(plan.Status.CurrentOperation)
if operation == "" {
    // Fallback: no prior operation, derive from current schedule
    ...
}
```

When the persisted operation conflicts with the current schedule window, a structured log
message informs the operator:

> _"retrying failed operation that conflicts with current schedule window — proceeding with
> original operation; to follow the current schedule, suspend the plan and perform manual
> intervention or resubmit the plan"_

The fallback (empty `CurrentOperation`) still uses the schedule-derived value for
backward compatibility with plans that pre-date the `CurrentOperation` field.

### Appendix: Rejected Alternative

Option B (document current behavior) was not taken. The `retry-now` annotation creates
a clear operator intent — "retry what failed" — that the schedule-derived path violates.
Dispensing the opposite operation silently is worse than informing the operator via a log
message that a conflict exists.

### Impact

None — applied. Correctness at schedule boundaries and for manual `retry-now` operations
is restored.

---

## F5: `Config` Builder Pattern — Used in One Callsite

**Status: resolved** _(March 8, 2026 — builder already replaced with struct literal)_

### Problem Description

`state.NewConfig()` provided a 13-method fluent builder invoked from exactly **one place**:
`Worker.buildConfig()`. Builder patterns only pay off with multiple callsites or optional
fields; all 13 fields here were mandatory and set unconditionally from Worker fields, making
the builder pure boilerplate.

### Resolution

`buildConfig()` in `worker.go` already uses a plain `&state.Config{...}` struct literal.
`state/config.go` contains no `NewConfig()` function and no `With*` methods — they were
removed. The `Config` struct itself remains as a meaningful dependency grouping.

### Appendix: Original Proposed Solution

The proposed fix was to replace the builder with a plain struct literal:

```go
func (s *Worker) buildConfig() *state.Config {
    return &state.Config{
        Log:                  s.log,
        Client:               s.Client,
        APIReader:            s.APIReader,
        Clock:                s.Clock,
        Scheme:               s.Scheme,
        Planner:              s.Planner,
        Statuses:             s.Statuses,
        RestoreManager:       s.RestoreManager,
        ControlPlaneEndpoint: s.ControlPlaneEndpoint,
        RunnerImage:          s.RunnerImage,
        RunnerServiceAccount: s.RunnerServiceAccount,
        OnJobMissing:         s.trackConsecutiveJobMiss,
        OnJobFound:           s.resetConsecutiveJobMiss,
    }
}
```

This was implemented. `config.go` was reduced by ~30 lines, improving static tooling
visibility and removing the false impression that `Config` construction is complex.

### Impact

None — applied. No further action needed.

---

## F6: `StateResult` Has Two Fields Mapping to the Same Timer

### Problem Description

`StateResult` declares four timer directives:

```go
type StateResult struct {
    Requeue       bool
    RequeueAfter  time.Duration  // → requeueTimer
    TimeoutAfter  time.Duration  // → deadlineTimer (arm-once: no-op if already running)
    DeadlineAfter time.Duration  // → deadlineTimer (always-override: resets existing timer)
}
```

`TimeoutAfter` and `DeadlineAfter` both target `deadlineTimer` in the Worker with different
arming semantics. The actual usage across all state handlers is:

- `TimeoutAfter`: only `awaitExecutionDrain` (set once; must not reset if the drain window
  is already running)
- `DeadlineAfter`: only `suspendedState` (must always override to reflect updated
  `SuspendUntil` from a refreshed exception)

### Root Cause Analysis

The distinction arises because the Worker does not expose a "set-if-absent" vs "always-set"
API for its `deadlineTimer` — callers communicate intent via which field they set. This leaks
Worker-internal timer management semantics into the public `StateResult` contract.

The subtlety (arm-once vs always-override) requires contributors to understand the Worker's
timer lifecycle when writing a new state handler, making the contract harder to onboard.

### Proposed Solutions

#### Option A: Collapse to a single `DeadlineAfter` field

Since each `StateResult` is freshly constructed per `Handle()` call, the caller trivially
controls arm-vs-override semantics by whether they set the field at all. A caller that
does not want to disturb the existing deadline simply leaves `DeadlineAfter` zero.

Adjust `applyTimers()` accordingly: a non-zero `DeadlineAfter` always overrides; zero means
preserve the existing timer.

- **Pros**: Simpler API; removes the arm-once vs always-override doc burden from StateResult
- **Cons**: `awaitExecutionDrain`'s arm-once requirement must be implemented differently
  (check if timer is already active before returning `DeadlineAfter`)

#### Option B: Rename for clarity; document the contract inline

Keep both fields but rename to make intent self-documenting:
- `SetDeadlineOnce time.Duration` — arm-once (no-op if already running)
- `ResetDeadline time.Duration` — always-override

- **Pros**: No logic change; clearer intent
- **Cons**: Still two fields; contributors writing new handlers must understand which to use

### Impact

Low. The current distinction works correctly; this is an API ergonomics issue that will
compound as more state handlers are added.

---

## F7: `watchable.Map` for 1-Subscriber Fan-Out

### Problem Description

The design uses `watchable.Map` (from `github.com/telepresenceio/watchable`) as the message
bus between the Provider and downstream processors:

- `PlanResources: watchable.Map[NamespacedName, *PlanContext]` → **1 subscriber**: Coordinator
- `ExceptionResources: watchable.Map[NamespacedName, *ExceptionContext]` → **2 subscribers**:
  LifecycleProcessor + deletion monitor

The `watchable` library provides deep-equality change detection on every `Store()` call,
coalescing (latest-wins for a given key), snapshot delivery to late subscribers, and crash
recovery wrappers.

### Root Cause Analysis

RFC-0008 justified `watchable` with the note that a future notification processor would
subscribe to `PlanResources`. That subscriber does not currently exist (see RFC-0006 for
the notification system, which has not been implemented).

For a typical deployment with tens of plans, the fan-out benefit of `watchable` vs a simple
`map[NamespacedName]chan *PlanContext` does not materialize:

- Deep-equality on every `Store()` means every Provider reconcile (`O(plans)` per schedule
  tick) triggers a full struct comparison of the `PlanContext` including all nested fields.
- The coalesce logic (a `conflate.Pipeline` per subscriber key) is already replicated
  explicitly in the Coordinator-to-Worker slot (`plan/coordinator.go`). Fan-out through
  `watchable` adds a second coalescing layer for a single subscriber.
- The external dependency (`github.com/telepresenceio/watchable`) is imported for semantics
  that a `sync.Map` + channel pair could provide for the current use case.

### Proposed Solution

This finding is **low-priority to act on now** — `watchable` is integrated and correct.

However, if RFC-0006 (notification system) remains unimplemented for two or more milestones,
the watchable dependency should be revisited. The Coordinator could own the same equality
check internally, replacing the library with a native channel broadcast pattern.

Document a decision threshold: *if `PlanResources` still has only 1 subscriber by v2.0,
remove `watchable`.*

### Impact

Medium — primarily a complexity and dependency footprint concern rather than a correctness
issue. The deep-equality cost scales linearly with plan count and reconcile frequency.

---

## F8: Duplicate Per-Key Goroutine Pool: Coordinator vs `keyedworker.Pool`

**Status: resolved** _(March 8, 2026 — Coordinator now uses `keyedworker.Pool`)_

### Problem Description

Two independent implementations of the "per-key goroutine pool" pattern existed:

1. **`Coordinator.workers map[types.NamespacedName]*workerEntry`** — managed manually via
   `spawn()`, `despawn()`, `reap()`, and `shutdownAll()`.
2. **`pkg/keyedworker.Pool`** — a generic per-key goroutine pool used by the status processor.

### Resolution

`Coordinator` was refactored to use `keyedworker.New(...)` (Option A). The manual
`spawn`/`despawn`/`reap`/`shutdownAll`/`workers map` logic was eliminated. The current
`coordinator.go` constructs the pool with typed callbacks:

```go
p := keyedworker.New(
    ctx,
    keyedworker.WithSlotFactory(...),
    keyedworker.WithOnSpawnCallback(...),
    keyedworker.WithOnRemoveCallback(...),
    keyedworker.WithLogger(log),
)
```

The idle-reap callback requirement that was listed as a `Cons` for Option A was satisfied
via `WithOnSpawnCallback` / `WithOnRemoveCallback` hooks on the pool.

### Appendix: Rejected Alternative

Option B (accept DRY violation, document it) was not taken — consolidation was preferable
given the pool API already supported the required callbacks.

### Impact

None — applied. ~80 lines of manual lifecycle management removed from `coordinator.go`;
single `keyedworker.Pool` implementation maintained.

---

## F9: Recursive `handle()` on `StateResult.Requeue` — Unbounded Stack Depth

**Status: resolved** _(March 8, 2026 — max-depth guard implemented, Option A)_

### Problem Description

When a phase handler returns `StateResult{Requeue: true}`, the original `Worker.handle()`
called itself synchronously without a depth bound. A handler bug returning `Requeue: true`
without advancing `Phase` would cause unbounded goroutine stack growth.

### Resolution

Option A (max-depth guard) was implemented in `worker.go`:

```go
const maxHandleDepth = 5

func (s *Worker) handle(ctx context.Context, planCtx *message.PlanContext, onDeadline bool) {
    s.handleWithDepth(ctx, planCtx, onDeadline, 0)
}

func (s *Worker) handleWithDepth(ctx context.Context, planCtx *message.PlanContext, onDeadline bool, depth int) {
    if depth >= maxHandleDepth {
        s.log.Error(nil, "handle() recursion depth exceeded; possible phase loop",
            "plan", s.key, "phase", planCtx.Plan.Status.Phase)
        return
    }
    // ...
    if result.Requeue {
        s.stopRequeueTimer()
        s.stopDeadlineTimer()
        s.handleWithDepth(ctx, planCtx, false, depth+1)
        return
    }
}
```

For correct handlers (all current state handlers return `Requeue: true` only on confirmed
phase transitions) there is no behaviour change. A buggy handler will log an error and
leave the plan in a partially-transitioned state rather than causing a stack overflow.

### Appendix: Rejected Alternative

Option B (convert to a loop) was considered but not taken — the depth guard adds a single
parameter to an internal function and is easier to reason about than a re-create loop.

### Impact

None — applied. Stack overflow risk eliminated for Worker goroutines.

---

## Root Cause Analysis (Cross-cutting)

Three patterns appear repeatedly across the findings:

1. **RFC promises not matched by code** (F1, F2-partial): The RFC's "Issues & Resolutions"
   table does not reflect the current branch state. `reconcileTruth()` is marked "Fixed" but
   absent from the codebase. This creates a false sense of coverage and obscures real gaps.

2. **Abstractions designed for future use cases** (F5, F6, F7): The `Config` builder, the
   dual-field `StateResult`, and `watchable.Map` were each designed with future extensibility
   in mind. In their current form they add surface area and cognitive overhead ahead of the
   demand. This is classic premature optimization applied to API design.

3. **Parallel implementations not consolidated** (~~F3~~, ~~F8~~): The `Worker.Resources` field
   was originally noted as dead code, but investigation confirmed it is actively used in
   `state_lifecycle.go` for plan deletion cleanup (finding was stale). The duplicate per-key
   goroutine pool has been eliminated — `Coordinator` now uses `keyedworker.Pool`.

## Recommended Priority Order

| Priority | Finding | Action |
|----------|---------|--------|
| P1 | ~~F1: `reconcileTruth()` missing~~ | ✅ Acked — reconciler is the source of truth; stale comment removed |
| P2 | ~~F4: `recoveryState` operation choice~~ | ✅ Resolved — operation derived from `CurrentOperation`; schedule-conflict logged |
| P3 | ~~F9: Recursive `handle()`~~ | ✅ Resolved — `maxHandleDepth = 5` guard + `handleWithDepth()` implemented |
| P4 | ~~F2: Double Job fetch~~ | ✅ Resolved — `ExecutionProgress` removed; `DeliveryNonce int64` ensures re-delivery on every provider reconcile |
| P5 | ~~F3: Dead `Resources` field~~ | ✅ Resolved — field is used in `state_lifecycle.go` for plan deletion cleanup; finding was stale |
| P6 | ~~F5: `Config` builder~~ | ✅ Resolved — `buildConfig()` already uses plain struct literal; builder methods removed |
| P7 | ~~F8: Duplicate pool~~ | ✅ Resolved — Coordinator now uses `keyedworker.Pool` |
| P8 | F6: `StateResult` timer fields | Defer until a third callsite appears |
| P9 | F7: `watchable` fan-out | Revisit if notification processor not shipped by v2.0 |

## Impact Summary

- **Correctness risk**: ~~F1~~ (acked, bounded by reconcile loop), ~~F4~~ (resolved, `CurrentOperation` used)
- **Safety risk**: ~~F9~~ (resolved, depth guard in place)
- **I/O efficiency**: ~~F2~~ (resolved, no Job list in provider; `DeliveryNonce` ensures fast event-driven Worker reaction), ~~F4~~ (resolved)
- **Maintainability**: ~~F3~~ (resolved, field is used), ~~F5~~ (resolved, builder removed), ~~F8~~ (resolved, Coordinator uses keyedworker.Pool)
- **API ergonomics**: F6, F7 (overspecified contracts)
