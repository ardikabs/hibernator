> **⚠️ ARCHIVED** — This document has been superseded by [RFC-0008](../../enhancements/0008-async-phase-driven-reconciler.md). Do not modify. Preserved for historical reference only.

# Async Phase-Driven Reconciler Architecture

**Status**: Archived
**Last Updated**: 2026-03-01
**Feature Flag**: `--legacy-reconciler` (default: `true`)

---

## Table of Contents

- [Motivation](#motivation)
- [Is This Overkill? — Honest Alternatives Assessment](#is-this-overkill--honest-alternatives-assessment)
- [Architecture Overview](#architecture-overview)
- [Feature Flag Strategy](#feature-flag-strategy)
- [Detailed Design](#detailed-design)
  - [Phase 1: Foundation — Message Bus & Utilities](#phase-1-foundation--message-bus--utilities)
  - [Phase 2: Provider Layer — K8s → Watchable Maps](#phase-2-provider-layer--k8s--watchable-maps)
  - [Phase 3: Phase Processors — Business Logic](#phase-3-phase-processors--business-logic)
  - [Phase 4: Status Writer — Watchable Maps → K8s API](#phase-4-status-writer--watchable-maps--k8s-api)
  - [Phase 5: Wiring — Composition Root](#phase-5-wiring--composition-root)
  - [Phase 6: Supporting Infrastructure](#phase-6-supporting-infrastructure)
- [Data Flow](#data-flow)
- [Idempotency Guarantees](#idempotency-guarantees)
- [Execution Processor Polling Strategy](#execution-processor-polling-strategy)
- [Testing Strategy](#testing-strategy)
- [Migration Plan](#migration-plan)
- [Key Design Decisions](#key-design-decisions)

---

## Motivation

The current HibernatePlan reconciler ([internal/controller/hibernateplan/controller.go](../../internal/controller/hibernateplan/controller.go)) is an 826-line monolithic function built on a synchronous mental model. While functional, it creates several long-term maintenance problems:

1. **Phase transition logic is interleaved with I/O**: A single `Reconcile()` call evaluates schedules, creates Jobs, queries Job status, writes status updates, and handles error recovery — all inline.
2. **No hook points**: Introducing cross-cutting behavior (e.g., pre-transition webhooks, runner progress integration, notification dispatch, audit logging) requires modifying the core reconcile loop.
3. **Execution plan rebuilt every reconcile**: The execution plan (DAG/stages) is not persisted — it's recomputed from spec on every reconcile cycle, making behavior undefined if spec changes mid-operation.
4. **Duplicate queries**: Active ScheduleExceptions are fetched twice per reconcile (once in `updateActiveExceptions`, again in `evaluateSchedule`).
5. **Job polling is requeue-based**: The reconciler requeues every 5-10s during execution to poll Job status — mixing time-based scheduling with event-driven reconciliation.
6. **Testing difficulty**: The reconciler is tightly coupled to the K8s API client, making unit tests require full envtest infrastructure.

As the project grows (notifications, multi-cloud executors, approval workflows, advanced scheduling), these problems compound — each new feature adds branches to the monolith.

---

## Is This Overkill? — Honest Alternatives Assessment

Before committing to the watchable pub/sub architecture, we should objectively evaluate simpler alternatives. The goal is to pick the **simplest approach that genuinely solves the maintenance problem** while supporting future extensibility.

### Alternative 1: Phase Handler Registry (Strategy Pattern)

**Approach**: Keep a single synchronous reconciler but decompose `Reconcile()` into registered phase handlers. Each handler implements `Handle(ctx, plan) (Result, error)`. The reconciler becomes a thin dispatcher:

```go
type PhaseHandler interface {
    Handle(ctx context.Context, plan *v1alpha1.HibernatePlan) (ctrl.Result, error)
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    plan := ... // fetch
    handler := r.handlers[plan.Status.Phase]
    return handler.Handle(ctx, plan)
}
```

| Criterion | Rating | Notes |
|-----------|--------|-------|
| Complexity | ★☆☆ Very Low | No new deps, familiar pattern |
| Phase isolation | ★★☆ Good | Each phase in its own file/struct |
| Hook points | ★★☆ Moderate | Before/after wrapper per handler, but hooks are synchronous |
| Testability | ★★☆ Moderate | Handlers still need K8s client mocks; coupling to reconcile loop |
| Idempotency | ★☆☆ No change | Same requeue-based model — no coalescing or dedup |
| Async extensibility | ★☆☆ None | Still synchronous; adding notifications or webhooks causes blocking |
| Addresses root cause | ★☆☆ Partially | Organizes code but doesn't change the execution model |

**Verdict**: Good first step for code organization, but doesn't solve the core architectural issue — the reconciler is still a synchronous state machine that polls and blocks. Future features (notifications, runner streaming integration, approval gates) would still require modifying the handler implementations with blocking I/O, creating the same spaghetti problem in smaller files.

### Alternative 2: State Machine Library (e.g., `looplab/fsm`)

**Approach**: Formalize the phase transitions using a state machine with before/after callbacks:

```go
fsm.NewFSM("Active", fsm.Events{
    {Name: "hibernate", Src: []string{"Active"}, Dst: "Hibernating"},
    {Name: "complete_shutdown", Src: []string{"Hibernating"}, Dst: "Hibernated"},
    ...
}, fsm.Callbacks{
    "before_hibernate": func(e *fsm.Event) { ... },
    "after_hibernate":  func(e *fsm.Event) { ... },
})
```

| Criterion | Rating | Notes |
|-----------|--------|-------|
| Complexity | ★★☆ Low | Small library, well-understood pattern |
| Phase isolation | ★★★ Excellent | Explicit state graph with named transitions |
| Hook points | ★★★ Excellent | Before/after/enter/leave callbacks per transition |
| Testability | ★★☆ Moderate | FSM testable in isolation, but callbacks still need K8s mocks |
| Idempotency | ★☆☆ No change | FSM runs inside reconciler — same requeue model |
| Async extensibility | ★☆☆ None | Callbacks are synchronous by design |
| Addresses root cause | ★★☆ Partially | Formalizes transitions but doesn't decouple I/O from logic |

**Verdict**: Excellent for formalizing the state graph and providing hook points. But the callbacks are synchronous — adding a notification webhook or runner progress listener would block the reconcile loop. The FSM runs inside the reconciler, so it still inherits the polling/requeue model with all its problems.

### Alternative 3: Event-Driven with Go Channels (No External Deps)

**Approach**: Build a custom pub/sub layer using Go channels. A provider goroutine watches K8s and pushes events. Phase processor goroutines read from filtered channels.

| Criterion | Rating | Notes |
|-----------|--------|-------|
| Complexity | ★★★ High | Must build coalescing, snapshot diffing, thread safety, cleanup |
| Phase isolation | ★★★ Excellent | Same as watchable approach |
| Hook points | ★★★ Excellent | Same pipeline model |
| Testability | ★★★ Excellent | Same benefits |
| Idempotency | ★★☆ Possible | Must implement coalescing and DeepEqual manually |
| Async extensibility | ★★★ Excellent | Full async model |
| Addresses root cause | ★★★ Fully | Same architecture, just hand-rolled |

**Verdict**: Achieves the same result as the watchable approach but requires building and maintaining ~500 lines of concurrent infrastructure code (coalescing, snapshot management, subscriber lifecycle, panic recovery). This is exactly what `watchable` already provides, battle-tested by Telepresence/Envoy Gateway.

### Alternative 4: Hybrid — Strategy Pattern + Async Hooks

**Approach**: Phase Handler Registry (Alternative 1) for the core reconcile flow, plus a separate event channel for cross-cutting async concerns (notifications, runner reporting). The reconciler dispatches to phase handlers synchronously, then fires async events for side effects.

| Criterion | Rating | Notes |
|-----------|--------|-------|
| Complexity | ★★☆ Moderate | Strategy pattern is simple; event channel adds some complexity |
| Phase isolation | ★★☆ Good | Phase handlers are isolated |
| Hook points | ★★★ Excellent | Async events for cross-cutting concerns |
| Testability | ★★☆ Moderate | Handlers need K8s mocks; event consumers are testable |
| Idempotency | ★★☆ Partial | Core reconcile still requeue-based; events are fire-and-forget |
| Async extensibility | ★★☆ Moderate | Async for side effects, but core transitions are still sync |
| Addresses root cause | ★★☆ Partially | Splits side effects from core, but core is still monolithic sync |

**Verdict**: Pragmatic middle ground. Core transitions remain simple and synchronous (easy to reason about), while side effects (notifications, audit) run asynchronously. However, this creates two execution models (sync reconciler + async events) that must stay consistent — the reconciler makes decisions that the event consumers depend on, creating implicit coupling.

### Alternative 5: Watchable Pub/Sub Pipeline (Proposed)

| Criterion | Rating | Notes |
|-----------|--------|-------|
| Complexity | ★★★ High | New dependency, new mental model, more goroutines |
| Phase isolation | ★★★ Excellent | SubscribeSubset per phase — true decoupling |
| Hook points | ★★★ Excellent | Any processor can subscribe to any map — infinite composability |
| Testability | ★★★ Excellent | Processors are pure functions of map events; no K8s mocks needed |
| Idempotency | ★★★ Excellent | Built-in: coalescing + DeepEqual + phase routing |
| Async extensibility | ★★★ Excellent | Adding a notification processor = just another subscriber |
| Addresses root cause | ★★★ Fully | Completely replaces the synchronous polling model |

### Comparison Matrix

| | Strategy Pattern | FSM Library | Custom Channels | Hybrid | **Watchable Pipeline** |
|-|-----------------|-------------|-----------------|--------|----------------------|
| **New deps** | 0 | 1 (small) | 0 | 0 | 1 (watchable) |
| **Code change size** | Small | Medium | Large | Medium | **Large** |
| **Learning curve** | None | Low | Medium | Low | **Medium** |
| **Future-proof** | Low | Medium | High | Medium | **High** |
| **Solves root cause** | No | Partially | Yes | Partially | **Yes** |
| **Risk** | Low | Low | High (bugs) | Medium | **Medium** |
| **When to pick** | Short-term cleanup | Formalize transitions | NIH requirement | Pragmatic middle | **Long-term architecture** |

### Recommendation

**If the project's roadmap is limited** (just maintaining current features, minor additions): **Alternative 1 (Strategy Pattern)** or **Alternative 2 (FSM)** is sufficient. The current code's problem is primarily organizational — splitting 826 lines into phase handlers solves 80% of the pain with 20% of the effort.

**If the project's roadmap includes** notifications, approval workflows, runner streaming integration, multi-tenant operations, or any feature requiring async side-effects: **Alternative 5 (Watchable Pipeline)** is the right investment. The single-dependency cost (`watchable` is 414 lines of code, MIT licensed, stable for 4+ years) is negligible compared to the architectural flexibility gained.

**If you want a stepping stone**: Start with **Alternative 1** (Strategy Pattern) immediately, then migrate to **Alternative 5** later. The strategy pattern decomposition makes the eventual watchable migration easier because each phase handler maps 1:1 to a phase processor.

**Given the project roadmap** (notifications RFC-0006, multi-cloud executors, advanced scheduling exceptions, CLI integration, potential approval workflows), this plan proceeds with **Alternative 5** but acknowledges that **Alternative 1 first + Alternative 5 later** is a viable phased approach.

---

## Architecture Overview

Replace the synchronous HibernatePlan reconciler with an asynchronous, phase-driven processing pipeline inspired by [Envoy Gateway](https://github.com/envoyproxy/gateway).

```
                        ┌──────────────────────────────────────────────┐
                        │              Controller Manager              │
                        └──────────────────────────────────────────────┘
                                            │
                    ┌───────────────────────┼───────────────────────┐
                    ▼                       ▼                       ▼
          ┌─────────────────┐    ┌──────────────────┐    ┌──────────────────┐
          │ Provider         │    │ Streaming Servers │    │ Webhooks         │
          │ Reconciler       │    │ (gRPC/WebSocket)  │    │ (unchanged)      │
          │ (K8s watcher)    │    │ (unchanged)       │    │                  │
          └────────┬─────────┘    └──────────────────┘    └──────────────────┘
                   │ Store()
                   ▼
    ┌──────────────────────────────────────────────────┐
    │              Watchable Maps (Message Bus)         │
    │                                                  │
    │  PlanResources    ExceptionResources             │
    │  watchable.Map    watchable.Map                  │
    │  [ns/name →       [ns/name →                     │
    │   PlanContext]      ScheduleException]            │
    └──────────┬───────────────────────────────────────┘
               │ SubscribeSubset (per phase)
               ▼
    ┌──────────────────────────────────────────────────┐
    │              Phase Processors                     │
    │                                                  │
    │  ┌──────────┐ ┌──────────┐ ┌──────────────────┐ │
    │  │Lifecycle  │ │Schedule  │ │Hibernation       │ │
    │  │(all plans)│ │(Active + │ │(Hibernating)     │ │
    │  │           │ │Hibernated│ │                  │ │
    │  └──────────┘ └──────────┘ └──────────────────┘ │
    │  ┌──────────┐ ┌──────────┐ ┌──────────────────┐ │
    │  │WakeUp    │ │Suspension│ │Error Recovery    │ │
    │  │(WakingUp)│ │(Suspended│ │(Error)           │ │
    │  │          │ │)         │ │                  │ │
    │  └──────────┘ └──────────┘ └──────────────────┘ │
    │  ┌──────────────────────┐                        │
    │  │Exception Lifecycle   │                        │
    │  │(all exceptions)      │                        │
    │  └──────────────────────┘                        │
    └──────────┬───────────────────────────────────────┘
               │ Store() status mutations
               ▼
    ┌──────────────────────────────────────────────────┐
    │              Status Maps (Output)                 │
    │                                                  │
    │  PlanStatuses         ExceptionStatuses           │
    │  watchable.Map        watchable.Map               │
    └──────────┬───────────────────────────────────────┘
               │ Subscribe()
               ▼
    ┌──────────────────────────────────────────────────┐
    │              Status Writer Processor              │
    │              (K8s status sub-resource writes)     │
    └──────────────────────────────────────────────────┘
```

**Core principle**: A single **Provider Reconciler** watches all K8s resources and feeds `watchable.Map` instances (the internal message bus). **Phase Processors** subscribe via `SubscribeSubset` (filtered by phase) and handle transitions independently. A **Status Writer** processor subscribes to an output map and batch-writes to the K8s API.

**Key properties**:
- Phase = routing key → natural idempotency
- Processors are independent goroutines → async by design
- Adding new behavior = adding a new subscriber → Open/Closed Principle
- Business logic never calls the K8s API directly → I/O isolation per architectural mandate

---

## Feature Flag Strategy

To preserve all existing stable code untouched, the new architecture lives alongside the current reconcilers behind a feature flag:

```
--legacy-reconciler=true   (default)  → loads existing reconcilers from internal/controller/
--legacy-reconciler=false              → loads new pipeline from internal/provider/ + internal/processor/
```

### Implementation in `cmd/controller/app/app.go`

```go
// In Options struct:
LegacyReconciler bool // default: true

// In Run():
if opts.LegacyReconciler {
    // Wire existing reconcilers (unchanged code path)
    hibernateplanReconciler := &hibernateplan.Reconciler{...}
    hibernateplanReconciler.SetupWithManager(mgr, ...)
    scheduleexceptionReconciler := &scheduleexception.Reconciler{...}
    scheduleexceptionReconciler.SetupWithManager(mgr)
} else {
    // Wire new async pipeline
    resources := &message.ControllerResources{}
    statuses := &message.ControllerStatuses{}
    provider := &provider.Reconciler{Resources: resources, ...}
    provider.SetupWithManager(mgr)
    // ... register processors via mgr.Add()
}
```

### Directory Structure

```
internal/
├── controller/                  # UNTOUCHED — existing reconcilers
│   ├── hibernateplan/           # Legacy HibernatePlan reconciler
│   ├── scheduleexception/       # Legacy ScheduleException reconciler
│   └── status/                  # Status updater (shared by both paths)
├── message/                     # NEW — watchable map types + HandleSubscription
│   ├── types.go
│   └── watchutil.go
├── provider/                    # NEW — K8s → watchable maps
│   ├── provider.go              # HibernatePlan provider reconciler
│   └── exception_provider.go    # ScheduleException provider reconciler
├── processor/                   # NEW — phase processors
│   ├── plan/
│   │   ├── lifecycle.go         # Finalizer management, initialization
│   │   ├── schedule.go          # Active/Hibernated → schedule evaluation
│   │   ├── hibernation.go       # Hibernating → job management
│   │   ├── wakeup.go            # WakingUp → job management
│   │   ├── suspension.go        # Suspended → resume logic
│   │   ├── error_recovery.go    # Error → retry/backoff
│   │   └── helpers.go           # Shared helpers (job creation, stage status)
│   ├── exception/
│   │   └── lifecycle.go         # Exception state management
│   └── status/
│       └── writer.go            # K8s status write processor
├── executor/                    # UNTOUCHED
├── metrics/                     # Extended with watchable metrics
├── recovery/                    # UNTOUCHED — reused by error_recovery processor
├── restore/                     # UNTOUCHED — reused by providers and processors
├── scheduler/                   # UNTOUCHED — reused by providers and processors
├── streaming/                   # UNTOUCHED
├── validationwebhook/           # UNTOUCHED
└── wellknown/                   # UNTOUCHED (minor additions for new intervals)
```

**Zero changes to existing code** — the `internal/controller/` package and all its dependencies remain exactly as they are.

---

## Detailed Design

### Phase 1: Foundation — Message Bus & Utilities

#### 1.1 Add `watchable` Dependency

```bash
go get github.com/telepresenceio/watchable
```

The library requires value types to implement `DeepCopy() T` (already generated by controller-gen for all CRD types) or be pure-value types.

#### 1.2 `internal/message/types.go`

Define the shared watchable map types:

**`PlanContext`** — Enriched snapshot of a HibernatePlan bundled with related data:

```go
type PlanContext struct {
    Plan             *v1alpha1.HibernatePlan
    Exceptions       []v1alpha1.ScheduleException  // active exceptions
    Jobs             []batchv1.Job                  // current-cycle jobs
    ScheduleResult   *ScheduleEvaluation            // pre-computed by provider
    HasRestoreData   bool
}

type ScheduleEvaluation struct {
    ShouldHibernate bool
    RequeueAfter    time.Duration
}
```

Requirements:
- Implements `DeepCopy() *PlanContext` and `Equal(*PlanContext) bool` for watchable compatibility.
- Jobs are deep-copied to prevent shared-memory issues.

**`PlanStatusUpdate`** — Output mutation intent:

```go
type PlanStatusUpdate struct {
    NamespacedName types.NamespacedName
    Mutate         func(*v1alpha1.HibernatePlanStatus)
}
```

**`ControllerResources`** — Input maps:

```go
type ControllerResources struct {
    PlanResources      watchable.Map[string, *PlanContext]
    ExceptionResources watchable.Map[string, *v1alpha1.ScheduleException]
}
```

**`ControllerStatuses`** — Output maps:

```go
type ControllerStatuses struct {
    PlanStatuses      watchable.Map[string, *PlanStatusUpdate]
    ExceptionStatuses watchable.Map[string, *ExceptionStatusUpdate]
}
```

Map key convention: `"namespace/name"` via `func Key(namespace, name string) string`.

#### 1.3 `internal/message/watchutil.go`

Adapt Envoy Gateway's `HandleSubscription` pattern:

```go
func HandleSubscription[K comparable, V any](
    log    logr.Logger,
    meta   Metadata,
    sub    <-chan watchable.Snapshot[K, V],
    handle func(Update[K, V], chan error),
)
```

Behavior:
1. First snapshot → iterate `snapshot.State`, call `handle` for each entry (bootstrap).
2. Subsequent snapshots → iterate `coalesceUpdates(snapshot.Updates)`, call `handle` per update.
3. Each `handle` invocation wrapped in `handleWithCrashRecovery` (panic catch → log stack → increment metrics → continue).
4. Error channel (buffered, size 10) logged by background goroutine.
5. Metrics: `watchable_subscribe_total`, `watchable_subscribe_duration_seconds`, `watchable_depth`.

`coalesceUpdates` iterates backwards through updates, keeping only the last update per key.

### Phase 2: Provider Layer — K8s → Watchable Maps

#### 2.1 `internal/provider/provider.go` — HibernatePlan Provider Reconciler

Single controller-runtime reconciler that replaces the K8s watch setup:

**Struct fields**: `Client`, `APIReader`, `Clock`, `Log`, `Scheme`, `Resources *message.ControllerResources`, `ScheduleEvaluator`, `RestoreManager`, `Planner`.

**`Reconcile(ctx, req)`**:
1. Fetch `HibernatePlan` by `req.NamespacedName`.
   - Not found → `r.Resources.PlanResources.Delete(key)`, return (triggers delete event in processors).
2. Fetch active `ScheduleException`s (label selector).
3. Fetch current-cycle `Job`s (label selector via `APIReader`).
4. Evaluate schedule: `r.ScheduleEvaluator.Evaluate(plan, exceptions, now)`.
5. Check `r.RestoreManager.HasRestoreData(plan)`.
6. Bundle into `PlanContext{Plan, Exceptions, Jobs, ScheduleResult, HasRestoreData}`.
7. `r.Resources.PlanResources.Store(key, planCtx)`.
8. Return `ctrl.Result{RequeueAfter: scheduleResult.RequeueAfter}` — preserves time-based schedule polling.

**`SetupWithManager()`**: Watches `HibernatePlan` (primary), `ScheduleException` (mapfunc → enqueue owning plan), `Job` (mapfunc → enqueue owning plan). Owns `Job` and `ConfigMap`. Same predicates as current setup.

**Key insight**: The provider is the **only** component that reads from the K8s API (besides the status writer). All processors read from watchable maps.

**Requeue during active execution**: When the plan is in `Hibernating` or `WakingUp` phase, the provider returns `RequeueAfter: 5s` to poll Job status updates. This drives the execution loop without requiring timers in processors.

#### 2.2 `internal/provider/exception_provider.go`

Thin reconciler for ScheduleException lifecycle events:
- Watches `ScheduleException` as primary resource.
- On reconcile: fetches exception, stores in `ExceptionResources`. If deleted → `Delete(key)`.
- Returns `RequeueAfter: 1m` for time-based state transitions (Pending→Active→Expired).

### Phase 3: Phase Processors — Business Logic

Each processor implements `Runner` interface (`Name() string`, `Start(ctx) error`) and `NeedLeaderElection() = true`.

#### 3.1 Lifecycle Processor (`internal/processor/plan/lifecycle.go`)

- **Subscribes**: `PlanResources.Subscribe(ctx)` (no phase filter — all plans).
- **On store** (new/updated plan):
  - No finalizer → add finalizer via K8s client, store updated plan in `PlanResources`.
  - `Phase == ""` → store `PlanStatusUpdate{Phase: Active, Mutate: initializeStatus}` in `PlanStatuses`.
- **On delete** (`update.Delete = true`):
  - Clean up owned Jobs (delete with label selector).
  - Remove finalizer.

#### 3.2 Schedule Processor (`internal/processor/plan/schedule.go`)

- **Subscribes**: `PlanResources.SubscribeSubset(ctx, func(k, v) bool { phase == Active || phase == Hibernated })`.
- **On store**: Reads pre-computed `PlanContext.ScheduleResult`:
  - Active + `ShouldHibernate=true` → store `PlanStatusUpdate{Phase: Hibernating, Mutate: initializeShutdown}`.
  - Hibernated + `!ShouldHibernate` + `HasRestoreData` → store `PlanStatusUpdate{Phase: WakingUp, Mutate: initializeWakeup}`.
  - `Spec.Suspend == true` → store transition to `Suspended`.
  - Otherwise → no-op (idempotent).
- **Optimistic update**: After queueing a phase transition, also store the updated `PlanContext` (with new phase) back into `PlanResources` so this processor's `SubscribeSubset` filter stops matching — preventing double-processing during the status-write round-trip.

#### 3.3 Hibernation Processor (`internal/processor/plan/hibernation.go`)

- **Subscribes**: `SubscribeSubset` where `Phase == Hibernating`.
- **On store**: Manages shutdown execution lifecycle:
  - Reads `PlanContext.Jobs` to determine per-target execution state.
  - Builds execution plan (reuses `scheduler.Planner`).
  - For pending targets with available concurrency slots: creates runner Jobs.
  - For completed stages: advances `CurrentStageIndex` via status update.
  - For all stages complete: stores `PlanStatusUpdate{Phase: Hibernated}`.
  - For failures in Strict mode: stores `PlanStatusUpdate{Phase: Error}`.
- Reuses logic from current `executeStage()` and `reconcileExecution()`, extracted into pure functions.

#### 3.4 WakeUp Processor (`internal/processor/plan/wakeup.go`)

- **Subscribes**: `SubscribeSubset` where `Phase == WakingUp`.
- Mirror of Hibernation Processor for wakeup operations.
- On all-complete: stores `PlanStatusUpdate{Phase: Active}`, triggers `cleanupAfterWakeUp`.
- Validates `HasRestoreData` before proceeding.

#### 3.5 Suspension Processor (`internal/processor/plan/suspension.go`)

- **Subscribes**: `SubscribeSubset` where `Phase == Suspended`.
- Checks `suspend-until` annotation deadlines.
- On resume (`Spec.Suspend == false`): evaluates whether forced wake-up is needed → stores appropriate phase transition.

#### 3.6 Error Recovery Processor (`internal/processor/plan/error_recovery.go`)

- **Subscribes**: `SubscribeSubset` where `Phase == Error`.
- Implements exponential backoff: `min(60s × 2^attempt, 30m)`.
- Checks `retry-now` annotation for manual intervention.
- Re-evaluates schedule to determine target phase (Hibernating or WakingUp).
- Relabels stale failed Jobs, resets failed targets to Pending.

#### 3.7 Exception Lifecycle Processor (`internal/processor/exception/lifecycle.go`)

- **Subscribes**: `ExceptionResources.Subscribe(ctx)`.
- Manages state transitions: Pending → Active → Expired.
- Stores status updates in `ExceptionStatuses`.

### Phase 4: Status Writer — Watchable Maps → K8s API

#### 4.1 `internal/processor/status/writer.go`

- **Subscribes** to both `PlanStatuses` and `ExceptionStatuses`.
- **For plan status updates**:
  1. Fetch fresh plan from K8s via `APIReader`.
  2. Apply `MutateFunc` to `plan.Status`.
  3. Write status sub-resource with `RetryOnConflict`.
  4. On success: delete entry from `PlanStatuses` (consumed).
  5. On permanent failure: log, delete entry.
  6. On transient failure: entry stays for retry on next snapshot.
- Reuses pattern from `internal/controller/status/updater.go`.
- Implements `NeedLeaderElection() = true`.

### Phase 5: Wiring — Composition Root

In `cmd/controller/app/app.go`, behind `--legacy-reconciler=false`:

1. Create shared `message.ControllerResources` and `message.ControllerStatuses` (zero-value watchable.Maps).
2. Create Provider Reconciler → `provider.SetupWithManager(mgr, ...)`.
3. Create Exception Provider → `exceptionProvider.SetupWithManager(mgr, ...)`.
4. Create all processors, each receiving pointers to their input/output maps.
5. Register processors via `mgr.Add(processor)`.
6. Streaming servers unchanged — registered separately.
7. Validation webhooks unchanged.

### Phase 6: Supporting Infrastructure

#### 6.1 Extracted Helpers (`internal/processor/plan/helpers.go`)

Pure functions extracted from current controller:
- `createRunnerJob()` — adapted from `helper.go`, takes explicit deps instead of receiver.
- `buildExecutionPlan()` — wraps `scheduler.Planner` calls.
- `getStageStatus()` — per-stage progress computation.
- `getDetailedErrorFromPod()` — termination message extraction.

#### 6.2 New Metrics

| Metric | Type | Labels | Purpose |
|--------|------|--------|---------|
| `watchable_subscribe_total` | Counter | runner, message, status | Per-handler invocation count |
| `watchable_subscribe_duration_seconds` | Histogram | runner, message | Handler processing time |
| `watchable_depth` | Gauge | runner, message | Subscription channel backlog |
| `watchable_panic_total` | Counter | runner | Panic recovery count |

Existing metrics (`reconcile_total`, `execution_duration_seconds`, etc.) wired into processors.

---

## Data Flow

Complete pipeline for a hibernation cycle:

```
K8s Event (HibernatePlan created/updated)
    │
    ▼
Provider Reconciler: fetches plan + exceptions + jobs, evaluates schedule
    │ Store()
    ▼
PlanResources [key: "ns/name", value: PlanContext{Phase: Active, ShouldHibernate: true}]
    │ SubscribeSubset (Phase == Active || Hibernated)
    ▼
Schedule Processor: sees ShouldHibernate=true
    │ Store()                          │ Store() (optimistic)
    ▼                                  ▼
PlanStatuses                     PlanResources [Phase: Hibernating]
    │ Subscribe()                      │ SubscribeSubset (Phase == Hibernating)
    ▼                                  ▼
Status Writer: writes               Hibernation Processor: creates runner Jobs
Phase=Hibernating to K8s               │
    │                                  │ Jobs run...
    ▼                                  ▼
K8s update → Provider Reconciler → re-fetches (jobs now Complete)
    │ Store()
    ▼
PlanResources [Phase: Hibernating, Jobs: [completed]]
    │ SubscribeSubset (Phase == Hibernating)
    ▼
Hibernation Processor: all stages done
    │ Store() PlanStatusUpdate{Phase: Hibernated}
    ▼
Status Writer → K8s → Provider → PlanResources [Phase: Hibernated]
    │ SubscribeSubset (Phase == Active || Hibernated)
    ▼
Schedule Processor: waits for wakeup (no-op until schedule flips)
```

---

## Idempotency Guarantees

Multiple layers of protection:

| Layer | Mechanism | Effect |
|-------|-----------|--------|
| **Watchable coalescing** | `coalesce` goroutine batches rapid Store() calls | Processors never see stale intermediate states |
| **HandleSubscription dedup** | `coalesceUpdates()` keeps only last update per key | Redundant updates within a batch are dropped |
| **SubscribeSubset DeepEqual** | No snapshot emitted if Store() doesn't change value | Re-storing identical PlanContext = no-op |
| **Phase routing** | SubscribeSubset filter by phase | Plan in phase X only processed by phase-X processor |
| **Optimistic phase update** | Processor updates PlanResources immediately | Prevents double-processing during status-write round-trip |
| **Status writer RetryOnConflict** | Fetches fresh plan before applying mutation | Handles concurrent writes safely |

---

## Execution Processor Polling Strategy

**Problem**: Execution processors (Hibernation/WakeUp) need to check Job progress even when no K8s events arrive.

**Solution**: The provider reconciler returns `RequeueAfter: 5s` during active execution. Each requeue triggers:
1. `Reconcile()` → fresh Job fetch → `Store()` in PlanResources
2. Execution processor receives updated `PlanContext.Jobs`
3. Processor evaluates stage progress and acts accordingly

No separate timers needed. During idle phases, the provider uses dynamic `RequeueAfter` from the schedule evaluator.

---

## Testing Strategy

### Unit Tests (per processor)

```go
func TestScheduleProcessor_ActiveToHibernating(t *testing.T) {
    resources := &message.ControllerResources{}
    statuses := &message.ControllerStatuses{}

    // Start processor
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go processor.Start(ctx)

    // Store a plan that should hibernate
    resources.PlanResources.Store("ns/test", &message.PlanContext{
        Plan:           planWithPhase(v1alpha1.PhaseActive),
        ScheduleResult: &message.ScheduleEvaluation{ShouldHibernate: true},
    })

    // Verify output
    sub := statuses.PlanStatuses.Subscribe(ctx)
    snapshot := <-sub
    assert.Equal(t, v1alpha1.PhaseHibernating, snapshot.State["ns/test"].Phase)
}
```

- No K8s client mocks needed for schedule/suspension/error processors.
- Hibernation/WakeUp processors need a mock for Job creation only.
- Status writer needs a mock K8s client.

### Integration Tests

Wire all processors with real watchable maps, verify full lifecycle:
Active → Hibernating → Hibernated → WakingUp → Active.

### HandleSubscription Tests

Verify coalescing, initial state bootstrap, panic recovery, metrics.

---

## Migration Plan

### Step 1: Foundation (no behavioral change)
- Add `watchable` dependency.
- Create `internal/message/` package.
- Add `--legacy-reconciler` flag (default: `true`).
- All existing tests pass unchanged.

### Step 2: Provider Layer
- Create `internal/provider/` with reconciler that populates watchable maps.
- Test in isolation — provider stores correct PlanContext for various scenarios.

### Step 3: Processors (one at a time)
- Start with Schedule Processor (simplest phase transition logic).
- Then Lifecycle Processor.
- Then Hibernation/WakeUp (most complex — job creation).
- Then Suspension, Error Recovery.
- Then Exception Lifecycle.
- Each processor tested independently.

### Step 4: Status Writer
- Create status writer processor.
- Integration test: provider + all processors + status writer.

### Step 5: Wiring
- Wire everything in `app.go` behind `--legacy-reconciler=false`.
- E2E test with `--legacy-reconciler=false`.

### Step 6: Graduation
- Once stable, flip default to `--legacy-reconciler=false`.
- Eventually remove `internal/controller/` (separate PR/release).

---

## Key Design Decisions

| Decision | Rationale |
|----------|-----------|
| **`watchable.Map` over plain channels** | Built-in coalescing, DeepEqual dedup, thread-safe snapshots, SubscribeSubset — exactly what phase-based routing needs. 414 lines of battle-tested code vs. building our own. |
| **Provider pre-computes schedule evaluation** | Keeps processors pure (act on pre-computed data) while maintaining the provider's requeue-based schedule polling. |
| **Processors update PlanResources optimistically** | Prevents double-processing during status-write round-trip. Eventual K8s write ensures persistence. |
| **Status writer as separate processor** | Isolates all K8s status I/O per architectural mandate (Rule 1: I/O Isolation). Enables batching and retry without blocking business logic. |
| **Single provider for all CRDs** | Consolidates K8s API interaction, eliminates redundant exception queries, provides single enrichment point. |
| **Feature flag with legacy default** | Zero risk to existing stable code. New architecture can be tested in parallel without affecting production users. |
| **Separate maps per CRD** | Each phase processor subscribes only to what it needs. Granular notification paths, no unnecessary wakeups. |
| **SubscribeSubset per phase** | True event-driven decoupling. Phase change triggers auto-filter: old processor sees "delete", new processor sees "store". No explicit handoff logic needed. |

---

## References

- [Envoy Gateway Architecture](https://github.com/envoyproxy/gateway) — Pipeline design inspiration
- [Envoy Gateway HandleSubscription](https://github.com/envoyproxy/gateway/blob/main/internal/message/watchutil.go) — Subscription handler pattern
- [telepresenceio/watchable](https://github.com/telepresenceio/watchable) — Pub/sub map library
- [RFC-0001: Hibernate Operator](../../enhancements/0001-hibernate-operator.md) — Core architecture reference
- [RFC-0006: Notification System](../../enhancements/0006-notification-system.md) — Future feature that benefits from async pipeline
