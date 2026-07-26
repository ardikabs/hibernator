---
rfc: RFC-0010
title: Control Intent Priority Model
status: Implemented
date: 2026-07-25
---

# RFC 0010 — Control Intent Priority Model

**Keywords:** State-Machine, Priority-Model, Intent-Resolution, Gates, Annotations, Mutual-Exclusivity, Revert, Override, Restart, Suspension

**Status:** Implemented

**Related:**
- RFC-0008 (Async Phase-Driven Reconciler) — state machine architecture
- ADR-0010-1 (OperationTrigger Field) — provenance tracking implementation

## Summary

This RFC defines the 5-tier Control Intent Priority Model that governs how the Hibernator state machine resolves competing user intents (deletion, suspension, revert, override, restart) and schedule-driven operations. The model provides deterministic precedence rules, a coexistence matrix, and mutual exclusivity enforcement via admission webhook validation.

## Motivation

Hibernator accepts multiple control signals simultaneously:

- Time-based schedule evaluation (automatic hibernation/wakeup)
- Manual annotations (override, restart, revert, retry)
- Administrative actions (suspension, deletion)
- Exception-driven schedule deviations

Without explicit priority rules, conflicting intents produce unpredictable behavior. A plan with both `override-action=true` (wakeup) and an active hibernation schedule could oscillate or enter undefined states. The priority model eliminates this ambiguity.

## Goals

- Provide deterministic resolution for all intent combinations
- Prevent infinite retry loops for user-intent operations
- Enable auditability via the `OperationTrigger` provenance field
- Maintain backward compatibility with existing annotation usage

## Non-Goals

- Application-level quiescing (out of scope)
- Autoscaling or cost optimization (out of scope)

## Proposal

### Priority Hierarchy

```
Tier 1: Deletion        (highest priority — always wins)
Tier 2: Suspension
Tier 3: Revert
Tier 4: Override
Tier 5: Restart         (lowest priority among user intents)
Base:   Schedule        (default when no user intent is active)
```

### Gate-Based Implementation

The `selectHandler()` function implements the priority model as a first-match-wins gate pipeline:

1. `deletionGate` — checks `DeletionTimestamp`
2. `suspensionGate` — checks `spec.suspend` and `suspend-until`
3. `revertGate` — checks `revert` annotation + `PhaseError`
4. Phase-based dispatch — routes to `selectIdleHandler()` for Tiers 4-5 + Schedule

Within `selectIdleHandler()`:
1. `override-action` → `overrideActionState`
2. `restart` → `restartState`
3. Default → `idleState` (schedule-driven)

### Coexistence Matrix

| Combination | Winner | Behavior |
|-------------|--------|----------|
| Deletion + anything | Deletion | Finalizer cleanup |
| Suspension + user intent | Suspension | Auto-clear lower-priority annotations |
| Revert + Override/Restart | Revert | Revert proceeds; others ignored in Error |
| Override + Restart | Override | Override captures tick; may handle restart internally |
| Schedule + user intent | User intent | Schedule suppressed for that reconciliation |

### Mutual Exclusivity Enforcement

The admission webhook validates that conflicting annotations cannot be applied simultaneously:

- `revert` + `override-action` → rejected
- `override-action` + `spec.suspend=true` → rejected
- `restart` + `PhaseError` → rejected (revert takes precedence)

### OperationTrigger Provenance Field

A typed enum field in `HibernatePlanStatus` records the origin of every operation:

```go
type OperationTrigger string

const (
    TriggerSchedule  OperationTrigger = "Schedule"
    TriggerRevert    OperationTrigger = "Revert"
    TriggerRetry     OperationTrigger = "Retry"
    TriggerOverride  OperationTrigger = "Override"
    TriggerRestart   OperationTrigger = "Restart"
)
```

**Semantics:**
- Set when operation starts
- Preserved in stable states (`PhaseActive`, `PhaseHibernated`) for provenance
- Cleared on operation success
- Cleared on operation failure for user-intent triggers (prevents loops)
- Preserved on failure for `TriggerSchedule` (allows retry backoff)

## Rationale

### Why This Priority Order?

1. **Deletion first** — administrative imperative; the plan should not exist
2. **Suspension second** — operational pause should not be blocked by manual controls
3. **Revert third** — recovery from error is higher priority than further manual control
4. **Override fourth** — continuous state override beats one-shot re-execution
5. **Restart fifth** — re-execution is the least intrusive intent

### Why Mutual Exclusivity?

- **Predictability:** Single winner per reconciliation loop
- **Safety:** No fighting intents (hibernate vs. wakeup simultaneously)
- **Auditability:** `OperationTrigger` clearly shows the winning intent
- **Simplicity:** Gates are independent and make binary decisions

## Alternatives Considered

### Alternative A: Weighted Scoring

Assign numerical weights to intents and pick the highest score.

- **Rejected:** Too complex; weights are arbitrary; hard to reason about

### Alternative B: Timestamp-Based (Last-Write-Wins)

The most recently applied annotation wins.

- **Rejected:** Non-deterministic in controller loops; race conditions possible; hard to audit

### Alternative C: Fully Parallel Execution

Allow all intents to execute simultaneously and merge results.

- **Rejected:** Impossible for hibernation/wakeup (mutually exclusive states); would require complex merge logic

## Implementation Notes

### Files Modified

- `api/v1alpha1/hibernateplan_types.go` — `OperationTrigger` enum + field
- `internal/provider/processor/plan/state/state_selection.go` — Gate pipeline
- `internal/provider/processor/plan/state/gates.go` — Individual gate logic
- `internal/provider/processor/plan/state/state_*.go` — State handlers set trigger
- `internal/validationwebhook/hibernateplan_validator.go` — Mutual exclusivity validation

### Backward Compatibility

- Existing plans with `revert` annotations continue to work
- `RevertInProgress` boolean was replaced by `OperationTrigger` field (no migration needed for new feature)
- Admission webhook prevents new conflicting annotations from being applied

## References

- Kubernetes Operator Best Practices: [Operator SDK Docs](https://sdk.operatorframework.io/docs/best-practices/)
- Kubernetes Finalizers: [Kubernetes Documentation](https://kubernetes.io/docs/concepts/overview/working-with-objects/finalizers/)
- Hibernator State Machine: See `internal/provider/processor/plan/state/state_selection.go`
