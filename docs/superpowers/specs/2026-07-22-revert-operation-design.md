# Design: Revert Operation for Partial Hibernation Failures

**Date:** 2026-07-22  
**Type:** Feature Design  
**Status:** Implemented

## Problem Statement

When a hibernation cycle has partial failures (some targets successfully hibernated, some failed), the operator may want to "revert" the situation — wake up what was successfully hibernated and skip what failed. Currently:

1. When hibernate fails, plan goes to `Error` phase.
2. If no manual intervention, the system may retry hibernate on next schedule.
3. This causes cascading failures: trying to hibernate already-hibernated targets or targets in unknown state.

**User Scenario:**
- Schedule triggers hibernation at 20:00.
- Target A: hibernates successfully.
- Target B: fails (e.g., RDS instance locked, EC2 instance protected).
- Plan ends in `Error` phase.
- Next day 06:00 wakeup schedule fires → but system retries hibernate instead of waking.

## Design Decision

Introduce a **Revert Operation** that is schedule-aware and selective:

1. User triggers revert via `kubectl hibernator revert <plan>` (sets `hibernator.ardikabs.com/revert=true` annotation).
2. Controller detects annotation on a `PhaseError` plan through a dedicated pre-phase gate.
3. System queries `RestoreManager` to find targets with `IsLive=true` (meaningful restore data exists).
4. **Selective wakeup**: transition to `WakingUp` only if at least one target has live restore data. The runner then wakes each target; targets without live restore data no-op.
5. **Schedule-aware transition** after successful wakeup:
   - If currently in hibernation window → `Suspended` (`spec.Suspend=true`, `suspend-reason=revert`).
   - If currently in active window → `Active` (annotation removed, schedule continues).

The `revert` annotation is **consumed** during the flow: removed by `idleState` after a successful revert to `Active`, or replaced by `spec.Suspend=true` + `suspend-reason=revert` during a hibernation window.

## Architecture

### Gate: `revertGate`

Revert is treated as a **pre-phase gate**, not as part of the `PhaseError` case in `selectHandler`. This reflects that revert is a special, non-happy-path recovery action.

Gate priority order:

1. `deletionGate`
2. `suspensionGate`
3. `revertGate`
4. Phase-based dispatch

`revertGate` returns a `revertState` handler when:

- `plan.Status.Phase == PhaseError`
- `plan.Annotations[AnnotationRevert] == "true"`

If the plan is in `PhaseError` without the annotation, normal `recoveryState` runs.

### New Handler: `revertState`

```go
// revertState handles plans in PhaseError with revert annotation.
// It orchestrates a selective wakeup by transitioning Error -> WakingUp,
// leaving the revert annotation for idleState to consume after wakeup completes.
type revertState struct {
    *state
}
```

**State Transitions:**

```
Error ──(revertGate)──► revertState ──► WakingUp ──┬──(active window)──► Active
                                                   └──(hibernation window)──► Suspended
```

**Selective Execution:**
- Query restore data for each target (using `PlanSnapshot` targets when the snapshot matches the current cycle, otherwise live `plan.Spec.Targets`).
- If at least one target has live restore data (`data != nil && data.IsLive`), transition to `WakingUp`.
- If no target has live restore data, remove the revert annotation, set `status.ErrorMessage`, and keep the plan in `Error`.

The runner-level no-op for missing restore data is the second line of defense: if a target without live data still gets a wakeup Job, the runner treats it as a successful no-op.

### Annotation

| Annotation | Purpose |
|---|---|
| `hibernator.ardikabs.com/revert=true` | Triggers revert operation on `PhaseError` plans |

**Behavior:**
- Detected by `revertGate` on plans in `PhaseError`.
- Removed automatically on successful revert to `Active`.
- Removed and replaced by `spec.Suspend=true` + `suspend-reason=revert` when revert completes during a hibernation window.
- If set on a non-`Error` plan, it has no effect until the plan reaches `PhaseError`.

### CLI Command

```bash
kubectl hibernator revert <plan-name>
```

Options:
- `--dry-run` — preview which targets would be reverted.
- `--wait` — block until the revert reaches `Active` or `Suspended`.

## Files Modified

1. **`internal/provider/processor/plan/state/state_revert.go`** (new)
   - `revertState` struct and `Handle()` method.
   - Selective wakeup logic using `RestoreManager`.
   - No-op guard when no live restore data exists.

2. **`internal/provider/processor/plan/state/gates.go`**
   - Added `revertGate`.

3. **`internal/provider/processor/plan/state/state_selection.go`**
   - Registered `revertGate` in `runPrePhaseGates`.
   - Removed revert logic from the `PhaseError` case.

4. **`internal/provider/processor/plan/state/state_idle.go`**
   - Added revert annotation consumption in `PhaseActive`.
   - Active window: remove annotation.
   - Hibernation window: set `spec.Suspend=true` + `suspend-reason=revert`.

5. **`internal/provider/processor/plan/state/state_wakingup.go`**
   - `OnError` removes the revert annotation on wakeup failure to prevent infinite loops.

6. **`cmd/kubectl-hibernator/cli/revert/`** (new)
   - `revert.go` command implementation.
   - Sets annotation, supports `--dry-run` and `--wait`.

7. **`internal/wellknown/annotations.go`**
   - Added `AnnotationRevert` constant.

8. **`cmd/runner/state/loader.go`**
   - Modified `LoadRestoreData` to return `(data, found, error)`.
   - Excludes stale keys (`StaleCount > 0`) from restore data.

9. **`cmd/runner/app/runner.go`**
   - In wakeup operation: treat "not found" as successful no-op instead of failure.

## Runner Behavior for Revert

The Runner must handle missing restore data gracefully during revert wakeup:

```go
rd, found, err := state.LoadRestoreData(...)
if err != nil {
    operationErr = fmt.Errorf("load restore data: %w", err)
    break
}
if !found {
    // No restore data for this target - treat as successful no-op.
    r.log.Info("no restore data for target, treating wakeup as no-op")
    executorResult = &executor.Result{Message: "wakeup completed (no restore data)"}
    break
}
// ... proceed with exec.WakeUp
```

**Rationale:**
- The controller filters at the target level (`revertState` checks `data.IsLive`).
- The runner filters at the resource level (`LoadRestoreData` excludes `StaleCount > 0` keys).
- If a target without live restore data is still dispatched, the runner no-ops rather than fails, making the system robust to races.

## Data Flow

```
1. kubectl hibernator revert my-plan
   └── Sets annotation: hibernator.ardikabs.com/revert=true

2. Controller reconciliation loop
   └── runPrePhaseGates evaluates revertGate
   └── revertGate returns revertState because plan is PhaseError + annotation is set

3. revertState.Handle()
   ├── Use PlanSnapshot targets if snapshot CycleID matches CurrentCycleID
   ├── Query RestoreManager.Load for each target
   ├── If no target has live restore data:
   │     ├── Remove revert annotation
   │     ├── Set status.ErrorMessage
   │     └── Stay in PhaseError
   └── If at least one target has live restore data:
         ├── Build executions for all targets (runner will no-op missing data)
         └── Transition: Error → WakingUp

4. wakingUpState execution completes
   └── On success:
       ├── idleState consumes revert annotation
       ├── If hibernation window → spec.Suspend=true, suspend-reason=revert
       └── If active window → annotation removed, plan Active

5. On wakeup failure:
   └── wakingUpState.OnError removes revert annotation
   └── Plan returns to Error (manual intervention required)
```

## Key Design Points

### Two-Level Filtering

| Level | What it filters | Mechanism |
|---|---|---|
| Controller (`revertState`) | Targets | `RestoreManager.Load` → `data.IsLive` |
| Runner (`LoadRestoreData`) | Resources within a target | `Status[key].StaleCount > 0` excludes the resource |

A target is only reverted if it has live restore data. Within that target, only non-stale resources are restored.

### Why Not Retry-Failed vs Wakeup?

| Aspect | Retry-Failed | Revert (This Design) |
|---|---|---|
| Intent | Fix the failure | Restore to safe state |
| Targets | All targets | Only live restore data |
| Next phase | Error (if still failing) | Active or Suspended |
| Manual intervention | Often needed | Optional (schedule decides) |

## Backward Compatibility

- Existing plans continue working as-is.
- Revert is purely opt-in via annotation.
- If no targets have live restore data, revert is a no-op (plan stays in `Error`).

## Edge Cases

1. **No targets have live restore data**: Revert is no-op, plan stays in `Error`. User must use other recovery methods (manual intervention, retry).
2. **Plan not in `Error` phase**: Annotation is ignored until the plan reaches `PhaseError`.
3. **Plan in `Error` but hibernate never ran**: No restore data exists, revert no-op (same as edge case 1).
4. **Partial restore data**: Targets with `IsLive=true` are reverted; others are no-op.
5. **Schedule changes during revert**: Uses schedule state at time `idleState` consumes the annotation (after wakeup completes).
6. **Wakeup fails during revert**: Revert annotation is removed to prevent an infinite retry loop; plan returns to `Error`.
7. **Revert + suspend annotation present**: `suspensionGate` runs before `revertGate`; suspension takes precedence.

## Test Scenarios

### Unit Tests

1. `TestRevertGate_PhaseError_WithAnnotation_ReturnsRevertState`
2. `TestRevertGate_PhaseError_WithoutAnnotation_ReturnsNil`
3. `TestRevertGate_NonErrorPhase_ReturnsNil`
4. `TestRevertGate_AnnotationValueNotTrue_ReturnsNil`
5. `TestRunPrePhaseGates_SuspensionBeatsRevert`
6. `TestRevertState_Handle_LiveRestoreData_TransitionsToWakingUp`
7. `TestRevertState_Handle_NoLiveRestoreData_StaysErrorAndRemovesAnnotation`
8. `TestRevertState_Handle_UsesPlanSnapshotTargets`
9. `TestRevertState_Handle_MissingAnnotation_IsNoop`
10. `TestIdleState_Handle_ActiveRevertAnnotationActiveWindow_RemovesAnnotation`
11. `TestIdleState_Handle_ActiveRevertAnnotationHibernationWindow_SuspendsPlan`
12. `TestWakingUpState_OnError_RemovesRevertAnnotation`
13. `TestRunnerWakeup_NoRestoreData_SuccessAsNoOp`

### E2E Tests (`test/e2e/tests/revert.go`)

1. `ActiveWindow: should revert to Active when the revert annotation is applied during the active window`
2. `HibernationWindow: should revert to Suspended when the revert annotation is applied during the hibernation window`
3. `NoLiveRestoreData: should no-op revert when no live restore data exists`
4. `WakeupFailure: should return to Error and remove the revert annotation when wakeup fails during revert`

## Follow-Ups

- `hib-5m1`: Skip `MarkTargetRestored` for no-op wakeup targets. Currently marking no-op targets as restored is pragmatic because it allows `postWakeupCleanup`'s `MarkAllTargetsRestored` check to succeed and unlock restore data, but semantically it claims a target was restored when it was never hibernated.
