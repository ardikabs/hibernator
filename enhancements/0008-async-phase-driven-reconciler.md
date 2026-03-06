---
rfc: RFC-0008
title: Async Phase-Driven Reconciler
status: In Progress
date: 2026-03-05
---

# RFC 0008 — Async Phase-Driven Reconciler

**Keywords:** Architecture, AsyncReconciler, WatchablePipeline, PhaseProcessors, Provider, Worker, Coordinator, LegacyMigration, FeatureFlag, StatusWriter, IdempotencyGuarantees

**Status:** In Progress 🚀

---

## Table of Contents

- [Summary](#summary)
- [Motivation](#motivation)
- [Honest Alternatives Assessment](#honest-alternatives-assessment)
- [Goals](#goals)
- [Non-Goals](#non-goals)
- [Proposal](#proposal)
  - [Architecture Overview](#architecture-overview)
  - [Feature Flag Strategy](#feature-flag-strategy)
  - [Detailed Design](#detailed-design)
    - [Phase 1: Foundation — Message Bus & Utilities](#phase-1-foundation--message-bus--utilities)
    - [Phase 2: Provider Layer](#phase-2-provider-layer)
    - [Phase 3: Phase Processors — Business Logic](#phase-3-phase-processors--business-logic)
    - [Phase 4: Status Writer](#phase-4-status-writer)
    - [Phase 5: Wiring — Composition Root](#phase-5-wiring--composition-root)
    - [Phase 6: Supporting Infrastructure](#phase-6-supporting-infrastructure)
  - [Data Flow](#data-flow)
  - [Idempotency Guarantees](#idempotency-guarantees)
  - [Execution Processor Polling Strategy](#execution-processor-polling-strategy)
  - [Key Design Decisions](#key-design-decisions)
- [Implementation](#implementation)
  - [Decisions Made](#decisions-made)
  - [Corrections Applied](#corrections-applied)
  - [Additional Changesets](#additional-changesets)
- [Code Review Findings](#code-review-findings)
  - [What's Done Well](#whats-done-well)
  - [Issues & Resolutions](#issues--resolutions)
- [Implementation Status](#implementation-status)
  - [Readiness Assessment](#readiness-assessment)
  - [Open Items](#open-items)
- [Testing Strategy](#testing-strategy)
- [Migration Plan](#migration-plan)
- [References](#references)

---

## Summary

This RFC proposes replacing the synchronous HibernatePlan reconciler with an asynchronous, phase-driven processing pipeline inspired by [Envoy Gateway](https://github.com/envoyproxy/gateway). The new architecture uses a `watchable.Map` pub/sub bus to decouple K8s API observation (Provider) from business logic (Phase Processors) and K8s status writes (Status Writer). A feature flag (`--legacy-reconciler`) preserves the existing stable code path untouched.

---

## Motivation

The current HibernatePlan reconciler (`internal/controller/hibernateplan/controller.go`) is an 826-line monolithic function built on a synchronous mental model. While functional, it creates several long-term maintenance problems:

1. **Phase transition logic is interleaved with I/O**: A single `Reconcile()` call evaluates schedules, creates Jobs, queries Job status, writes status updates, and handles error recovery — all inline.
2. **No hook points**: Introducing cross-cutting behavior (e.g., pre-transition webhooks, runner progress integration, notification dispatch, audit logging) requires modifying the core reconcile loop.
3. **Execution plan rebuilt every reconcile**: The execution plan (DAG/stages) is not persisted — it's recomputed from spec on every reconcile cycle, making behavior undefined if spec changes mid-operation.
4. **Duplicate queries**: Active ScheduleExceptions are fetched twice per reconcile (once in `updateActiveExceptions`, again in `evaluateSchedule`).
5. **Job polling is requeue-based**: The reconciler requeues every 5–10s during execution to poll Job status — mixing time-based scheduling with event-driven reconciliation.
6. **Testing difficulty**: The reconciler is tightly coupled to the K8s API client, making unit tests require full envtest infrastructure.

As the project grows (notifications, multi-cloud executors, approval workflows, advanced scheduling), these problems compound — each new feature adds branches to the monolith.

---

## Honest Alternatives Assessment

Before committing to the watchable pub/sub architecture, simpler alternatives were evaluated. The goal was to pick the **simplest approach that genuinely solves the maintenance problem** while supporting future extensibility.

### Alternative 1: Phase Handler Registry (Strategy Pattern)

Decompose `Reconcile()` into registered phase handlers, each implementing `Handle(ctx, plan) (Result, error)`. The reconciler becomes a thin dispatcher.

| Criterion | Rating | Notes |
|-----------|--------|-------|
| Complexity | ★☆☆ Very Low | No new deps, familiar pattern |
| Phase isolation | ★★☆ Good | Each phase in its own file/struct |
| Hook points | ★★☆ Moderate | Sync hooks only |
| Testability | ★★☆ Moderate | Still needs K8s client mocks |
| Async extensibility | ★☆☆ None | Still synchronous |
| Addresses root cause | ★☆☆ Partially | Organizes code but doesn't change execution model |

**Verdict**: Good first step for code organization, but doesn't solve the core architectural issue.

### Alternative 2: State Machine Library (e.g., `looplab/fsm`)

Formalize phase transitions with before/after callbacks.

| Criterion | Rating | Notes |
|-----------|--------|-------|
| Phase isolation | ★★★ Excellent | Explicit state graph |
| Async extensibility | ★☆☆ None | Callbacks are synchronous by design |
| Addresses root cause | ★★☆ Partially | Formalizes but doesn't decouple I/O from logic |

**Verdict**: Excellent for formalizing the state graph but callbacks are synchronous — adding a notification webhook would block the reconcile loop.

### Alternative 3: Event-Driven with Go Channels (No External Deps)

Build a custom pub/sub layer using Go channels.

| Criterion | Rating | Notes |
|-----------|--------|-------|
| Complexity | ★★★ High | Must build coalescing, snapshot diffing, thread safety, cleanup |
| Async extensibility | ★★★ Excellent | Full async model |
| Addresses root cause | ★★★ Fully | Same architecture, just hand-rolled |

**Verdict**: Achieves the same result as the watchable approach but requires ~500 lines of concurrent infrastructure code that `watchable` already provides, battle-tested by Telepresence/Envoy Gateway.

### Alternative 4: Hybrid — Strategy Pattern + Async Hooks

Sync phase handlers for core reconcile flow plus a separate event channel for cross-cutting async concerns.

**Verdict**: Pragmatic middle ground, but creates two execution models that must stay consistent.

### Alternative 5: Watchable Pub/Sub Pipeline (Chosen)

| Criterion | Rating | Notes |
|-----------|--------|-------|
| Complexity | ★★★ High | New dependency, new mental model, more goroutines |
| Phase isolation | ★★★ Excellent | SubscribeSubset per phase — true decoupling |
| Hook points | ★★★ Excellent | Any processor can subscribe to any map |
| Testability | ★★★ Excellent | Processors are pure functions of map events; no K8s mocks needed |
| Idempotency | ★★★ Excellent | Built-in: coalescing + DeepEqual + phase routing |
| Async extensibility | ★★★ Excellent | Adding a notification processor = just another subscriber |
| Addresses root cause | ★★★ Fully | Completely replaces the synchronous polling model |

### Decision

**Given the project roadmap** (notifications RFC-0006, multi-cloud executors, advanced scheduling exceptions, CLI integration, potential approval workflows), this RFC proceeds with **Alternative 5**. The single-dependency cost (`watchable` is ~414 lines of code, MIT licensed, stable for 4+ years) is negligible compared to the architectural flexibility gained.

---

## Goals

- Replace the synchronous HibernatePlan reconciler with an async, phase-driven pipeline.
- Zero behaviour change for existing users — legacy code path remains as default behind a feature flag.
- Phase processors are independently testable without K8s client mocks.
- Cross-cutting concerns (notifications, audit) can be added as new subscribers — no modifications to core processors.
- All K8s status I/O is isolated to a single, dedicated Status Writer processor.
- Full hibernation → wakeup lifecycle works correctly under the new pipeline.

## Non-Goals

- Remove or deprecate `internal/controller/` in this RFC (separate future PR).
- Implement new executor types or new CRDs.
- Change any observable user-facing API or behaviour.

---

## Proposal

### Architecture Overview

Replace the synchronous HibernatePlan reconciler with an asynchronous pipeline:

```markdown
                    ┌──────────────────────────────────────────────┐
                    │              Controller Manager              │
                    └──────────────────────────────────────────────┘
                                            │
                    ┌───────────────────────┼───────────────────────┐
                    ▼                       ▼                       ▼
          ┌─────────────────┐    ┌──────────────────┐    ┌──────────────────┐
          │ Provider        │    │ Streaming Servers│    │ Webhooks         │
          │ Reconciler      │    │ (gRPC/WebSocket) │    │ (unchanged)      │
          │ (K8s watcher)   │    │ (unchanged)      │    │                  │
          └────────┬────────┘    └──────────────────┘    └──────────────────┘
                   │ Store()
                   ▼
    ┌──────────────────────────────────────────────────┐
    │              Watchable Maps (Message Bus)        │
    │                                                  │
    │  PlanResources    ExceptionResources             │
    │  watchable.Map    watchable.Map                  │
    │  [ns/name →       [ns/name →                     │
    │   PlanContext]      ScheduleException]           │
    └──────────┬───────────────────────────────────────┘
               │ SubscribeSubset (per phase)
               ▼
    ┌─────────────────────────────────────────────────────────────────────────┐
    │                         Coordinator + Workers                           │
    │                                                                         │
    │   Coordinator spawns one Worker goroutine per HibernatePlan.            │
    │   Each Worker runs a state-machine loop over its plan's lifecycle.      │
    │                                                                         │
    │  ┌───────────────────────────────────────────────────────────────────┐  │
    │  │  Worker per plan (actor model)                                    │  │
    │  │  select { slot.ready | pollTimer | retryTimer | deadlineTimer |   │  │
    │  │           idleTimer }                                             │  │
    │  │                                                                   │  │
    │  │  State handlers:                                                  │  │
    │  │  ┌──────────┐ ┌──────────┐ ┌──────────────┐ ┌──────────────────┐  │  │
    │  │  │lifecycle  │ │idle/sched│ │hibernating   │ │wakingUp         │  │  │
    │  │  └──────────┘ └──────────┘ └──────────────┘ └──────────────────┘  │  │
    │  │  ┌──────────┐ ┌──────────┐                                        │  │
    │  │  │suspended │ │error     │                                        │  │
    │  │  └──────────┘ └──────────┘                                        │  │
    │  └───────────────────────────────────────────────────────────────────┘  │
    │                                                                         │
    │  ┌──────────────────────────────────┐                                   │
    │  │ Exception Lifecycle Processor    │                                   │
    │  │ (subscribes ExceptionResources)  │                                   │
    │  └──────────────────────────────────┘                                   │
    └──────────┬──────────────────────────────────────────────────────────────┘
               │ PlanStatuses.Send() / ExceptionStatuses.Send()
               ▼
    ┌──────────────────────────────────────────────────┐
    │              Status Maps (Output)                │
    │                                                  │
    │  PlanStatuses         ExceptionStatuses          │
    │  StatusQueue          StatusQueue                │
    └──────────┬───────────────────────────────────────┘
               │ Subscribe()
               ▼
    ┌──────────────────────────────────────────────────┐
    │              Status Writer Processor             │
    │              (K8s status sub-resource writes)    │
    └──────────────────────────────────────────────────┘
```

> **Implementation note**: The original design described per-phase `SubscribeSubset` processors. The implementation wisely consolidated into a **Coordinator + Worker actor model** — a Coordinator subscribes to the PlanResources map and spawns one Worker goroutine per HibernatePlan. Each Worker runs a state-machine loop. This eliminates the sequential `HandleSubscription` bottleneck and makes the state machine natural, since each plan's lifecycle runs in a single goroutine with no phase-routing concurrency hazards.

**Core principle**: A single **Provider Reconciler** watches all K8s resources and feeds `watchable.Map` instances (the internal message bus). **Workers** execute the per-plan state machine. A **Status Writer** subscribes to an output map and batch-writes to the K8s API.

**Key properties**:

- Phase = routing key → natural idempotency
- Workers are independent goroutines → async by design
- Adding new behavior = adding a new subscriber → Open/Closed Principle
- Business logic never calls the K8s API directly → I/O isolation per architectural mandate

---

### Feature Flag Strategy

To preserve all existing stable code untouched, the new architecture lives alongside the current reconcilers behind a feature flag:

```bash
--legacy-reconciler=true   (default)  → loads existing reconcilers from internal/controller/
--legacy-reconciler=false              → loads new pipeline from internal/provider/ + internal/provider/processor/
```

**Implementation in `cmd/controller/app/app.go`**:

```go
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
    // ... register processor via mgr.Add()
}
```

### Directory Structure

```bash
internal/
├── controller/                         # UNTOUCHED — existing reconcilers
│   ├── hibernateplan/
│   ├── scheduleexception/
│   └── status/
├── message/                            # NEW — watchable map types + HandleSubscription
│   ├── types.go
│   └── watchutil.go
├── provider/                           # NEW — K8s → watchable maps + phase processors
│   ├── provider_hibernateplan.go       # HibernatePlan provider reconciler
│   ├── provider_scheduleexception.go   # ScheduleException provider reconciler
│   └── processor/
│       ├── plan/
│       │   ├── coordinator.go          # Spawns per-plan Workers, subscribes PlanResources
│       │   ├── worker.go               # Per-plan goroutine + state-machine event loop
│       │   ├── state/
│       │   │   ├── state.go            # Config, dispatch, patchPreservingStatus, updateExecutionStatuses
│       │   │   ├── state_lifecycle.go  # handleInit, handleDelete, AddFinalizer, RemoveFinalizer
│       │   │   ├── state_idle.go       # idle state: schedule eval → transition to Hibernating/WakingUp
│       │   │   ├── state_execution.go  # hibernating/wakingUp execution loop
│       │   │   ├── state_suspended.go  # resume, forceWakeUpOnResume, deadline handling
│       │   │   ├── state_recovery.go   # error recovery with exponential backoff
│       │   │   └── utils.go            # FetchCurrentCycleJobs, snapshotExecutionStates, pruneCycleHistory
│       │   └── helpers.go              # Pure helpers (createRunnerJob, buildExecutionPlan, getStageStatus)
│       ├── scheduleexception/
│       │   └── lifecycle.go            # Exception state machine processor
│       └── status/
│           └── writer.go               # Dedicated K8s status write processor
├── metrics/                            # Extended with worker/watchable metrics
└── [all others untouched]
```

**Zero changes to existing code** — the `internal/controller/` package and all its dependencies remain exactly as they are.

---

### Detailed Design

#### Phase 1: Foundation — Message Bus & Utilities

**`internal/message/types.go`** — defines shared watchable map types:

**`PlanContext`** — enriched snapshot of a HibernatePlan bundled with related data:

```go
type PlanContext struct {
    Plan             *v1alpha1.HibernatePlan
    Exceptions       []v1alpha1.ScheduleException
    Jobs             []batchv1.Job
    ScheduleResult   *ScheduleEvaluation
    HasRestoreData   bool
}

type ScheduleEvaluation struct {
    ShouldHibernate bool
    RequeueAfter    time.Duration
}
```

- Implements `DeepCopy() *PlanContext` and `Equal(*PlanContext) bool` for watchable compatibility.
- Jobs are deep-copied to prevent shared-memory issues.

**`PlanStatusUpdate`** — output mutation intent (function closure over the plan status):

```go
type PlanStatusUpdate struct {
    NamespacedName types.NamespacedName
    Mutate         func(*v1alpha1.HibernatePlanStatus)
}
```

**`ControllerResources`** — input maps:

```go
type ControllerResources struct {
    PlanResources      watchable.Map[string, *PlanContext]
    ExceptionResources watchable.Map[string, *v1alpha1.ScheduleException]
}
```

**`ControllerStatuses`** — output maps (backed by `StatusQueue` for drop-on-full semantics):

```go
type ControllerStatuses struct {
    PlanStatuses      *StatusQueue[*PlanStatusUpdate]
    ExceptionStatuses *StatusQueue[*ExceptionStatusUpdate]
}
```

Map key convention: `"namespace/name"` via `func Key(namespace, name string) string`.

**`internal/message/watchutil.go`** — adapts Envoy Gateway's `HandleSubscription` pattern:

1. First snapshot → iterate `snapshot.State`, call `handle` for each entry (bootstrap).
2. Subsequent snapshots → iterate `coalesceUpdates(snapshot.Updates)`, call `handle` per update.
3. Each `handle` invocation wrapped in `handleWithCrashRecovery` (panic catch → log stack → increment metrics → continue).
4. `errChan` (buffered, size 10) logged by background goroutine.

`coalesceUpdates` iterates backwards through updates, keeping only the last update per key.

---

#### Phase 2: Provider Layer — K8s → Watchable Maps

**`internal/provider/provider_hibernateplan.go`** — single controller-runtime reconciler that replaces the K8s watch setup:

**`Reconcile(ctx, req)`**:
1. Fetch `HibernatePlan` by `req.NamespacedName`.
   - Not found → delete from `PlanResources` (triggers delete handling in Worker).
2. Fetch active `ScheduleException`s (label selector).
3. Fetch current-cycle `Job`s via `APIReader` (bypasses informer cache).
4. Evaluate schedule → `ScheduleEvaluation{ShouldHibernate, RequeueAfter}`.
5. Check `RestoreManager.HasRestoreData(plan)`.
6. Bundle into `PlanContext`, store in `PlanResources`.
7. Return `ctrl.Result{RequeueAfter: scheduleResult.RequeueAfter}`.

**Predicates**: `GenerationChangedPredicate | AnnotationChangedPredicate` on HibernatePlan breaks the status-write feedback loop. `configMapDataChangedPredicate` filters annotation-only writes on restore ConfigMaps.

**`internal/provider/provider_scheduleexception.go`** — thin reconciler for ScheduleException lifecycle events:

- On reconcile: fetches exception, stores in `ExceptionResources`. If deleted → `Delete(key)`.
- Returns `RequeueAfter: 1m` for time-based state transitions (Pending→Active→Expired).

> **Key insight**: The provider is the **only** component that reads from the K8s API (besides the status writer). All Workers read from watchable maps.

---

#### Phase 3: Phase Processors — Business Logic

**`internal/provider/processor/plan/coordinator.go`** — subscribes to `PlanResources`, spawns and manages one Worker per HibernatePlan.

**`internal/provider/processor/plan/worker.go`** — per-plan goroutine with five-case select loop:

```go
select {
case ctx := <-slot.ready:     // new PlanContext from watchable map
case <-pollTimer.C:           // 5s execution polling
case <-retryTimer.C:          // error recovery backoff
case <-deadlineTimer.C:       // suspension deadline expiry
case <-idleTimer.C:           // 30m idle reaping
}
```

Worker delegates to state handlers via `buildConfig()` + `dispatch()`:

- **`state_lifecycle.go`**: Finalizer management, plan initialization, deletion cleanup.
- **`state_idle.go`**: Active/Hibernated → evaluates pre-computed `ScheduleResult`; transitions to Hibernating or WakingUp.
- **`state_execution.go`**: Hibernating/WakingUp → manages runner Job lifecycle (create, poll, advance stages, finalize).
- **`state_suspended.go`**: Suspended → checks annotation deadlines, handles resume and force-wakeup.
- **`state_recovery.go`**: Error → exponential backoff retry (`min(60s × 2^attempt, 30m)`), manual `retry-now` annotation.

**`internal/provider/processor/scheduleexception/lifecycle.go`** — subscribes to `ExceptionResources`, manages Pending→Active→Expired transitions, stores status updates in `ExceptionStatuses`.

---

#### Phase 4: Status Writer — Watchable Maps → K8s API

**`internal/provider/processor/status/writer.go`**:

- Subscribes to both `PlanStatuses` and `ExceptionStatuses` queues.
- For plan status updates:
  1. Fetch fresh plan from K8s via `APIReader`.
  2. Apply `Mutate` closure to `plan.Status`.
  3. Write status sub-resource with `RetryOnConflict`.
  4. On success: item consumed from queue.
  5. Guard: `isPlanStatusEqual` (using `cmp.Equal` with `cmpopts.IgnoreFields`) skips no-op writes.
- 10 plan workers + 5 exception workers (bounded parallelism).
- `drain()` on shutdown: flushes buffered updates with a 5s background-context deadline.

---

#### Phase 5: Wiring — Composition Root

In `cmd/controller/app/app.go`, behind `--legacy-reconciler=false`:

1. Create shared `message.ControllerResources` and `message.ControllerStatuses`.
2. Create and register Provider reconcilers via `SetupWithManager`.
3. Create Coordinator processor → `mgr.Add()`.
4. Create Exception Lifecycle processor → `mgr.Add()`.
5. Create Status Writer processor → `mgr.Add()`.
6. Streaming servers and validation webhooks unchanged.

---

#### Phase 6: Supporting Infrastructure

**`internal/provider/processor/plan/helpers.go`** — pure functions extracted from legacy controller:
- `createRunnerJob()` — adapted from `helper.go`, takes explicit deps.
- `buildExecutionPlan()` — wraps `scheduler.Planner` calls.
- `getStageStatus()` — per-stage progress computation.
- `getDetailedErrorFromPod()` — termination message extraction.

**New Metrics** (added to `internal/metrics/metrics.go`):

| Metric | Type | Labels | Purpose |
|--------|------|--------|---------|
| `hibernator_watchable_subscribe_total` | Counter | runner, message | Per-handler invocation count |
| `hibernator_watchable_subscribe_duration_seconds` | Histogram | runner, message | Handler processing time |
| `hibernator_status_queue_dropped_total` | Counter | — | StatusQueue overflow visibility |
| `hibernator_worker_goroutines` | Gauge | — | Live Worker goroutine count |

---

### Data Flow

Complete pipeline for a hibernation cycle:

```bash
K8s Event (HibernatePlan created/updated)
    │
    ▼
Provider Reconciler: fetches plan + exceptions + jobs, evaluates schedule
    │ PlanResources.Store()
    ▼
Coordinator: delivers PlanContext to plan's Worker via planContextSlot
    │
    ▼
Worker: slot.ready fires → mergeIncoming() → buildConfig() → dispatch()
    │
    ▼
idleState: ShouldHibernate=true → transitionToHibernating()
    │ PlanStatuses.Send(Phase=Hibernating)
    ▼
Status Writer: fetches fresh plan, writes Phase=Hibernating to K8s
    │
    ▼
K8s event → Provider → PlanResources.Store(Phase=Hibernating)
    │
    ▼
Worker: slot.ready fires → hibernatingState → createRunnerJobs()
    │ (5s pollTimer ticks...)
    ▼
Provider: re-fetches jobs (now Complete) → PlanResources.Store()
    │
    ▼
Worker: hibernatingState → all stages done → transitionToHibernated()
    │ PlanStatuses.Send(Phase=Hibernated)
    ▼
Status Writer → K8s → Provider → Worker: idleState (waits for wakeup schedule)
```

---

### Idempotency Guarantees

Multiple layers of protection:

| Layer | Mechanism | Effect |
|-------|-----------|--------|
| **Watchable coalescing** | `coalesce` goroutine batches rapid Store() calls | Workers never see stale intermediate states |
| **HandleSubscription dedup** | `coalesceUpdates()` keeps only last update per key | Redundant updates within a batch are dropped |
| **SubscribeSubset DeepEqual** | No snapshot emitted if Store() doesn't change value | Re-storing identical PlanContext = no-op |
| **Status writer RetryOnConflict** | Fetches fresh plan before applying mutation | Handles concurrent writes safely |
| **`isPlanStatusEqual` guard** | `cmp.Equal` with IgnoreFields | Skips no-op status writes |
| **`mergeIncoming()`** | Carries forward optimistic Status on watchable delivery | Prevents informer-lag from reverting in-memory mutations |
| **`reconcileTruth()`** | Every 5th poll cycle, fetch from APIReader | Bounds optimistic/persisted divergence to ~25s |

---

### Execution Processor Polling Strategy

Execution Workers (hibernatingState/wakingUpState) check Job progress via the provider's `RequeueAfter: 5s` during active execution. Each requeue triggers:
1. `Reconcile()` → fresh Job fetch via `APIReader` → `PlanResources.Store()`
2. Worker receives updated `PlanContext.Jobs` via `slot.ready`
3. Worker evaluates stage progress and acts accordingly

No separate timers needed in Workers during execution. During idle phases, the provider uses dynamic `RequeueAfter` from the schedule evaluator.

---

### Key Design Decisions

| Decision | Rationale |
|----------|-----------|
| **Coordinator + Worker actor model** | Original design used per-phase `SubscribeSubset` processors. Consolidated into one Worker per plan to eliminate sequential `HandleSubscription` bottleneck and make the state machine natural. |
| **`planContextSlot` (latest-wins slot)** | Non-blocking single-value channel avoids queue buildup. Mutex + separate signal channel is correct for "latest overwrites" semantic. |
| **`patchPreservingStatus()`** | controller-runtime's `Patch()` deserialises the API response back into the live object, overwriting Status. Snapshot + restore preserves optimistic mutations. Applied at all 7 Patch call sites in state handlers. |
| **`mergeIncoming()`** | On every watchable delivery, accept incoming Spec/ObjectMeta/provider fields but carry forward optimistic `plan.Status`. Prevents informer-lag from reverting pending phase transitions. |
| **`reconcileTruth()`** | Every 5th poll cycle, fetch from `APIReader` and replace cached plan on divergence. Bounds the optimistic-vs-persisted divergence window without new plumbing. |
| **Worker idle reaping** | `workerIdleTimeout = 30m` + fifth select case. Prevents O(plans) goroutines for large deployments. |
| **`StatusQueue` drop-on-full** | Combined with `isPlanStatusEqual` guard and `RetryOnConflict`, dropped updates are safe — the next poll cycle re-derives current state. `StatusQueueDroppedTotal` metric provides visibility. |
| **`updateExecutionStatuses` drift detection** | Snapshot execution states before mutation; only `Send()` when drift is detected. Eliminates redundant writes on poll ticks where jobs haven't progressed. |
| **`forceWakeUpOnResume` via Hibernated dispatch** | Routes to `idleState` which calls canonical `transitionToWakingUp()` — correctly initialises fresh Executions, new CycleID, StageIndex=0. Avoids stale execution state from pre-suspension cycle. |
| **Provider pre-computes schedule evaluation** | Keeps Workers pure (act on pre-computed data) while maintaining the provider's requeue-based schedule polling. |
| **Feature flag with legacy default** | Zero risk to existing stable code. New architecture can be tested in parallel without affecting production users. |

---

## Implementation

### Decisions Made

The following key decisions were made during the initial implementation on 2026-03-01:

1. **Feature Flag Default**: `--legacy-reconciler=true` — existing reconcilers remain the default. New pipeline loads only with `--legacy-reconciler=false`.

2. **Coordinator + Worker over per-phase processors**: Consolidated from the design doc's original processor-per-phase with `SubscribeSubset`. Eliminates sequential `HandleSubscription` bottleneck and makes the state machine natural.

3. **Zero Modification to Existing Code**: All files under `internal/controller/` remain untouched. The new pipeline lives entirely in new packages.

4. **`watchable` Library Usage**: Used `github.com/telepresenceio/watchable` as the message bus. Map key convention: `"namespace/name"`.

5. **Processor Registration**: Each processor implements `manager.Runnable` via `Start(ctx) error` and `NeedLeaderElection() bool`. Registered via `mgr.Add()`.

6. **Status Writer Pattern**: Dedicated processor subscribes to output queues and performs all K8s status writes with `RetryOnConflict`. Items are consumed (dequeued) after successful write.

7. **Job Creation Ownership**: hibernatingState and wakingUpState create runner Jobs directly using the K8s client (passed as dependency). The provider re-fetches Jobs on each reconcile to feed Workers.

8. **Schedule Evaluation in Provider**: The provider pre-computes `ScheduleEvaluation` so that Workers act on pre-computed data without needing access to the `ScheduleEvaluator`. This keeps Workers I/O-isolated.

9. **`errChan` Error Propagation**: `HandleSubscription` creates a buffered `errChan` with a consumer goroutine that logs errors. Two-category model:
   - **Infrastructure errors** (K8s API failures) → `errChan`
   - **Plan execution errors** (strict-mode failures) → `setError()` only (emitted as plan status error message)

### Corrections Applied

Build verification on 2026-03-01 identified the following corrections:

| # | Issue | Fix |
|---|-------|-----|
| 1 | HandleSubscription callback signature | `func(watchable.Update[K,V])` (single update), not `func(*Snapshot)` |
| 2 | `scheduler.StageDef` does not exist | Corrected to `scheduler.Stage`; `PlanStaged()` takes `([]Stage, int32)` |
| 3 | `strategy.MaxConcurrency` is `*int32` | Added nil checks and dereference throughout `BuildExecutionPlan()` |
| 4 | `metav1.Time` vs custom Time type | ScheduleException uses standard `metav1.Time`; fixed in exception lifecycle processor |
| 5 | Missing `Clock` field on `LifecycleProcessor` | Added `Clock clock.Clock` field |
| 6 | Missing imports | Added `watchable`, `k8s.io/utils/clock` imports |

### Additional Changesets

**2026-03-01 — Async Error Propagation (RunnerErrors)** — Introduced then reverted.

An `ErrorNotifier`/`RunnerErrors` watchable-map pattern (adapted from Envoy Gateway) was introduced to propagate critical async errors from processors. After evaluation, this was removed entirely. `HandleSubscription`'s `errChan` pattern provides sufficient visibility, and the additional complexity was not justified. Reversion confirmed by `go build ./...` pass.

**2026-03-01 — Systematic `errChan` Wiring**

After refactoring `HandleSubscription` to pass `errChan chan error` to handlers, `errChan` was wired across all 8 processors:

| Processor | Infrastructure Errors Wired |
|-----------|---------------------------|
| `lifecycle.go` | Finalizer add/remove, restore point, job list errors |
| `schedule.go` | `transitionToSuspended` failure |
| `suspension.go` | Auto-resume patch failure |
| `hibernation.go` | `CreateRunnerJob` failure, `BuildExecutionPlan` failure |
| `wakeup.go` | `CreateRunnerJob` failure, `BuildExecutionPlan` failure |
| `error_recovery.go` | Annotation clear, BuildExecutionPlan rebuild, stale job list failures |
| `exception/lifecycle.go` | Finalizer add/remove, plan label, `removeFromPlanStatus` |
| `status/writer.go` | Removed redundant `log.Error` (errChan consumer already logs) |

---

## Code Review Findings

Scope: ~5,300 new lines across 29 files. Branch `feat/async-reconciler`, single commit `ffa2f03`, review date 2026-03-04.

### What's Done Well

1. **`planContextSlot` (Latest-Wins Slot)**: The non-blocking single-value channel pattern avoids queue buildup. The mutex + separate signal channel is correct for the "latest overwrites" semantic.

2. **`StatusQueue` with Drop-on-Full**: Combined with the status writer's `isPlanStatusEqual` guard and `RetryOnConflict`, dropped updates are genuinely safe.

3. **Feature Flag Isolation**: The `--legacy-reconciler` flag with clean branching in `cmd/controller/app/app.go` means zero risk to the existing stable code path.

4. **Provider Predicates**: `GenerationChangedPredicate | AnnotationChangedPredicate` correctly breaks the status-write feedback loop. `configMapDataChangedPredicate` filtering annotation-only writes on restore ConfigMaps prevents spurious reconciles during wakeup.

5. **State Handlers as Composition Over Inheritance**: The `Config` struct with closure-based timer control cleanly separates the Worker's internal state from the Handler's logic. Handlers never know about Worker internals.

6. **`isPlanStatusEqual` Using `cmp.Equal`**: Superior to hand-rolled comparison — readable, maintainable, explicitly documents which fields are semantic vs. bookkeeping.

7. **`patchPreservingStatus()` and `mergeIncoming()`**: Two targeted mechanisms to preserve optimistic status mutations — one against Patch response deserialisation, one against informer-lag watchable delivery.

### Issues & Resolutions

#### Critical

| ID | Issue | Status |
|----|-------|--------|
| C1 | `ScheduleExceptionProcessor.removeFromPlanStatus` directly called `p.Status().Update()` — bypasses the Status Writer and violates the core architectural invariant | ✅ Fixed: Rewrote to queue via `PlanStatuses.Send()` with mutation closure |
| C2 | No Job watches in Provider — event detection relies solely on the 5s poll timer (0–5s detection lag) | ⏳ Deferred: Functional with poll timer; `.Owns(&batchv1.Job{})` can be added as a performance enhancement |
| C3 | `handleDelete` received potentially stale exception from watchable Delete event — finalizer patch had no retry | ✅ Fixed: Re-fetch from `APIReader` at entry; finalizer patch wrapped in `RetryOnConflict` with re-fetch per iteration |

#### High

| ID | Issue | Status |
|----|-------|--------|
| H1 | Worker goroutines never reaped for idle plans — O(plans) goroutines at scale | ✅ Fixed: `workerIdleTimeout = 30m` + fifth idle `select` case + `reap()` callback |
| H2 | Potential hot loop from ResourceVersion-based equality | ✅ Not a Bug: `planPredicate` (`GenerationChangedPredicate \| AnnotationChangedPredicate`) filters status-only updates at informer level |
| H3 | No divergence detection between optimistic and persisted state | ✅ Fixed: `reconcileTruth()` every 5th poll cycle |
| H4 | No graceful drain of `StatusQueue` on shutdown | ✅ Fixed: `Writer.drain()` with 5s background-context deadline |
| H5 | `FetchCurrentCycleJobs` used cached client (informer lag) instead of `APIReader` | ✅ Fixed: `APIReader` threaded through provider → Coordinator → Worker → Config → `FetchCurrentCycleJobs` |

#### Medium

| ID | Issue | Status |
|----|-------|--------|
| M1 | `dispatch()` creates potential unbounded call-depth | Open (Low Risk): Max depth ~4 in practice; chains are terminating by construction |
| M2 | `handleRetry` doesn't reset `CurrentStageIndex` | ✅ Not a Bug: Retry-from-current-stage is correct; only `handleManualRetry` resets to stage 0 |
| M3 | Exception `LifecycleProcessor` is single-threaded — may bottleneck at scale | Open (Low Risk): Bottleneck unlikely below ~100 concurrent exceptions |
| M4 | No worker goroutine count metric | ✅ Fixed: `WorkerGoroutinesGauge` added in `metrics.go` |
| M5 | `buildConfig()` allocates new `Config` on every `handle()` call | ✅ Fixed: `cachedConfig` lazily initialised on first call, only `PlanCtx` updated per call |

#### Low / Nits

| ID | Issue | Status |
|----|-------|--------|
| L1 | `forceWakeUpOnResume` transitioned directly to `PhaseWakingUp` with stale execution state | ✅ Fixed: Routes to `PhaseHibernated` + `dispatch()` → `idleState` → canonical `transitionToWakingUp()` |
| L2 | Magic number `5` in `pruneCycleHistory` | ✅ Fixed: Extracted as `maxCycleHistorySize = 5` |
| L3 | `mapsEqual` reimplements `maps.Equal` (stdlib since Go 1.21) | ✅ Fixed: Replaced with `maps.Equal` |
| L4 | Design doc directory structure doesn't match implementation | Open (Cosmetic): `internal/processor/plan/...` → actual: `internal/provider/processor/plan/...` |
| L5 | Missing rapid-coalescing integration test in `watchutil_test.go` | Open (Nice-to-have) |

#### Post-Review Fixes (PR)

Four additional fixes closed race conditions in the optimistic status pipeline that would have been difficult to diagnose in production:

| ID | Fix |
|----|-----|
| PR1 | `patchPreservingStatus()` — snapshot Status before `client.Patch()`, restore after; applied at all 7 Patch call sites in state handlers |
| PR2 | `mergeIncoming()` — carry forward optimistic Status on every watchable delivery; correction path via `reconcileTruth()` |
| PR3 | `updateExecutionStatuses` — refactored to route through StatusWriter with drift detection via `snapshotExecutionStates` / `executionStatesEqual` |
| PR4 | Execution status cascade pattern — `StartedAt` hoisted with idempotent guard; `Active > 0` → `StateRunning`; `JobComplete`/`JobFailed` conditions loop with `break`; metrics on first terminal transition |

---

## Implementation Status

### Readiness Assessment

**Build & Tests** (verified 2026-03-04):

| Check | Status |
|-------|--------|
| `go build ./...` — clean | ✅ |
| `go test ./internal/message/...` — 11/11 pass | ✅ |
| `go test ./internal/metrics/...` — 18/18 pass | ✅ |
| `go test ./api/...` — all pass | ✅ |
| `go test ./internal/scheduler/... ./internal/restore/... ./internal/recovery/...` | ✅ |
| Unit tests for `internal/provider/processor/...` packages | ⚠️ Gap |

**Status Write Path Integrity** — only the StatusWriter writes status sub-resources. All 14 mutation sites in plan state handlers route through `PlanStatuses.Send()`. Exception `removeFromPlanStatus` routes through `PlanStatuses.Send()`.

**Metrics Coverage** — 11 metrics verified including new `worker_goroutines` and `status_queue_dropped_total`.

### Open Items

| ID | Severity | Risk | Action |
|----|----------|------|--------|
| C2 | Critical | Low | No Job watches — 0–5s detection lag via poll timer. Functional but sub-optimal. Add `.Owns(&batchv1.Job{})` as follow-up. |
| M1 | Medium | Very Low | Max dispatch depth ~4 in practice. Optional `maxDispatchDepth` guard. |
| M3 | Medium | Low | Exception processor serial; bottleneck unlikely below ~100 concurrent exceptions. |
| L4 | Low | None | Design doc directory structure mismatch — cosmetic. |
| L5 | Low | None | Missing rapid-coalescing integration test. |

### Conditions for Flipping `--legacy-reconciler=false` as Default

Before graduating the async pipeline as default, the following must be completed:

1. **Unit tests** for `internal/provider/processor/...` packages — currently zero coverage on state handlers, Worker, Coordinator, and Status Writer.
2. **E2E validation** of a full hibernation → wakeup → error → recovery cycle under `--legacy-reconciler=false`.
3. **C2 (Job watches)** — adding `.Owns(&batchv1.Job{})` to the provider is strongly recommended for production readiness.

---

## Testing Strategy

### Unit Tests (per processor)

```go
func TestScheduleProcessor_ActiveToHibernating(t *testing.T) {
    resources := &message.ControllerResources{}
    statuses := &message.ControllerStatuses{}

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go coordinator.Start(ctx)

    resources.PlanResources.Store("ns/test", &message.PlanContext{
        Plan:           planWithPhase(v1alpha1.PhaseActive),
        ScheduleResult: &message.ScheduleEvaluation{ShouldHibernate: true},
    })

    // Verify PlanStatuses contains Phase=Hibernating
    ...
}
```

- No K8s client mocks needed for schedule/suspension/error state handlers.
- hibernatingState/wakingUpState need a mock for Job creation only.
- Status Writer needs a mock K8s client.

### Integration Tests

Wire all processors with real watchable maps, verify full lifecycle:
`Active → Hibernating → Hibernated → WakingUp → Active`.

### HandleSubscription Tests

Verify coalescing, initial state bootstrap, panic recovery, metrics.

---

## Migration Plan

| Step | Description | Status |
|------|-------------|--------|
| 1 | Foundation: `watchable` dependency + `internal/message/` + feature flag | ✅ Done |
| 2 | Provider Layer: HibernatePlan + ScheduleException providers | ✅ Done |
| 3 | Coordinator + Worker + all state handlers | ✅ Done |
| 4 | Status Writer processor | ✅ Done |
| 5 | Wiring in `app.go` behind `--legacy-reconciler=false` | ✅ Done |
| 6 | Unit tests for processor packages | ⏳ Pending |
| 7 | E2E test: full hibernation/wakeup/error/recovery cycle | ⏳ Pending |
| 8 | C2: Add Job watches to provider | ⏳ Pending |
| 9 | Flip default: `--legacy-reconciler=false` | ⏳ Pending (after 6–8) |
| 10 | Remove `internal/controller/` legacy code (separate PR) | ⏳ Future |

---

## References

- [Envoy Gateway Architecture](https://github.com/envoyproxy/gateway) — Pipeline design inspiration
- [Envoy Gateway watchutil.go](https://github.com/envoyproxy/gateway/blob/main/internal/message/watchutil.go) — Subscription handler pattern
- [telepresenceio/watchable](https://github.com/telepresenceio/watchable) — Pub/sub map library (MIT, ~414 lines)
- [RFC-0001: Hibernate Operator](./0001-hibernate-operator.md) — Core architecture reference
- [RFC-0006: Notification System](./0006-notification-system.md) — Future feature enabled by this async pipeline
- [Original Plan Document](../docs/plan/async-phase-driven-reconciler.md) — Archived; design superseded by this RFC
- [Original Review Document](../docs/plan/async-phase-driven-reconciler-review.md) — Archived; findings incorporated into this RFC
- [Original Changelog](../docs/plan/async-phase-driven-reconciler-changelog.md) — Archived; implementation decisions incorporated into this RFC
