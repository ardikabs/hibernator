---
date: March 19, 2026
updated: March 20, 2026
status: investigated
component: internal/provider (controller core)
---

# Controller Core Design Review

## Summary

This document provides a thorough review of the Hibernator Operator's async
phase-driven reconciler architecture, examining the core design patterns, their
trade-offs relative to standard `controller-runtime` idioms, and flagging
potential bugs, misunderstandings, or maintenance risks for the future.

**Scope:** `internal/provider/`, `internal/message/`, `pkg/keyedworker/`,
and their interactions.

---

## 1. Architecture Overview

The controller uses a **three-layer async pipeline** adapted from Envoy Gateway's
watchable-map subscription pattern:

```
K8s Watch Events
    ↓
[PlanReconciler] — data collector, never requeues
    ↓
watchable.Map[PlanContext] — pub/sub bus
    ↓
    ├── [Coordinator → per-plan Workers] — phase state machine
    ├── [PlanRequeueProcessor]           — time-based re-enqueuing
    └── [ExceptionLifecycleProcessor]    — exception state machine
    ↓
[Status UpdateProcessor] — per-key FIFO async writer → K8s API
```

This is a significant departure from the standard `controller-runtime` pattern
(single reconcile function, shared work queue, `ctrl.Result{RequeueAfter}`).
The design's strengths and risks are evaluated below.

---

## 2. Design Strengths

### 2.1 Per-Plan Isolation
Each `HibernatePlan` gets its own `Worker` goroutine with local state, timers,
and a latest-wins slot. This eliminates:
- Head-of-line blocking: a slow plan doesn't block others.
- Race conditions between plans sharing global mutable state.

### 2.2 Optimistic In-Memory Status
`mergeIncoming()` preserves the worker's local status mutations over stale
informer deliveries, allowing rapid phase transitions without waiting for the
API server round-trip. This is a deliberate trade-off for responsiveness.

### 2.3 Predicate Design
The predicate composition on `HibernatePlan` is well-considered:
```go
predicate.Or(
    predicate.GenerationChangedPredicate{},  // Spec changes
    predicate.AnnotationChangedPredicate{},  // retry-now, override-action
)
```
This correctly suppresses status-write → reconcile loops while preserving
annotation-driven state machine triggers. The `ScheduleException` watch using
only `GenerationChangedPredicate` is also correct since exception status writes
should not re-trigger the plan reconciler.

### 2.4 Status Writer Architecture
The `UpdateProcessor` with per-key FIFO ordering, `RetryOnConflict`, fresh
`APIReader.Get()` before mutation, and no-op detection (`isStatusEqual`) is
solid. Decoupling status writes from the hot path respects the Kubernetes API
server and avoids retry storms.

### 2.5 Crash Recovery in Subscription Handlers
`handleWithCrashRecovery()` wrapping around subscription handlers with panic
recovery and metrics is a good defensive practice for long-running goroutines.

---

## 3. Findings — Potential Bugs

### 3.1 [HIGH] Timer Goroutine Leak in PlanRequeueProcessor

**File:** `internal/provider/processor/requeue/processor.go:97-105`

```go
t := p.Clock.NewTimer(d)
timers[key] = t
go func() {
    select {
    case <-ctx.Done():
        _ = t.Stop()
    case <-t.C():
        log.V(1).Info("requeue timer fired", "plan", key)
        p.Enqueuer.Enqueue(key)
    }
}()
```

**Problem:** When a plan update arrives before the timer fires, the old timer is
stopped (`_ = t.Stop()`) and removed from the map, but the goroutine launched
by the *previous* iteration is still blocked in `select`. `t.Stop()` does NOT
close `t.C()`, so the goroutine remains alive until `ctx.Done()` fires (i.e.,
controller shutdown).

Over time, with frequent plan updates, this accumulates orphaned goroutines —
one per replaced timer. For 100 plans updating every 30 seconds over 24 hours,
that's ~288,000 leaked goroutines.

**Fix options:**
1. Use a per-key `context.CancelFunc` alongside the timer. Cancel the old
   goroutine's context before stopping the timer:
   ```go
   type timerEntry struct {
       timer  clock.Timer
       cancel context.CancelFunc
   }
   ```
2. Drain the timer channel after `Stop()` returns true, OR use a single
   long-lived goroutine per plan that resets its timer on update instead of
   spawning new goroutines.

**Severity:** High — this is a memory/goroutine leak that scales linearly with
plan count and update frequency. Will manifest in production under sustained
operation.

### 3.2 [HIGH] channelEnqueuer.Enqueue() Can Block Indefinitely

**File:** `internal/provider/setup.go:81, 206-213`

```go
enqueueCh := make(chan event.GenericEvent, 128)

func (e *channelEnqueuer) Enqueue(key types.NamespacedName) {
    e.ch <- event.GenericEvent{...} // blocking send
}
```

**Problem:** `Enqueue()` performs a blocking channel send. If the channel buffer
(cap 128) is full — because reconciler workers are slow or the controller-runtime
work queue is backed up — the calling goroutine (PlanRequeueProcessor's
subscription handler) blocks. Since `HandleSubscription` processes updates
serially, this blocks ALL plan requeue processing, causing a system-wide stall.

**Fix:** Use a non-blocking send with `select/default`:
```go
func (e *channelEnqueuer) Enqueue(key types.NamespacedName) {
    select {
    case e.ch <- event.GenericEvent{...}:
    default:
        // Channel full — the plan will be reconciled on the next natural trigger.
        // Log at V(1) for diagnostics.
    }
}
```

**Severity:** High — under load, this can deadlock the requeue processor,
preventing ALL time-based schedule transitions across all plans.

### 3.3 [MEDIUM] Optimistic Status Can Diverge Permanently on StatusWriter Failure — **OUTSTANDING (P2)**

**File:** `internal/provider/processor/plan/worker.go:291-307` (`mergeIncoming`)

The `mergeIncoming()` pattern always carries forward the worker's in-memory
status over the informer delivery:

```go
status := s.cachedCtx.Plan.Status
s.cachedCtx = incoming
s.cachedCtx.Plan.Status = status
```

The design assumes that the StatusWriter will eventually persist the optimistic
status, at which point the next informer delivery carries the correct state.
However, if the StatusWriter permanently fails for a plan (e.g., admission
webhook rejection, quota exceeded, persistent conflict), the worker's in-memory
status diverges from the API server **forever**. There is no reconciliation
mechanism to detect or correct this divergence.

**Scenario:**
1. Worker transitions plan to `PhaseHibernating` (optimistic in-memory).
2. StatusWriter fails to persist (e.g., 422 validation error).
3. Every subsequent informer delivery has `PhaseActive` from the API server.
4. Worker ignores it and keeps seeing `PhaseHibernating` in-memory.
5. Plan is stuck in a phantom phase that doesn't match the API server.

**Proposed fix — Generation-based staleness fallback (not yet implemented):**

1. **Add `lastStatusWriteGen int64` to `Worker`.** The StatusWriter signals a
   successful write via a `PostHook` that atomically stores the plan's
   `Generation` at the time of the write:
   ```go
   PostHook: func(_ context.Context, obj *HibernatePlan) error {
       atomic.StoreInt64(&s.lastStatusWriteGen, obj.Generation)
       return nil
   }
   ```

2. **Divergence detection in `mergeIncoming()`.** Before carrying forward the
   optimistic status, compare the incoming `Generation` against the last
   successfully written generation. If it has advanced by more than
   `maxStaleGenerations` (suggested: 3) without a successful write, abandon
   the optimistic status and adopt the informer's snapshot:
   ```go
   gen := atomic.LoadInt64(&s.lastStatusWriteGen)
   if gen > 0 && incoming.Plan.Generation-gen > maxStaleGenerations {
       s.log.Error(nil, "status divergence detected, resetting to informer state",
           "plan", s.key, "lastWriteGen", gen, "incomingGen", incoming.Plan.Generation)
       s.cachedCtx = incoming
       return
   }
   ```

3. **Add a metric counter** for divergence-detected events so operators can alert.

This bounds the divergence window to at most 3 spec-change generations without
requiring additional API calls. Cold-start safety is preserved: `lastStatusWriteGen`
starts at 0, and the `gen > 0` guard keeps the existing first-delivery behaviour.

**Severity:** Medium — requires a specific failure mode (persistent write
failure) but is undetectable and unrecoverable without operator restart.

### 3.4 [MEDIUM] ExceptionReferences Updated from Stale Exception State — **NOT A BUG**

**File:** `internal/provider/processor/scheduleexception/lifecycle.go:118-160`
(`updateExceptionReferences`)

`updateExceptionReferences()` builds `ExceptionReference` entries from
`planCtx.Exceptions`, reading `exc.Status.State`. But `ExceptionLifecycleProcessor`
modifies exception status via async `Send()` — the actual API write happens later
in the StatusWriter pipeline. This means:

1. Exception transitions from Pending → Active (queued via `Send()`).
2. `updateExceptionReferences()` runs in the same `handlePlanUpdate()` call.
3. It reads `exc.Status.State` which is still `Pending` (the `Send()` mutator
   applied to the in-memory object but the exc was fetched from informer cache).

Wait — looking more carefully at `defaultUpdater.Send()`:
```go
func (u *defaultUpdater[T]) Send(update Update[T]) {
    if update.Mutator != nil {
        update.Mutator.Mutate(update.Resource) // applies in-place
    }
    u.pool.Deliver(update.NamespacedName, update)
}
```

The `Send()` applies the mutator in-place to `update.Resource`, which is the
same pointer as the exception in `planCtx.Exceptions`. So the in-memory mutation
IS visible to `updateExceptionReferences()` if it runs after the `Send()` call.

**However**, the ordering depends on the loop iteration order in
`handlePlanUpdate()`:
```go
func (p *LifecycleProcessor) handlePlanUpdate(...) {
    for i := range planCtx.Exceptions {
        // processes each exception (may call transitionState → Send)
    }
    // Then updates references
    p.updateExceptionReferences(...)
}
```

This IS correctly ordered — exceptions are processed first, then references are
built. The in-place mutation from `Send()` means the references pick up the
new state.

**Residual risk:** If a *different* delivery arrives between the exception
processing and reference update (unlikely since `HandleSubscription` is serial),
the state could be inconsistent. Current serial processing mitigates this.

**Verdict:** Not a bug. The in-place mutation via `Send()` is a deliberate design
feature that makes the new state immediately visible within the same call chain.
The loop-before-references ordering is correct and enforced by serial execution.
No fix is needed.

**Severity:** Low (mitigated by serial execution, but worth documenting).

### 3.5 [MEDIUM] `isStatusEqual` Ignores DetachedAt — Silent No-Op on Detached Transition — **FIXED**

**File:** `internal/provider/processor/status/processor.go:251-280`

```go
case *hibernatorv1alpha1.ScheduleException:
    if b, ok := objB.(*hibernatorv1alpha1.ScheduleException); ok {
        return cmp.Equal(a.Status, b.Status,
            cmpopts.IgnoreFields(hibernatorv1alpha1.ScheduleExceptionStatus{},
                "AppliedAt", "ExpiredAt"),
        )
    }
```

`isStatusEqual` ignores `AppliedAt` and `ExpiredAt` for no-op detection, but
does NOT ignore `DetachedAt`. This is inconsistent. If the spec should be that
timestamp fields are cosmetic (not semantically significant), `DetachedAt` should
also be ignored. If timestamps ARE significant, then `AppliedAt`/`ExpiredAt`
should not be ignored either.

**Current behavior:** A status write that ONLY changes `DetachedAt` (without
changing `State` or `Message`) would be detected as a real change and written.
A write that ONLY changes `AppliedAt` would be suppressed as a no-op. This
inconsistency could cause confusion.

**Fix:** Either:
- Add `DetachedAt` to the ignored fields list (consistent with other timestamps).
- Remove all timestamp ignores and let the no-op guard be purely structural.

The former is more aligned with the current intent (timestamps are side-effects
of state transitions, the state field itself drives the transition).

**Resolution:** The `isStatusEqual` function was refactored to a unified
`cmpopts.IgnoreMapEntries` approach that ignores only generic condition-level
timestamps (`lastTransitionTime`, `lastRetryTime`) across the entire status
tree. The individual timestamp fields (`AppliedAt`, `ExpiredAt`, `DetachedAt`)
are no longer individually excepted — all three are now compared equally. The
inconsistency is gone.

**Severity:** Medium — won't cause bugs today (state + timestamp always change
together), but a future refactor could hit this inconsistency.

---

## 4. Findings — Design Risks and Future Misunderstandings

### 4.1 [HIGH] No Test Coverage for PlanRequeueProcessor

There is no `processor_test.go` file for the requeue processor. This is the
component responsible for ALL time-based re-enqueuing in the system. Given that:
- Timer goroutine leaks (Finding 3.1) would be caught by tests.
- `computeBoundary()` logic is critical and was recently refactored.
- The interaction between `clock.Timer`, goroutines, and the `HandleSubscription`
  serial execution model is subtle.

**Recommendation:** Add unit tests covering:
- Timer replacement (old timer cancelled, new one armed).
- Goroutine cleanup on context cancellation.
- `computeBoundary()` with various exception combinations.
- Immediate enqueue when boundary is in the past.
- Plan deletion cleans up timer.

### 4.2 [MEDIUM] Worker Uses `time.NewTimer` While Requeue Uses `clock.NewTimer`

**File:** `internal/provider/processor/plan/worker.go:105`
vs `internal/provider/processor/requeue/processor.go:100`

The `Worker` uses `time.NewTimer` for `requeueTimer` and `deadlineTimer`:
```go
s.requeueTimer = time.NewTimer(d)  // wall clock!
```

But `PlanRequeueProcessor` correctly uses `p.Clock.NewTimer(d)` (fake-clock
compatible). This inconsistency means:
- The Worker's timers are NOT controllable via fake clock in E2E tests.
- Phase execution polling (job checking) and deadline timeouts cannot be
  accelerated in tests, creating test brittleness or forcing real-time waits.

**Recommendation:** Replace all `time.NewTimer` in `worker.go` with
`s.Clock.NewTimer(d)` for consistent testability.

### 4.3 [MEDIUM] handlePlanDelete Lists Exceptions Using Cached Client

**File:** `internal/provider/processor/scheduleexception/lifecycle.go:179-182`

```go
// Use the cached client (not APIReader) because field indexes only work with the cache.
if err := p.List(ctx, &exceptionList,
    client.InNamespace(planKey.Namespace),
    client.MatchingFields{wellknown.FieldIndexExceptionPlanRef: planKey.Name},
); err != nil {
```

The comment correctly explains why the cached client is used. However, when a
plan is deleted, the exception lifecycle processor *also* transitions non-owned
exceptions to Detached. This is triggered by `update.Delete` in the watchable
subscription — meaning the plan has already been removed from the informer cache.

**Risk:** If the informer cache has lag (which is normal), the `List()` may
return stale results and miss exceptions that were very recently created. In
the worst case, a brand-new exception that was created moments before plan
deletion could be missed and left in a non-Detached state with a finalizer
pointing to a non-existent plan.

**Mitigation:** The current design is acceptable because:
- The exception's finalizer prevents deletion.
- A future reconcile (when the informer catches up) would detect the orphan.

But there's no such future reconcile — the plan is gone, so nothing triggers
the exception lifecycle processor for that plan again. The exception would be
stuck with a finalizer until someone manually intervenes.

**Fix consideration:** After the Detached transition loop, schedule a delayed
re-check (e.g., via a timer or by re-listing after a short delay) to catch
any exceptions that were missed due to cache lag.

### 4.4 [LOW] `coalesceUpdates` Reverses Order

**File:** `internal/message/watchutil.go:106-122`

`coalesceUpdates` iterates backwards to find the last update per key, then
reverses the result to "maintain original order." This is correct for preserving
key ordering but subtly means: if key A was updated at T1 and T3, and key B at
T2, the coalesced output is [A@T3, B@T2] — A before B, even though B's last
update was chronologically between A's two updates. This is fine for the current
use case (each key is processed independently), but could cause confusion if
someone assumes chronological ordering across keys.

### 4.5 [LOW] `maxHandleDepth` May Silently Drop Phase Transitions

**File:** `internal/provider/processor/plan/worker.go:40, 181-187`

```go
const maxHandleDepth = 5

if depth >= maxHandleDepth {
    s.log.Error(nil, "handle() recursion depth exceeded; possible phase loop",
        "plan", s.key,
        "phase", phaseBefore)
    return
}
```

When `maxHandleDepth` is exceeded, the handler silently returns without
processing. The error is logged but no recovery action is taken. If a
legitimate phase transition chain requires >5 steps (unlikely but possible
with complex annotation-driven overrides), it would be silently dropped.

Consider adding a metric counter for depth-exceeded events so operators can
alert on it.

### 4.6 [LOW] DeliveryNonce Monotonicity Not Guaranteed Across Leader Failovers

**File:** `internal/provider/provider.go:68`

```go
deliveryNonce atomic.Int64
```

The `deliveryNonce` starts at 0 on each controller startup. After a leader
failover, the new leader's nonce restarts at 0. Since nonce is only used to
force `watchable.Map.Store()` to detect changes (not for ordering), this is
functionally harmless. However, it's worth documenting that nonce values are
NOT globally monotonic and should never be used for ordering or deduplication
across restarts.

---

## 5. Findings — Architectural Observations

### 5.1 Departure from controller-runtime Idioms

The async pipeline is a significant departure from standard controller-runtime
patterns. The standard approach uses:
- A single `Reconcile()` function per controller.
- `ctrl.Result{RequeueAfter}` for time-based re-enqueuing.
- `MaxConcurrentReconciles` for bounded concurrency.
- The work queue for deduplication and rate limiting.

This operator replaces all of those with:
- watchable.Map pub/sub for inter-component communication.
- Per-plan goroutines for unbounded (per-key) concurrency.
- Manual timer management for time-based triggers.
- A custom key-worker pool for status writing.

**Trade-offs:**
| Aspect | Standard Pattern | This Design |
|---|---|---|
| Per-plan isolation | No (shared workers) | Yes (dedicated goroutine) |
| Status write latency | Synchronous in reconcile | Async, decoupled |
| Timer management | Built-in via work queue | Manual per-plan timers |
| Goroutine count | Fixed (`MaxConcurrentReconciles`) | Unbounded (one per plan) |
| Debuggability | Standard (well-documented) | Custom (learning curve) |
| Rate limiting | Built-in exponential backoff | Manual (none on requeue) |
| Deduplication | Automatic in work queue | Manual (latest-wins slot) |

**Recommendation:** The design is justified for the hibernation use case (long-lived
per-plan state, multiple timers per plan, optimistic status). However, the
goroutine-per-plan model means that at scale (1000+ plans), the operator will run
1000+ goroutines plus timer goroutines. Document the expected scale ceiling and
consider adding a soft cap or pool sizing limit.

### 5.2 Exception Lifecycle Driven by Plan Subscription (Not Exception Subscription)

The `ExceptionLifecycleProcessor` subscribes to `PlanResources`, not a separate
exception watchable map. This is a deliberate choice to ensure exception lifecycle
transitions are triggered by plan reconciles (which recompute the full context).

**Consequence:** Exception state transitions only happen when the plan is
reconciled. If the PlanRequeueProcessor fails (Finding 3.2 blocking), exception
transitions stall too. There is no independent timer for exception lifecycle.

This is acceptable as long as the requeue processor is reliable, but it creates
a single point of failure for all time-based state transitions.

### 5.3 Multiple Subscribers on Same watchable.Map

`PlanResources` has three subscribers:
1. Coordinator (plan execution)
2. PlanRequeueProcessor (timer management)
3. ExceptionLifecycleProcessor (exception state machine)

All three process updates in their own `HandleSubscription` loops. If one
subscriber is slow, it doesn't block the others (each has its own channel).
However, snapshot coalescing means a slow subscriber may skip intermediate
states. This is documented and correct for the current design.

---

## 6. Recommendations Summary

> Last reviewed: March 20, 2026. ✅ = Fixed · ⚠️ = Outstanding · 📝 = Not a Bug · 🔵 = Accepted

| Priority | Finding | Status | Resolution |
|---|---|---|---|
| **P0** | 3.1 Timer goroutine leak | ✅ Fixed | `timerEntry{timer, cancel}` — goroutine exits on `timerCtx.Done()` |
| **P0** | 3.2 Blocking Enqueue() | ✅ Fixed | Non-blocking `select/default`; V(4) log + `EnqueueDropTotal` metric on drop |
| **P1** | 4.1 No requeue processor tests | ✅ Fixed | 15 test functions covering `computeBoundary` + timer lifecycle |
| **P1** | 4.2 Worker uses wall-clock timers | ✅ Fixed | All `Clock.NewTimer` — fake-clock compatible |
| **P1** | 4.3 Orphaned exception on plan delete | 🔵 Accepted | Cache lag window is extremely narrow; finalizer prevents premature GC |
| **P2** | 3.3 Optimistic status divergence | ⚠️ **OUTSTANDING** | Proper plan documented in §3.3: generation-based staleness + PostHook signal |
| **P2** | 3.5 isStatusEqual inconsistency | ✅ Fixed | Unified `cmpopts.IgnoreMapEntries`; all timestamp fields treated equally |
| **P3** | 3.4 ExceptionReferences stale state | 📝 Not a Bug | `Send()` mutates in-place before `updateExceptionReferences()` — correct by design |
| **P3** | 4.4 coalesceUpdates order | 🔵 Accepted | Per-key independence; cross-key ordering is not required |
| **P3** | 4.5 maxHandleDepth silent drop | 🔵 Accepted | Error is logged; metric is optional future work |
| **P3** | 4.6 DeliveryNonce monotonicity | 🔵 Accepted | Redesigned as `DependencyNonces`; post-restart restart is harmless |
| **P3** | New: nil-guard on Job owner | ✅ Fixed | `owner == nil` early-return guard added in `onJobTerminalUpdate` |
| **P3** | New: no enqueue drop visibility | ✅ Fixed | `hibernator_enqueue_drop_total` counter added to `channelEnqueuer` |

---

## Appendix A: File Reference

| File | Role |
|---|---|
| `internal/provider/provider.go` | PlanReconciler — data collector, never requeues |
| `internal/provider/setup.go` | Pipeline wiring — providers, processors, status writers |
| `internal/provider/processor/plan/coordinator.go` | Per-plan worker goroutine lifecycle |
| `internal/provider/processor/plan/worker.go` | Worker event loop, optimistic status, timer management |
| `internal/provider/processor/plan/state/state.go` | Handler interface, state factory, error classification |
| `internal/provider/processor/plan/state/state_selection.go` | Phase → handler dispatch |
| `internal/provider/processor/plan/state/state_idle.go` | Active/Hibernated phase handler |
| `internal/provider/processor/requeue/processor.go` | Time-based re-enqueuing via clock.Timer |
| `internal/provider/processor/scheduleexception/lifecycle.go` | Exception state machine |
| `internal/provider/processor/status/processor.go` | Async status writer with RetryOnConflict |
| `internal/provider/processor/status/updater.go` | Updater interface (Send) |
| `internal/message/types.go` | PlanContext, ScheduleEvaluation |
| `internal/message/watchutil.go` | HandleSubscription, coalesceUpdates |
| `internal/message/enqueuer.go` | PlanEnqueuer interface |
| `pkg/keyedworker/pool.go` | Per-key goroutine pool with lazy spawn/idle reap |

## Appendix B: Research References

- controller-runtime work queue design: `kubernetes-sigs/controller-runtime`
  controller.go — `MaxConcurrentReconciles`, `RateLimiter`, deduplication.
- Envoy Gateway watchable.Map: `envoyproxy/gateway` — publisher/subscriber
  pattern, deep-copy requirement, shutdown data races.
- Optimistic caching risks: stale data after leader failover, divergence when
  StatusWriter fails, thundering herd on cache rebuild.
