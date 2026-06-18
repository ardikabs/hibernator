# Design: PlanSnapshot Lifecycle Fix

**Date:** 2026-06-18
**Related:** docs/findings/plansnapshot-midcycle-clear.md, Beads hib-iwl

## Problem

`PlanSnapshot` and `AppliedExceptionOverride` are cleared in `hibernatingState.finalize()`, breaking the cycle intent locking guarantee. Wakeup rebuilds from live exception state instead of reusing the locked snapshot.

## Design Decision

Adopt a **CurrentCycleID-style lifecycle** for `PlanSnapshot`:

- **Set/replaced** only when a **new cycle starts**: `Active` → `Hibernating` (`transitionToHibernating()`)
- **Preserved** across all subsequent phases: `Hibernating` → `Hibernated` → `WakingUp` → `Active`
- **Never explicitly cleared** in `finalize()` methods
- **Natural invalidation** via `CycleID` mismatch: when the next cycle starts, `CurrentCycleID` changes, making the old snapshot stale. `effectivePlan()` already ignores snapshots where `snap.CycleID != CurrentCycleID`.

This mirrors how `CurrentCycleID` works — it is set at cycle start and persists until the next cycle overwrites it.

### Lifecycle Table

| Transition | `PlanSnapshot` Action | Rationale |
|---|---|---|
| `Active` → `Hibernating` | **Set** (or leave stale if no exception) | New cycle starts; capture locked intent |
| `Hibernating` → `Hibernated` | **Preserve** | Still mid-cycle |
| `Hibernated` → `WakingUp` | **Preserve / Reuse** | Continue with same locked intent |
| `WakingUp` → `Active` | **Preserve** | Cycle complete, but snapshot stays (stale on next cycle) |

### Why This Works

- `effectivePlan()` checks `snap.CycleID == CurrentCycleID`. An old snapshot from a completed cycle will have a mismatched `CycleID`, so it is safely ignored.
- No explicit cleanup needed in `finalize()` methods.
- `transitionToWakingUp()` must **not** rebuild from live exceptions; it should let the existing snapshot (if any) continue to apply.

## Files to Modify

1. `internal/provider/processor/plan/state/state_hibernating.go`
   - Remove `AppliedExceptionOverride = ""` and `PlanSnapshot = nil` from `finalize()`

2. `internal/provider/processor/plan/state/state_wakingup.go`
   - Remove `AppliedExceptionOverride = ""` and `PlanSnapshot = nil` from `finalize()`

3. `internal/provider/processor/plan/state/state_idle.go`
   - In `transitionToWakingUp()`, remove the live exception rebuild logic. The existing snapshot (if `CycleID` matches `CurrentCycleID`) will be used automatically by `effectivePlan()`.
   - Keep the cycle ID logic (reuse existing `CurrentCycleID` from hibernation).
   - Keep `AppliedExceptionOverride` as-is (it should already be set from the hibernation transition).

4. `internal/provider/processor/plan/state/state_execution_override_test.go`
   - Fix `TestHibernatingState_Finalize_ClearsPlanSnapshot` → assert fields are **preserved**
   - Fix `TestWakingUpState_Finalize_ClearsPlanSnapshot` → assert fields are **preserved**
   - Add `TestTransitionToWakingUp_ReusesPlanSnapshot`
   - Add `TestTransitionToWakingUp_FallsBackWithoutSnapshot`

## Backward Compatibility

Plans without a snapshot (pre-upgrade) continue to fall back to live evaluation via `buildEffectivePlan()`.
