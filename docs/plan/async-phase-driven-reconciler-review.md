> **⚠️ ARCHIVED** — This document has been superseded by [RFC-0008](../../docs/proposals/0008-async-phase-driven-reconciler.md). Preserved for historical reference only.
>
> **Retractions (two items listed as ✅ Fixed were subsequently reverted in later refactoring):**
> - **H3 / PR2**: `reconcileTruth()` was designed and documented here as fixed, but was **never implemented** in the codebase. The divergence-correction path is absent. See [docs/findings/async-reconciler-review.md F1](../findings/async-reconciler-review.md) for analysis.
> - **M5**: The `cachedConfig` optimisation was **reverted**. `buildConfig()` intentionally constructs a fresh `Config` on every `handle()` call to keep handlers fully stateless with respect to the Worker.

# Async Phase-Driven Reconciler — Code Review

**Date**: 2026-03-04
**Branch**: `feat/async-reconciler`
**Scope**: ~5,300 new lines across 29 files
**Status**: WIP (single commit: `ffa2f03`)
**Last Fix Pass**: 2026-03-04
**Readiness Review**: 2026-03-04

---

## Table of Contents

- [Overall Assessment](#overall-assessment)
- [What's Done Well](#whats-done-well)
- [Issues & Concerns](#issues--concerns)
  - [Critical (Must Fix)](#critical-must-fix)
  - [High (Should Fix)](#high-should-fix)
  - [Medium (Consider)](#medium-consider)
  - [Low / Nits](#low--nits)
- [Post-Review Fixes](#post-review-fixes)
- [Fix Log](#fix-log)
- [Readiness Assessment](#readiness-assessment)
- [Summary Table](#summary-table)
- [Verdict](#verdict)

---

## Overall Assessment

The architecture is a thoughtful evolution from the monolithic reconciler. The Envoy Gateway-inspired `watchable.Map` pipeline with per-plan Worker goroutines is a legitimate pattern for this domain. The code compiles cleanly and the existing tests pass.

The Coordinator + Worker (Actor) model is a better choice than the design doc's original processor-per-phase with `SubscribeSubset` — it eliminates the sequential `HandleSubscription` bottleneck and makes the state machine natural, since each plan's lifecycle runs in a single goroutine with no phase-routing concurrency hazards.

---

## What's Done Well

### 1. Coordinator + Worker Actor Model

The design doc described processor-per-phase with `SubscribeSubset`, but the implementation wisely consolidated into a Coordinator that spawns per-plan Workers. This avoids the sequential `HandleSubscription` bottleneck and makes the state machine natural — each plan's lifecycle runs in a single goroutine with no phase-routing concurrency hazards.

### 2. `planContextSlot` (Latest-Wins Slot)

The non-blocking single-value channel pattern avoids queue buildup. The mutex + separate signal channel is correct for the "latest overwrites" semantic.

**Reference**: `internal/provider/processor/plan/coordinator.go`, lines 38–63.

### 3. `StatusQueue` with Drop-on-Full

Combined with the status writer's `isPlanStatusEqual` guard and `RetryOnConflict`, dropped updates are genuinely safe. The `StatusQueueDroppedTotal` metric provides visibility into overflow conditions.

**Reference**: `internal/message/status_queue.go`.

### 4. Feature Flag Isolation

The `--legacy-reconciler` flag with clean branching in `cmd/controller/app/app.go` means zero risk to the existing stable code path. The `internal/controller/` package is completely untouched.

### 5. Provider Predicates

The `GenerationChangedPredicate | AnnotationChangedPredicate` combo in `PlanReconciler.SetupWithManager()` correctly breaks the status-write feedback loop. The `configMapDataChangedPredicate` filtering annotation-only writes on restore ConfigMaps prevents spurious reconciles during wakeup.

**Reference**: `internal/provider/provider_hibernateplan.go`, lines 259–297.

### 6. State Handlers as Composition Over Inheritance

The `Config` struct with closure-based timer control (`ResetPollTimer`, `ScheduleRetry`, etc.) cleanly separates the Worker's internal state from the Handler's logic. Handlers never know about Worker internals.

**Reference**: `internal/provider/processor/plan/state/state.go`, lines 95–140.

### 7. `isPlanStatusEqual` Using `cmp.Equal`

Using `cmp.Equal` with `cmpopts.IgnoreFields` is superior to hand-rolled comparison — it's readable, maintainable, and explicitly documents which fields are semantic vs. bookkeeping.

**Reference**: `internal/provider/processor/status/writer.go`, lines 176–195.

---

## Issues & Concerns

### Critical (Must Fix)

#### C1. `ScheduleExceptionProcessor.removeFromPlanStatus` Bypasses the Status Writer

> **Status**: ✅ Fixed

The exception processor directly calls `p.Status().Update(ctx, plan)` to mutate HibernatePlan status during exception deletion cleanup. This violates the core architectural invariant: **only the status writer should write status sub-resources**.

This creates potential conflict races with the status writer and bypasses the `isPlanStatusEqual` guard.

**File**: `internal/provider/processor/scheduleexception/lifecycle.go`, `removeFromPlanStatus()`.

**Fix Applied**: Rewrote `removeFromPlanStatus` to queue a `PlanStatusUpdate` through `p.Statuses.PlanStatuses.Send()` with a mutation closure that filters `ActiveExceptions` by removing the deleted exception name. No longer writes status directly.

---

#### C2. No Job Watches in the Provider

The legacy controller watches Jobs via `Owns(&batchv1.Job{})` which triggers immediate reconciliation when Jobs complete. The new `PlanReconciler` does **not** watch Jobs. It relies entirely on the Worker's `pollTimer` (5s interval) to re-fetch Jobs. This means:

- Job completion has up to 5s lag before detection.
- If the poll timer doesn't fire (e.g., worker is processing a long handler), the lag compounds.
- The design doc explicitly says the provider should watch Jobs.

**File**: `internal/provider/provider_hibernateplan.go`, `SetupWithManager()`.

**Fix**: Add `.Owns(&batchv1.Job{})` to the provider's `SetupWithManager`. This gives immediate K8s event-driven updates with the poll timer as a safety net.

---

#### C3. `handleDelete` Receives a Potentially Stale Exception

> **Status**: ✅ Fixed

When the watchable `Delete` event fires, `update.Value` is the **last known** value, not a fresh fetch. In the lifecycle processor:

```go
if update.Delete {
    p.handleDelete(ctx, log, update.Key, update.Value, errChan)
    return
}
```

`handleDelete` then calls `removeFromPlanStatus` and patches the finalizer on the stale object. This can cause `409 Conflict` errors (handled by `RetryOnConflict` for the plan status, but the finalizer patch has no retry).

**File**: `internal/provider/processor/scheduleexception/lifecycle.go`, lines 69–74.

**Fix Applied**: Rewrote `handleDelete` to re-fetch the exception from the API server via `p.APIReader.Get()` at entry. Finalizer patch is now wrapped in `retry.RetryOnConflict` with a fresh re-fetch inside each retry iteration.

---

### High (Should Fix)

#### H1. Worker Goroutines Are Never Reaped for Idle Plans

> **Status**: ✅ Fixed

Once spawned, a Worker lives until the plan is deleted from the watchable map (`despawn`). If you have 1000 `HibernatePlan` resources, you have 1000 long-lived goroutines, most of which are idle 99% of the time (blocked on `select`). While Go goroutines are lightweight, the pattern doesn't scale well.

**File**: `internal/provider/processor/plan/coordinator.go`, `internal/provider/processor/plan/worker.go`.

**Fix Applied**: Added `workerIdleTimeout` (30m) constant and a fifth `idleTimer` case to the Worker's `run()` select loop. When the idle timer fires, the Worker calls `onIdleReap()` and returns. The Coordinator's `reap()` method handles cleanup with `WorkerGoroutinesGauge` decrement. Timer is reset on every meaningful event.

---

#### H2. Potential Hot Loop from ResourceVersion-Based Equality

> **Status**: ✅ Not a Bug (Verified)

`PlanContext.Equal` correctly excludes `RequeueAfter` from equality. But `planEqual` includes `ResourceVersion`, which changes on every K8s write. The concern was that status writes could create a hot loop:

1. Provider reconcile (via K8s event).
2. Provider stores new `PlanContext` with new `ResourceVersion`.
3. Watchable detects inequality → delivers to Coordinator → Worker handles.

**Verification**: The `planPredicate` (`GenerationChangedPredicate | AnnotationChangedPredicate`) filters status-only updates at the informer level. Status sub-resource writes via `Status().Update()` do **not** bump `Generation`, so `GenerationChangedPredicate` filters them out. They also don't change annotations, so `AnnotationChangedPredicate` filters them too. The only time the provider re-reconciles on a status-adjacent write is when a state handler patches annotations (suspend, retry-now, retry-at, suspended-at-phase) — which is **correct and intentional** behavior.

`ResourceVersion` in `planEqual` is still useful: it ensures that any genuine K8s-side change (e.g., user edits spec) that reaches the provider is always delivered through watchable even if the field-level diff is subtle.

**File**: `internal/message/types.go`, `planEqual()`.

---

#### H3. No Divergence Detection Between Optimistic and Persisted State

> **Status**: ✅ Fixed

State handlers directly mutate `plan.Status.*` (optimistic updates) AND send status queue updates:

```go
mutate(&plan.Status)                      // local optimistic
state.Statuses.PlanStatuses.Send(...)     // remote persistent
```

The local in-memory state and the K8s-persisted state can diverge if the status write fails or is dropped. While the design doc acknowledges this (eventual consistency), there's no reconciliation mechanism to detect divergence. If the status queue drops an update and the poll timer re-drives with cached state, the worker proceeds based on a phase that was never persisted.

**File**: `internal/provider/processor/plan/worker.go`.

**Fix Applied**: Added `reconcileTruth()` method to Worker. Every 5th poll-timer cycle (`truthCheckInterval`), the Worker fetches the plan from `APIReader` and compares persisted phase with cached phase. On divergence it replaces the cached plan pointer and resets the `consecutiveJobMisses` map. This bounds the divergence window to ~25s at the default 5s poll interval without adding new plumbing.

> **⚠️ Retraction**: `reconcileTruth()` was **never implemented** despite being listed as fixed here. See [docs/findings/async-reconciler-review.md F1](../findings/async-reconciler-review.md).

---

#### H4. No Graceful Drain in `StatusQueue`

> **Status**: ✅ Fixed

The `StatusQueue` channel is unbuffered on the close path — when context is cancelled, the worker pool goroutines in the status writer exit immediately via `ctx.Done()`. Any queued updates in the 1000-capacity channel are silently lost.

**File**: `internal/message/status_queue.go`, `internal/provider/processor/status/writer.go`.

**Fix Applied**: Added `Len()` method to `StatusQueue`. Added `drain()` method to Writer that runs after all worker goroutines exit — it reads remaining items from both plan and exception queues using a `context.WithTimeout(context.Background(), 5s)` deadline with a `default` case for empty-channel exit.

---

#### H5. Execution State Handlers Fetch Jobs from the K8s Cache Directly

> **Status**: ✅ Fixed

In `state_execution.go`, `FetchCurrentCycleJobs` uses the cached client (`state.Client`), not `APIReader`. The design doc says:

> Business logic never calls the K8s API directly → I/O isolation

Using the cached client means the Job list may be stale (informer lag). Combined with the lack of Job watches (C2), this means execution processors may consistently see stale Job data until the poll timer re-drives with fresh data from the provider.

**File**: `internal/provider/processor/plan/state/utils.go`, `FetchCurrentCycleJobs()`.

**Fix Applied**: Changed `FetchCurrentCycleJobs` parameter from `client.Client` to `client.Reader`. Threaded `APIReader` through the full chain: `provider.go` → `Coordinator` → `Worker` → `state.Config` → `state_execution.go` call sites. Both `hibernatingState` and `wakingUpState` now call `FetchCurrentCycleJobs(ctx, state.APIReader, plan)`.

---

### Medium (Consider)

#### M1. Recursive `handle()` on `StateResult.Requeue` — Unbounded Call-Depth Possible

`dispatch()` was removed and replaced by `StateResult{Requeue: true}`, which causes `Worker.handle()` to call itself synchronously. If a chain like idle→hibernating→error→recovery all return `Requeue: true`, the call stack grows linearly. In practice the chain is short (<4 levels), but there's no guard against cycles (e.g., a bug causing recovery→hibernating→error→recovery).

**File**: `internal/provider/processor/plan/worker.go`, `handle()`.

**Suggestion**: Add a `maxDispatchDepth` counter and log a warning if it exceeds 4.

---

#### M2. `handleRetry` Doesn't Reset `CurrentStageIndex` in Mutate Closure

> **Status**: ✅ Not a Bug (Verified)

In `state_recovery.go`, `handleRetry` deliberately retries from the **current stage** rather than resetting to stage 0. It only resets failed targets within that stage to `StatePending` for re-dispatch. This is correct behavior:

- Earlier stages that completed successfully are left as-is.
- The overflow guard (`currentStageIndex >= len(execPlan.Stages)` → 0) handles edge cases.
- `handleManualRetry` (annotation-based reset) DOES reset to stage 0 with fresh executions — this is the appropriate path for full restarts.

The original concern about skipping stages is unfounded because `execute()` already drives stage advancement from `CurrentStageIndex` forward, and completed stages don't need re-execution.

**File**: `internal/provider/processor/plan/state/state_recovery.go`, `handleRetry()`.

---

#### M3. Exception `LifecycleProcessor` Is Single-Threaded

The exception lifecycle processor processes every exception serially within `HandleSubscription`. With many exceptions (e.g., 100+), this could become a bottleneck. The plan Coordinator solved this with per-plan Workers; the exception processor hasn't.

**File**: `internal/provider/processor/scheduleexception/lifecycle.go`.

---

#### M4. No Metrics for Worker Goroutine Count

> **Status**: ✅ Fixed

There's no gauge tracking the number of live Worker goroutines in the Coordinator. This is essential for capacity planning and debugging.

**File**: `internal/provider/processor/plan/coordinator.go`, `internal/metrics/metrics.go`.

**Fix Applied**: Added `WorkerGoroutinesGauge` (prometheus.Gauge) in `metrics.go`. Coordinator increments on `spawn()`, decrements on `despawn()`, `reap()`, and `shutdownAll()`.

---

#### M5. `buildConfig()` Allocates a New `Config` on Every `handle()` Call

> **Status**: ✅ Fixed

Each `handle()` allocates a fresh `Config` struct with a dozen fields. This is fine for correctness but creates GC pressure under high poll rates. Consider caching and updating the `Config` struct.

**File**: `internal/provider/processor/plan/worker.go`, `buildConfig()`.

**Fix Applied**: `buildConfig()` now lazily initialises `cachedConfig` on first call and reuses it on subsequent calls. Only the per-call `PlanCtx` field is updated in place.

> **⚠️ Retraction**: The `cachedConfig` optimisation was **reverted** in later refactoring. `buildConfig()` intentionally constructs a fresh `Config` on every `handle()` call to keep handlers fully stateless with respect to the Worker.

---

### Low / Nits

#### L1. `forceWakeUpOnResume` Stale Execution State on Resume

> **Status**: ✅ Fixed

`forceWakeUpOnResume` directly transitioned to `PhaseWakingUp` setting only `Phase` and `CurrentOperation`, without reinitialising `Executions`, `CurrentCycleID`, or `CurrentStageIndex`. Since suspension can occur during any phase (including mid-`Hibernating`), the stale execution entries from the pre-suspension cycle caused `wakingUpState` to either skip wakeup jobs entirely (all targets already terminal) or operate on a half-completed execution list with mismatched cycle labels.

**File**: `internal/provider/processor/plan/state/state_suspended.go`, `forceWakeUpOnResume()`.

**Fix Applied**: Instead of transitioning directly to `PhaseWakingUp`, `forceWakeUpOnResume` now transitions to `PhaseHibernated` and returns `StateResult{Requeue: true}`. The Worker immediately re-invokes `handle()`, which selects `idleState` for `PhaseHibernated`. `idleState` sees `!ShouldHibernate` + `HasRestoreData` and calls the canonical `transitionToWakingUp()` path — correctly initialising fresh `Executions` (all `StatePending`), a new `CycleID`, `StageIndex=0`, and `CurrentOperation="wakeup"`. Suspension annotations are cleaned up before the requeue.

---

#### L2. Magic Number `5` in `pruneCycleHistory`

> **Status**: ✅ Fixed

Consider extracting as a named constant: `maxCycleHistorySize = 5`.

**File**: `internal/provider/processor/plan/state/utils.go`, `pruneCycleHistory()`.

**Fix Applied**: Extracted to named constant.

---

#### L3. `mapsEqual` Reimplements Stdlib

> **Status**: ✅ Fixed

`mapsEqual` in `provider_hibernateplan.go` reimplements `maps.Equal` (available since Go 1.21+). Use `maps.Equal` from the standard library.

**File**: `internal/provider/provider_hibernateplan.go`, `mapsEqual()`.

**Fix Applied**: Replaced with `maps.Equal` from stdlib.

---

#### L4. Design Doc Directory Structure Doesn't Match Implementation

Doc says `internal/processor/plan/...` but code is `internal/provider/processor/plan/...`. Update the design doc.

**File**: `docs/plan/async-phase-driven-reconciler.md`.

---

#### L5. Missing Rapid-Coalescing Integration Test

`watchutil_test.go` has comprehensive tests but no test for coalescing behavior within `HandleSubscription` (rapid Store→Store→Handle should only see last value). Consider adding an integration test.

**File**: `internal/message/watchutil_test.go`.

---

---

## Post-Review Fixes

The following fixes were discovered and applied during the extended review session, beyond the original 17-item finding list.

### PR1. `patchPreservingStatus` — Prevent Patch Response from Clobbering Optimistic Status

controller-runtime's `Patch()` deserialises the API server response back into the live object, overwriting `Status` with the server's (potentially stale) version. Since state handlers hold optimistic status mutations that haven't been persisted yet, every annotation/spec Patch silently reverted pending status changes.

**Fix**: Added `patchPreservingStatus()` helper on `Config` that snapshots `Status.DeepCopy()` before the Patch and restores it afterwards. Applied to all 7 Patch call sites in state handlers:

| Call Site | File |
|-----------|------|
| `TransitionToSuspended` (record suspended-at-phase annotation) | `state.go` |
| `Handle` (deadline expired → Spec.Suspend=false) | `state_suspended.go` |
| `OnDeadline` (deadline timer → Spec.Suspend=false) | `state_suspended.go` |
| `cleanupSuspensionAnnotations` (remove suspend annotations) | `state_suspended.go` |
| `handleManualRetry` (clear retry-now annotation) | `state_recovery.go` |
| `clearRetryAtAnnotation` (clear retry-at annotation) | `state_recovery.go` |
| `postWakeupCleanup` (remove suspended-at-phase annotation) | `state_execution.go` |

Two lifecycle Patch calls (AddFinalizer, RemoveFinalizer) intentionally use plain `Patch` because they run at Phase="" or during deletion where no optimistic status exists.

---

### PR2. `mergeIncoming` — Prevent Informer Delivery from Clobbering Optimistic Status

When the watchable map delivers a fresh `PlanContext` to the worker via `slot.ready`, the informer-sourced plan carries the **last-persisted** status, which lags behind the worker's optimistic in-memory mutations by at least one StatusWriter round-trip. The original code unconditionally replaced `cachedCtx`, silently reverting optimistic phase transitions (e.g., Active→Hibernating would revert to Active).

**Fix**: Added `mergeIncoming()` method to Worker. On every `slot.ready` delivery (except the first), it accepts the incoming PlanContext's Spec, ObjectMeta, and provider-computed fields (Exceptions, ScheduleResult, HasRestoreData) but **carries forward** the optimistic `plan.Status` from the previous `cachedCtx`. A corresponding `reconcileTruth()` correction path was designed but not implemented (see F1).

**File**: `worker.go`, `mergeIncoming()`.

---

### PR3. `updateExecutionStatuses` Refactor — Route Through StatusWriter

The original `updateExecutionStatuses` wrote directly to the status sub-resource via `b.Status().Patch()`, bypassing the StatusWriter and clobbering the worker's optimistic status (same root cause as PR1 but in the execution hot loop).

**Fix**: Refactored to:
1. Snapshot execution states before mutation via `snapshotExecutionStates()`.
2. Mutate `plan.Status.Executions` in-place (optimistic).
3. Compare via `executionStatesEqual()` — only queue `PlanStatuses.Send()` when drift is detected.
4. The Send closure captures a copy of `Executions` at the snapshot point.

This eliminates redundant writes on poll ticks where jobs haven't progressed, while still capturing incremental changes (state transitions, attempt bumps, JobRef/LogsRef assignment).

The `executionSnapshot` struct was expanded to include `State`, `Attempts`, `Message`, `JobRef`, and `LogsRef` for comprehensive producer-side dedup.

**Files**: `state.go` (`updateExecutionStatuses`), `utils.go` (`executionSnapshot`, `snapshotExecutionStates`, `executionStatesEqual`).

---

### PR4. Execution Status Cascade Pattern — Align with Legacy Controller

The original execution status update used an `if/else if/else if` chain that missed `StartedAt` for fast-completing jobs (sub-5s). The pattern was refactored to match the legacy controller's cascade:

1. **StartedAt** hoisted with idempotent guard — always set when `job.Status.StartTime` is available.
2. **Active > 0** → `StateRunning` (overwritten by terminal if conditions match).
3. **Conditions loop** iterates `JobComplete`/`JobFailed` — overwrites Running with terminal state + `break` on first match.
4. **Metrics emission** — fires on first transition to terminal state, capturing duration if both StartedAt and FinishedAt are set.

K8s Job controller guarantees `JobComplete` and `JobFailed` are mutually exclusive, so the loop without `else if` is functionally correct. The `break` provides defensive clarity.

**File**: `state.go`, `updateExecutionStatuses()`.

---

## Fix Log

Fixes applied on 2026-03-04. C2 deferred (under consideration).

### Original Review Findings

| ID | Fix | Files Changed |
|----|-----|---------------|
| C1 | Route `removeFromPlanStatus` through `PlanStatuses.Send()` | `lifecycle.go` |
| C3 | Re-fetch from `APIReader` + `RetryOnConflict` for finalizer patch | `lifecycle.go` |
| H1 | `workerIdleTimeout` (30m) + fifth select case + `reap()` callback | `worker.go`, `coordinator.go` |
| H3 | `reconcileTruth()` — every 5th poll, fetch from APIReader, replace on phase divergence | `worker.go` |
| H4 | `Writer.drain()` with 5s background-context deadline | `status_queue.go`, `writer.go` |
| H5 | `APIReader` threaded: provider → Coordinator → Worker → Config → `FetchCurrentCycleJobs` | `provider.go`, `coordinator.go`, `worker.go`, `state.go`, `state_execution.go`, `utils.go` |
| M4 | `WorkerGoroutinesGauge` + Inc/Dec in spawn/despawn/reap/shutdownAll | `metrics.go`, `coordinator.go` |
| M5 | Cached `Config` — lazily allocated, only `PlanCtx` updated per call | `worker.go` |
| L2 | Extracted `maxCycleHistorySize` constant | `utils.go` |
| L3 | Replaced `mapsEqual` with `maps.Equal` | `provider_hibernateplan.go` |
| L1 | Transition to `Hibernated` + dispatch instead of direct `WakingUp` | `state_suspended.go` |

### Post-Review Fixes

| ID | Fix | Files Changed |
|----|-----|---------------|
| PR1 | `patchPreservingStatus` — snapshot Status before Patch, restore after | `state.go`, `state_suspended.go`, `state_recovery.go`, `state_execution.go` |
| PR2 | `mergeIncoming` — carry forward optimistic Status on watchable delivery | `worker.go` |
| PR3 | `updateExecutionStatuses` — route through StatusWriter with drift detection | `state.go`, `utils.go` |
| PR4 | Cascade pattern for execution status (StartedAt → Active → Conditions) | `state.go` |

---

## Readiness Assessment

Comprehensive cross-check performed on 2026-03-04 after all fix passes.

### 1. Status Write Path Integrity

**Invariant**: Only the StatusWriter writes status sub-resources to the K8s API.

All 14 status mutation sites in plan state handlers now route through `PlanStatuses.Send()`:

| Mutation Site | File | Verified |
|---------------|------|----------|
| `TransitionToSuspended` | `state.go` | ✅ |
| `nextStage` | `state.go` | ✅ |
| `setError` | `state.go` | ✅ |
| `updateExecutionStatuses` | `state.go` | ✅ (drift-gated) |
| `transitionToHibernating` | `state_idle.go` | ✅ |
| `transitionToWakingUp` | `state_idle.go` | ✅ |
| `hibernatingState.finalize` | `state_execution.go` | ✅ |
| `wakingUpState.finalize` | `state_execution.go` | ✅ |
| `resume` (normal) | `state_suspended.go` | ✅ |
| `forceWakeUpOnResume` | `state_suspended.go` | ✅ |
| `handleManualRetry` | `state_recovery.go` | ✅ |
| `handleRetry` | `state_recovery.go` | ✅ |
| `retryToPhase` | `state_recovery.go` | ✅ |
| `handleInit` | `state_lifecycle.go` | ✅ |

Exception processor: `removeFromPlanStatus` routes through `PlanStatuses.Send()` ✅.

**No direct `Status().Update()` or `Status().Patch()` calls remain in any state handler or processor.**

### 2. Optimistic Status Preservation

**Invariant**: The worker's in-memory `plan.Status` is never silently overwritten by stale data.

Two attack vectors identified and mitigated:

| Vector | Mitigation | Verified |
|--------|------------|----------|
| `client.Patch()` deserialises API response into live object | `patchPreservingStatus()` snapshots + restores Status | ✅ (7 call sites) |
| Watchable delivery carries informer's stale Status | `mergeIncoming()` carries forward optimistic Status | ✅ |
| Genuine divergence (dropped status writes) | `reconcileTruth()` ⚠️ designed but not implemented | ⚠️ Gap |

Patch call sites **correctly using plain Patch** (no optimistic status to preserve):

| Call Site | Reason |
|-----------|--------|
| `handleInit` (AddFinalizer) | Phase="" — no prior optimistic state |
| `handleDelete` (RemoveFinalizer) | Plan is being deleted |
| `relabelStaleFailedJobs` | Patching Job objects, not the Plan |

### 3. Execution Status Update Pipeline

| Check | Status |
|-------|--------|
| StartedAt always captured (idempotent guard) | ✅ |
| Active > 0 → StateRunning (overwritten by terminal) | ✅ |
| JobComplete/JobFailed conditions → terminal with `break` | ✅ |
| Metrics on first terminal transition | ✅ |
| Drift detection via `snapshotExecutionStates` / `executionStatesEqual` | ✅ |
| Only `PlanStatuses.Send()` when executions changed | ✅ |
| GC'd job inference (FinishedAt set, job gone → Completed) | ✅ |
| Lost job tracking (consecutiveJobMissThreshold = 3 → StatePending) | ✅ |
| Stale runner job skip (LabelStaleRunnerJob) | ✅ |

### 4. Worker Event Loop

| Check | Status |
|-------|--------|
| 5 select cases: slot.ready, requeueTimer (poll+retry), deadlineTimer, idleTimer | ✅ |
| `mergeIncoming()` on every slot delivery | ✅ |
| `reconcileTruth()` every 5th poll cycle | ⚠️ Not implemented — see F1 |
| Worker idle reaping (30m timeout) | ✅ |
| All timers reset on meaningful events | ✅ |
| `cleanup()` cancels all timers on worker exit | ✅ |
| `cachedConfig` reused, only `PlanCtx` updated per handle() | ⚠️ Reverted — fresh Config per call is now intentional |

### 5. Coordinator Lifecycle

| Check | Status |
|-------|--------|
| `spawn()` increments `WorkerGoroutinesGauge` | ✅ |
| `despawn()` decrements gauge + cancels context | ✅ |
| `reap()` decrements gauge (idle worker self-termination) | ✅ |
| `shutdownAll()` decrements gauge for all workers | ✅ |
| Delete event → `despawn()` removes worker entry | ✅ |

### 6. Status Writer

| Check | Status |
|-------|--------|
| `APIReader` for fresh fetches inside `RetryOnConflict` | ✅ |
| `isPlanStatusEqual` guard skips no-op writes | ✅ |
| `drain()` flushes buffered updates on shutdown (5s timeout) | ✅ |
| PreHook/PostHook lifecycle hooks | ✅ |
| 10 plan workers + 5 exception workers | ✅ |

### 7. Provider Layer

| Check | Status |
|-------|--------|
| `GenerationChangedPredicate \| AnnotationChangedPredicate` prevents status-write feedback | ✅ |
| `configMapDataChangedPredicate` filters annotation-only restore CM updates | ✅ |
| Watches `ScheduleException` via `findPlansForException` | ✅ |
| Does NOT own `Job` (C2 deferred — poll timer is functional fallback) | ⏳ |

### 8. Metrics Coverage

| Metric | Purpose | Verified |
|--------|---------|----------|
| `hibernator_execution_duration_seconds` | Per-target operation duration | ✅ |
| `hibernator_execution_total` | Per-target terminal transitions | ✅ |
| `hibernator_reconcile_total` | Per-handle() call counting | ✅ |
| `hibernator_reconcile_duration_seconds` | Per-handle() timing | ✅ |
| `hibernator_active_plans` | Phase gauge (inc/dec on transitions) | ✅ |
| `hibernator_jobs_created_total` | Job creation counting | ✅ |
| `hibernator_job_failures_total` | Job creation failure counting | ✅ |
| `hibernator_status_queue_dropped_total` | Queue overflow visibility | ✅ |
| `hibernator_watchable_subscribe_total` | HandleSubscription invocation counting | ✅ |
| `hibernator_watchable_subscribe_duration_seconds` | HandleSubscription handler timing | ✅ |
| `hibernator_worker_goroutines` | Worker goroutine gauge | ✅ |

### 9. Build & Tests

| Check | Status |
|-------|--------|
| `go build ./...` — clean | ✅ |
| `go test ./internal/message/...` — 11/11 pass | ✅ |
| `go test ./internal/metrics/...` — 18/18 pass | ✅ |
| `go test ./api/...` — all pass | ✅ |
| `go test ./internal/scheduler/... ./internal/restore/... ./internal/recovery/...` — all pass | ✅ |
| No unit tests for `internal/provider/processor/...` packages | ⚠️ Gap |

### 10. Remaining Open Items

| ID | Severity | Assessment | Risk | Action |
|----|----------|------------|------|--------|
| C2 | Critical | No Job watches → 0-5s detection lag via poll timer | Low | Functional but sub-optimal. Can be added later as performance enhancement. |
| M1 | Medium | Max dispatch depth ~4 (idle→exec→error→recovery) | Very Low | Chains are terminating by construction. Optional `maxDispatchDepth` guard. |
| M3 | Medium | Exception processor serial | Low | Exceptions are lightweight; bottleneck unlikely below ~100 concurrent exceptions. |
| L4 | Low | Design doc path mismatch | None | Cosmetic fix. |
| L5 | Low | Missing coalescing test | None | Nice-to-have. |

---

## Summary Table

| ID | Severity | Area | Issue | Status |
|----|----------|------|-------|--------|
| C1 | **Critical** | Exception Lifecycle | Direct status write bypasses status writer | ✅ Fixed |
| C2 | **Critical** | Provider | No Job watches — event detection relies solely on poll timer | ⏳ Deferred |
| C3 | **Critical** | Exception Lifecycle | Stale object used for finalizer patch in delete handler | ✅ Fixed |
| H1 | High | Worker Lifecycle | No idle reaping of Worker goroutines | ✅ Fixed |
| H2 | High | Provider/Watchable | Potential hot loop from ResourceVersion-based equality | ✅ Not a Bug |
| H3 | High | State Machine | No divergence detection between optimistic and persisted state | ⚠️ Not Implemented (`reconcileTruth()` designed but never shipped — see F1) |
| H4 | High | Status Writer | No graceful drain of queued updates on shutdown | ✅ Fixed |
| H5 | High | Execution | `FetchCurrentCycleJobs` uses cached client, may see stale Jobs | ✅ Fixed |
| M1 | Medium | State Dispatch | Recursive `handle()` on `StateResult.Requeue` — unbounded depth possible | Open (Low Risk) |
| M2 | Medium | Recovery | `CurrentStageIndex` not reset in `handleRetry` mutate | ✅ Not a Bug |
| M3 | Medium | Exception Processor | Serial processing may bottleneck at scale | Open (Low Risk) |
| M4 | Medium | Coordinator | No worker goroutine count metric | ✅ Fixed |
| M5 | Medium | Worker | Config allocation per `handle()` call | ↩️ Reverted (fresh Config per call is intentional) |
| L1 | Low | Suspended | `forceWakeUpOnResume` stale execution state on resume | ✅ Fixed |
| L2 | Low | Utils | Magic number for cycle history pruning | ✅ Fixed |
| L3 | Low | Provider | `mapsEqual` reimplements stdlib | ✅ Fixed |
| L4 | Low | Docs | Directory structure mismatch with implementation | Open |
| L5 | Low | Tests | Missing rapid-coalescing integration test | Open |
| PR1 | — | Status Preservation | Patch response clobbers optimistic status | ✅ Fixed |
| PR2 | — | Status Preservation | Watchable delivery clobbers optimistic status | ✅ Fixed |
| PR3 | — | Execution Pipeline | Direct status write in `updateExecutionStatuses` | ✅ Fixed |
| PR4 | — | Execution Pipeline | StartedAt missed for fast-completing jobs | ✅ Fixed |

---

## Verdict

The architecture is **sound** and the implementation quality is **high**. The Coordinator + Worker actor model is well-suited for this domain.

### Resolved

All **Critical** findings resolved (C1 ✅, C3 ✅, C2 ⏳ deferred — functional with poll timer). High findings H1, H2, H4, H5 resolved ✅; **H3 (`reconcileTruth()`) was listed as fixed here but was never implemented** — see [docs/findings/async-reconciler-review.md F1](../findings/async-reconciler-review.md). Four additional post-review fixes (PR1–PR4) closed race conditions in the optimistic status pipeline that would have been difficult to diagnose in production.

### Merge Readiness: **Ready with Conditions**

**Blocking for merge**: None — all Critical and High items are resolved or acceptably deferred.

**Recommended before flipping `--legacy-reconciler=false` (default)**:
1. Unit tests for `internal/provider/processor/...` packages — currently zero coverage on state handlers, worker, coordinator, and status writer.
2. E2E validation of a full hibernation→wakeup→error→recovery cycle under the async reconciler.
3. C2 (Job watches) — adding `.Owns(&batchv1.Job{})` to the provider removes the poll-timer lag and gives event-driven job completion detection.

**Follow-up (non-blocking)**:
- M1: Optional `maxDispatchDepth` safety net.
- M3: Exception processor parallelisation (only needed at scale).
- L4, L5: Documentation and testing improvements.
