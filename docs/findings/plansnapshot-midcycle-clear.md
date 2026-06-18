---
date: June 18, 2026
status: resolved
component: Plan State Processor / Exception Overrides
---

# Findings: PlanSnapshot and AppliedExceptionOverride Cleared Mid-Cycle

## Problem Description

The `PlanSnapshot` and `AppliedExceptionOverride` fields in `HibernatePlan` status, introduced in Phase 7 of the Schedule Exceptions feature (RFC 0003) to provide **cycle intent locking**, are being cleared during the `Hibernating` → `Hibernated` transition. This breaks the guarantee that a cycle's execution intent remains locked for the entire duration of the cycle (shutdown + wakeup).

### Impact

When an active `ScheduleException` with execution overrides is in effect during shutdown, the following scenarios fail:

1. **Exception expires or is deleted during hibernation**: On wakeup, `state_idle.go` rebuilds the effective plan from the **live** exception state instead of the locked snapshot. If the exception no longer exists or has changed, the wakeup may re-enable targets that were disabled during shutdown, revert overridden parameters, or use a different execution strategy.

2. **Exception is edited mid-cycle**: While the validating webhook blocks changes to `targetOverrides` and `executionOverride`, other field changes (e.g., `validUntil`) are allowed. If those changes affect exception eligibility, the wakeup may evaluate a different effective plan than the shutdown used.

3. **Audit trail is broken**: `AppliedExceptionOverride` is lost after shutdown, making it impossible to determine which exception was active during the cycle without inspecting `ExecutionHistory`.

## Root Cause Analysis

### Code Evidence

**`state_hibernating.go:104-105`** clears both fields during `Hibernating` → `Hibernated`:

```go
func (state *hibernatingState) finalize(...) {
    // ...
    state.Statuses.PlanStatuses.Send(statusprocessor.Update[...]{
        Mutator: statusprocessor.MutatorFunc[...](func(p *hibernatorv1alpha1.HibernatePlan) {
            p.Status.Phase = hibernatorv1alpha1.PhaseHibernated
            // ...
            p.Status.AppliedExceptionOverride = ""
            p.Status.PlanSnapshot = nil  // <-- BUG: cleared too early
        }),
    })
}
```

**`state_idle.go:140-197`** (`transitionToWakingUp`) rebuilds `PlanSnapshot` from the **live** exception instead of reusing the existing snapshot:

```go
func (state *idleState) transitionToWakingUp(log logr.Logger) (StateResult, error) {
    var effectivePlan = plan
    appliedExceptionName := ""
    if ep := state.buildEffectivePlan(plan); ep != nil {
        effectivePlan = ep
        if exc := state.findActiveExceptionOverride(); exc != nil {
            appliedExceptionName = exc.Name
        }
    }
    // ...
    if appliedExceptionName != "" {
        p.Status.PlanSnapshot = &hibernatorv1alpha1.PlanSnapshot{
            CycleID:       plan.Status.CurrentCycleID,
            ExceptionName: appliedExceptionName,
            Targets:       effectivePlan.Spec.Targets,
            // ...
        }
    }
}
```

This means:
- If the same exception is still active → a **new snapshot** is built, which happens to match but is redundant and races with live exception changes.
- If the exception is no longer active → **no snapshot is set**, and wakeup falls back to the unmodified live spec.
- If a **different** exception is now active → the wrong exception's overrides are applied to wakeup.

### Design Intent vs Implementation

The Phase 7 addendum to RFC 0003 states:

> **Usage**: All subsequent states (`Hibernating`, `Hibernated`, `WakingUp`, `PhaseError`) use the snapshot instead of re-evaluating the live exception.
> **Clear**: Snapshot is cleared **only when the cycle completes** (`Idle`) or is explicitly restarted (`restart=true` annotation).

The current implementation treats the fields as **per-operation** (cleared after shutdown, rebuilt for wakeup) instead of **per-cycle** (preserved across both operations).

## Proposed Solutions

### Option A: Preserve Snapshot Across Entire Cycle (Recommended)

**Approach**: Remove the clear of `PlanSnapshot` and `AppliedExceptionOverride` from `hibernatingState.finalize()`. Modify `transitionToWakingUp()` to reuse the existing snapshot instead of rebuilding from live exceptions.

Changes:
1. In `state_hibernating.go:finalize()`, remove lines 104-105 (`AppliedExceptionOverride = ""`, `PlanSnapshot = nil`).
2. In `state_idle.go:transitionToWakingUp()`, check for existing `PlanSnapshot` first. If present and `CycleID` matches `CurrentCycleID`, reuse it. Only fall back to live exception evaluation if no snapshot exists (backward compatibility for pre-upgrade plans).
3. Update tests: `TestHibernatingState_Finalize_ClearsPlanSnapshot` → `TestHibernatingState_Finalize_KeepsPlanSnapshot`, `TestWakingUpState_Finalize_ClearsPlanSnapshot` stays correct since wakeup→active should still clear.
4. Add new test: `TestTransitionToWakingUp_ReusesExistingPlanSnapshot`.

- **Pros**: Matches design intent exactly. Minimal code change. Preserves backward compatibility (no snapshot = fallback to live). Clean separation between cycle locking and per-operation logic.
- **Cons**: None significant. The `AppliedExceptionOverride` field stays populated longer, but this is the intended behavior.

### Option B: Move Cycle-Complete Clear to Idle State

**Approach**: Same as Option A, but explicitly clear the fields in `idleState` when entering a stable `Active` or `Hibernated` phase without a pending transition. This is more explicit about lifecycle boundaries.

- **Pros**: Very explicit about when clearing happens.
- **Cons**: Unnecessary complexity. `idleState` runs on every reconcile; adding clearing logic there introduces risk of race conditions. The current `wakingUpState.finalize()` already clears on transition to `Active`, which is sufficient.

### Option C: Separate Operation Snapshots

**Approach**: Store separate snapshots for shutdown and wakeup operations in `ExecutionHistory`.

- **Pros**: Could support asymmetric exceptions (different overrides for shutdown vs wakeup).
- **Cons**: Massive scope increase. No requirement for this. Violates the "one cycle = one locked intent" design principle. Would require schema changes.

## Impact of Fix

- **Deterministic behavior**: A cycle's execution intent is locked from start to finish, regardless of exception changes during hibernation.
- **Auditability**: `AppliedExceptionOverride` remains visible throughout the cycle, making it easy to identify which exception was active.
- **Backward compatibility**: Plans without a snapshot (from pre-upgrade controllers) continue to fall back to live evaluation.
- **Webhook + finalizer remain relevant**: The snapshot is the primary safeguard; the webhook and finalizer provide defense in depth.

## Appendix: Affected Tests

The following tests encode the buggy behavior and will need updating:

- `TestHibernatingState_Finalize_ClearsPlanSnapshot` — asserts incorrect behavior
- `TestWakingUpState_Finalize_ClearsPlanSnapshot` — this one is actually correct (wakeup→active should clear)
- `TestTransitionToHibernating_CapturesPlanSnapshot` — may need companion test for wakeup

New tests needed:
- `TestTransitionToWakingUp_ReusesExistingPlanSnapshot`
- `TestTransitionToWakingUp_FallsBackToLiveWhenNoSnapshot`
- `TestHibernatingState_Finalize_PreservesPlanSnapshot`

## Implementation Details

Fixed in the following changes:

1. **`hibernatingState.finalize()`** (`state_hibernating.go`):
   - Removed `AppliedExceptionOverride = ""` and `PlanSnapshot = nil` from the Mutator.
   - Snapshot is now preserved when transitioning `Hibernating` → `Hibernated`.

2. **`wakingUpState.finalize()`** (`state_wakingup.go`):
   - Removed `AppliedExceptionOverride = ""` and `PlanSnapshot = nil` from the Mutator.
   - Snapshot is now preserved when transitioning `WakingUp` → `Active`.

3. **`idleState.transitionToWakingUp()`** (`state_idle.go`):
   - Stopped rebuilding the effective plan from live exceptions.
   - Now reuses the existing `PlanSnapshot` targets when `CycleID` matches `CurrentCycleID`.
   - Falls back to `plan.Spec.Targets` when no snapshot exists (backward compatibility).
   - `AppliedExceptionOverride` and `PlanSnapshot` are no longer overwritten in the Mutator.

4. **Tests updated** (`state_execution_override_test.go`):
   - `TestHibernatingState_Finalize_ClearsPlanSnapshot` → renamed to `TestHibernatingState_Finalize_PreservesPlanSnapshot`, assertions flipped.
   - `TestWakingUpState_Finalize_ClearsPlanSnapshot` → renamed to `TestWakingUpState_Finalize_PreservesPlanSnapshot`, assertions flipped.
   - `TestTransitionToWakingUp_UsesEffectivePlanTargets` → renamed to `TestTransitionToWakingUp_UsesPlanSnapshotTargets`, now sets up a snapshot instead of a live exception.
   - Added `TestTransitionToWakingUp_ReusesPlanSnapshot` — verifies snapshot targets are used.
   - Added `TestTransitionToWakingUp_FallsBackToLiveWhenNoSnapshot` — verifies backward compatibility.

### Design Rationale

The adopted approach mirrors `CurrentCycleID` lifecycle:
- Set/replaced only when a new cycle starts (`Active` → `Hibernating`).
- Preserved across all subsequent phases.
- Never explicitly cleared in `finalize()` methods.
- Natural invalidation via `CycleID` mismatch when the next cycle starts.
