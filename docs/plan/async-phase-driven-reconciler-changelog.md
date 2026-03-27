> **⚠️ ARCHIVED** — This document has been superseded by [RFC-0008](../../docs/proposals/0008-async-phase-driven-reconciler.md). Do not modify. Preserved for historical reference only.

# Async Phase-Driven Reconciler — Implementation Changelog

**Plan Document**: [async-phase-driven-reconciler.md](./async-phase-driven-reconciler.md) (archived — do not modify)

---

## Changelog

### 2026-03-01 — Initial Implementation

**Scope**: Full implementation of the async phase-driven reconciler architecture as described in the plan document.

#### Decisions Made

1. **Feature Flag Default**: `--legacy-reconciler=true` — existing reconcilers remain the default. New pipeline loads only with `--legacy-reconciler=false`.

2. **Package Structure**: Created exactly as specified in the plan:
   - `internal/message/` — watchable map types + HandleSubscription utility
   - `internal/provider/` — K8s → watchable maps (HibernatePlan + ScheduleException)
   - `internal/processor/plan/` — phase processors for HibernatePlan lifecycle
   - `internal/processor/exception/` — ScheduleException state management
   - `internal/processor/status/` — K8s status sub-resource writer

3. **Zero Modification to Existing Code**: All files under `internal/controller/` remain untouched. The new pipeline lives entirely in new packages.

4. **watchable Library Usage**: Used `github.com/telepresenceio/watchable` as the message bus. Map key convention: `"namespace/name"`.

5. **Processor Registration**: Each processor implements `manager.Runnable` via `Start(ctx) error` and `NeedLeaderElection() bool`. Registered via `mgr.Add()`.

6. **Status Writer Pattern**: Dedicated processor subscribes to output maps and performs all K8s status writes with `RetryOnConflict`. Status updates are consumed (deleted from map) after successful write.

7. **Optimistic Phase Updates**: Processors update `PlanResources` immediately after queueing a status mutation. This prevents double-processing during the status-write round-trip.

8. **Job Creation Ownership**: Hibernation and WakeUp processors create runner Jobs directly using the K8s client (passed as dependency). The provider re-fetches Jobs on each reconcile to feed processors.

9. **Schedule Evaluation in Provider**: The provider pre-computes `ScheduleEvaluation` so that processors act on pre-computed data without needing access to the `ScheduleEvaluator`. This keeps processors I/O-isolated.

10. **Requeue Strategy**: Provider returns `RequeueAfter: 5s` during active execution phases (Hibernating/WakingUp) to drive the polling loop. During idle phases, uses the schedule evaluator's next requeue time.

#### Considerations

- **PlanContext.DeepCopy**: All fields are deep-copied to prevent shared-memory issues between goroutines. `Jobs` slice is deep-copied element by element.
- **PlanContext.Equal**: Uses `reflect.DeepEqual` as a pragmatic choice. The watchable library uses this for dedup - if a Store() call produces an identical value, no snapshot is emitted.
- **HandleSubscription**: Adapted from Envoy Gateway's pattern with crash recovery (panic catch → log → continue) to ensure one bad plan doesn't crash the entire processor.
- **Processor Independence**: Each processor only subscribes to its relevant phase(s). Phase transitions cause automatic routing: old processor stops seeing the plan, new processor starts seeing it.
- **Exception Provider**: Kept simple - just stores/deletes ScheduleExceptions in the watchable map. The HibernatePlan provider also reads exceptions directly from K8s to bundle them into PlanContext.
- **Helpers Package**: Extracted pure functions from the existing controller code (createRunnerJob, buildExecutionPlan, getStageStatus, etc.) with explicit dependencies instead of receiver methods.
- **Status Writer Subscription**: Subscribes to PlanStatuses and ExceptionStatuses separately, each in its own goroutine using sync.WaitGroup for clean shutdown.

### 2026-03-01 — Build Verification & Corrections

**Scope**: Fixed compilation errors discovered during build verification.

#### Corrections Applied

1. **HandleSubscription API Alignment**: The callback signature is `func(watchable.Update[K,V])` (single update), not `func(*Snapshot)`. Fixed in exception/lifecycle.go and status/writer.go.

2. **Scheduler API Types**: `scheduler.StageDef` does not exist — corrected to `scheduler.Stage`. `PlanStaged()` takes `([]Stage, int32)` not `([]StageDef)`.

3. **Pointer Dereference**: `strategy.MaxConcurrency` is `*int32` (optional field). Added nil checks and dereference throughout `BuildExecutionPlan()`.

4. **metav1.Time vs custom Time type**: ScheduleException uses standard `metav1.Time`, not a custom `hibernatorv1alpha1.Time`. Fixed in exception lifecycle processor.

5. **Missing Clock Field**: Added `Clock clock.Clock` to `LifecycleProcessor` struct (needed for timestamp operations and passed from app.go).

6. **Missing Imports**: Added `"github.com/telepresenceio/watchable"` to exception/lifecycle.go and status/writer.go for the `watchable.Update` type, and `"k8s.io/utils/clock"` to lifecycle.go.

#### Verification Results

- `go build ./...` — **PASS** (all packages compile)
- `go test ./api/... ./internal/scheduler/... ./internal/recovery/... ./internal/restore/... ./pkg/...` — **ALL PASS** (no regressions)

#### Files Created/Modified (Final List)

| File | Type | Purpose |
|------|------|---------|
| `internal/message/types.go` | New | Watchable map types, PlanContext, status updates |
| `internal/message/watchutil.go` | New | HandleSubscription + coalescing + crash recovery |
| `internal/provider/provider.go` | New | HibernatePlan provider reconciler |
| `internal/provider/exception_provider.go` | New | ScheduleException provider reconciler |
| `internal/processor/plan/helpers.go` | New | Pure helper functions (17 functions) |
| `internal/processor/plan/lifecycle.go` | New | Finalizer, init, deletion processor |
| `internal/processor/plan/schedule.go` | New | Schedule evaluation processor |
| `internal/processor/plan/hibernation.go` | New | Shutdown execution processor |
| `internal/processor/plan/wakeup.go` | New | Wake-up execution processor |
| `internal/processor/plan/suspension.go` | New | Suspension + shared transitionToSuspended |
| `internal/processor/plan/error_recovery.go` | New | Error recovery with backoff |
| `internal/processor/exception/lifecycle.go` | New | Exception state machine processor |
| `internal/processor/status/writer.go` | New | Dedicated K8s status writer |
| `cmd/controller/app/app.go` | Modified | Feature flag + async reconciler wiring |
| `go.mod` / `go.sum` | Modified | Added watchable dependency |

### 2026-03-01 — Async Error Propagation (RunnerErrors)

**Scope**: Added two-tier error propagation following Envoy Gateway's pattern.

#### Problem

All processor `Start()` methods block on `HandleSubscription` until ctx is cancelled, then `return nil`. There was no mechanism for processors to report **critical async errors** (e.g., unrecoverable failures inside subscription handlers) back to the operator. Panics were caught by `handleWithCrashRecovery`, but non-panic errors were silently swallowed.

#### Solution

Adopted Envoy Gateway's `RunnerErrors` pattern — a watchable map that processors publish critical errors into:

```
Processor goroutine (async error)
        │
        ▼
ErrorNotifier.NotifyError(err)
        │
        ▼
RunnerErrors.Store(runnerName, WatchableError)
        │
        ▼
HandleSubscription (goroutine in setupAsyncReconciler)
        │
        ▼
Log critical error with runner name + timestamp
```

**Two-tier error handling:**
- **Synchronous errors**: `runner.Start(ctx)` returns `error` → `mgr.Add()` propagates → operator aborts startup.
- **Asynchronous errors**: `ErrorNotifier.NotifyError(err)` → stores in `RunnerErrors` watchable map → central subscriber logs.

#### Changes

1. **`internal/message/types.go`** — Added `WatchableError`, `RunnerErrors`, `RunnerErrorNotifier`, and `NotifyError()`.
2. **All 8 processors** — Added `ErrorNotifier message.RunnerErrorNotifier` field to each struct.
3. **`cmd/controller/app/app.go`** — Created shared `RunnerErrors` instance, `newNotifier()` helper, and registered a `manager.RunnableFunc` subscriber that logs async errors. Passed `ErrorNotifier` to all processor instantiations.

#### Design Decisions

- **RunnerErrors is a `watchable.Map[string, WatchableError]`**, not a Go channel. This matches the existing message bus pattern and avoids channel lifecycle management.
- **`RunnerErrorNotifier` is a value type** (not a pointer) — lightweight struct with runner name + shared map reference. Safe to copy.
- **Central subscriber uses `manager.RunnableFunc`** to integrate with controller-runtime lifecycle (starts/stops with manager).
- **Async errors are logged, not fatal** — consistent with operator resilience principles. Processors should degrade gracefully; the error map provides visibility for alerting/dashboards.
- **Each processor gets its own named notifier** — the runner name in the error map identifies which processor reported the error.

#### Verification

- `go build ./...` — **PASS**
- All existing tests — **PASS** (no regressions)

---

### 2026-03-01 — ~~Consolidate ErrorNotifier Into Processor Implementations~~ (Reverted)

**Status**: Reverted — `ErrorNotifier` and all `RunnerErrors` infrastructure removed entirely.

#### What Was Reverted

The entire `RunnerErrors` / `RunnerErrorNotifier` pattern introduced in the previous entry has been removed:

1. **`internal/message/types.go`** — Removed `WatchableError`, `NewWatchableError`, `RunnerErrors`, `RunnerErrorNotifier`, and `NotifyError()`.
2. **All 8 processor structs** — Removed `ErrorNotifier message.RunnerErrorNotifier` field.
3. **`cmd/controller/app/app.go`** — Removed shared `RunnerErrors` instance, `newNotifier()` helper, central `manager.RunnableFunc` subscriber, and all `ErrorNotifier:` wiring in processor instantiations. Cleaned up unused `"sigs.k8s.io/controller-runtime/pkg/manager"` and `"github.com/telepresenceio/watchable"` imports.
4. **All 15 `NotifyError()` calls** — Removed from hibernation, wakeup, lifecycle, schedule, suspension, error_recovery, exception/lifecycle, and status/writer processors.

#### Verification

- `go build ./...` — **PASS**

---

### 2026-03-01 — Consolidate errChan Error Propagation

**Scope**: Systematic wiring of `errChan` across all processors after user refactored `HandleSubscription` to pass `errChan chan error` to handlers.

#### Context

`HandleSubscription` was refactored to create a buffered `errChans := make(chan error, 10)` with a consumer goroutine that logs `log.Error(err, "observed an error")`. The handler function signature changed to `func(update watchable.Update[K, V], errChan chan error)`.

#### Two-Category Error Model

1. **Infrastructure errors** (K8s API failures for finalizers, patches, lists, job creation) → `errChan <- fmt.Errorf(...)` — logged by HandleSubscription's consumer goroutine.
2. **Plan execution errors** (BuildExecutionPlan failure, stage failures in strict mode) → `setError()` only — emitted as plan status error message. No `log.Error`, no `errChan` (except BuildExecutionPlan which is both infrastructure AND execution error).
3. **Non-fatal errors** (e.g., restore data cleanup, annotation cleanup) → keep as `log.Error` (informational, doesn't affect core flow).

#### Changes per Processor

| Processor | Changes |
|-----------|---------|
| `lifecycle.go` | Wired `errChan` to `handleUpdate`/`handleDelete`. Finalizer add/remove, restore point, job list errors → `errChan`. |
| `schedule.go` | Wired `errChan` to `handleUpdate`. `transitionToSuspended` failure → `errChan`. |
| `suspension.go` | Wired `errChan` to `handleUpdate`/`handleSuspendUntilAnnotation`. Auto-resume patch failure → `errChan`. Non-fatal annotation cleanup stays as `log.Error`. |
| `hibernation.go` | Added `errChan` to `executeStageTargets`. `CreateRunnerJob` failure → `errChan` (was `log.Error`). `BuildExecutionPlan` already wired (both `setError` + `errChan`). |
| `wakeup.go` | Full wiring: handler → `handleUpdate` → `executeStageTargets`. `BuildExecutionPlan` → `setError` + `errChan`. `CreateRunnerJob` → `errChan`. Non-fatal cleanup stays as `log.Error`. |
| `error_recovery.go` | Added `errChan` to `handleManualRetry`, `executeRetry`, `relabelStaleFailedJobs`. Annotation clear, BuildExecutionPlan rebuild, stale job list failures → `errChan`. Per-job relabel patch stays as `log.Error`. |
| `exception/lifecycle.go` | Wired `errChan` through `processException`/`handleExceptionDeletion`. Finalizer add/remove, plan label, removeFromPlanStatus → `errChan`. |
| `status/writer.go` | Removed redundant `log.Error` from exception status subscriber (errChan consumer already logs it). |

#### Verification

- `go build ./...` — **PASS**
