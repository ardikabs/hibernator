---
date: June 18, 2026
status: investigated
component: Restore Manager / PlanSnapshot / Exception Overrides
---

# Findings: Restore Data Semantics for Fresh Cycle Annotation

## Context

As part of integrating `PlanSnapshot` into the operational workflow (hib-7f9), a `fresh` annotation (`hibernator.ardikabs.com/fresh=true`) is being introduced. When paired with `restart=true` or `override-action=true`, it signals that the operator wants to start a **new cycle** with a fresh `PlanSnapshot` rebuilt from the live `ScheduleException` state, rather than preserving the locked intent from the previous cycle.

## Problem

The interaction between a **fresh cycle** and **restore data** is undefined and potentially unsafe:

- `PlanSnapshot` captures execution intent (targets, strategy, behavior).
- Restore data captures actual cloud resource state needed to wake resources up (e.g., node counts, ASG configs, RDS state).
- A fresh cycle refreshes execution intent, but it is unclear what should happen to existing restore data.

### Scenarios

| Operation | Default behavior | Fresh behavior (snapshot) | Open question (restore data) |
|---|---|---|---|
| Restart hibernate | Reuse cycle ID from live restore data | New cycle ID + fresh snapshot | Should old restore data be cleared? |
| Restart wakeup | Reuse existing cycle ID + snapshot | New cycle ID + fresh snapshot | Should old restore data be reused or cleared? |
| Override hibernate from Active | Already starts new cycle + fresh snapshot | Same as default | Should old restore data be cleared? |
| Override wakeup from Hibernated | Reuse existing cycle ID + snapshot | New cycle ID + fresh snapshot | Should old restore data be reused or cleared? |

### Why This Matters

1. **Fresh hibernate**: If old restore data is not cleared, the new shutdown may append to or conflict with stale data. Restoring from mixed-cycle data could corrupt resource state on wakeup.

2. **Fresh wakeup**: Wakeup requires restore data to actually restore resources. If old restore data is cleared, there is nothing to wake up from. If old restore data is reused while the snapshot is fresh, there is a semantic mismatch: the wakeup may target a different target list than the data that was captured during shutdown.

3. **Safety**: Restore data is the authoritative source for resource restoration. Combining a fresh intent with stale restore data, or clearing restore data when wakeup still needs it, could lead to data loss or operational failure.

## Root Cause

The `fresh` annotation concept was introduced to allow operators to break cycle intent locking and re-evaluate live exceptions. However, the design discussion focused on `PlanSnapshot` and did not fully account for the `RestoreManager` lifecycle. `RestoreManager` data is currently tied to the plan and target name, not explicitly to a `CycleID`, so there is no natural mechanism to isolate per-cycle restore data or decide when to clear it.

## Proposed Directions

### Option A: Fresh clears all restore data

When `fresh=true`, the controller clears all restore data for the plan before starting the operation.

- **Pros**: Simple, avoids cross-cycle contamination.
- **Cons**: Fresh wakeup becomes impossible unless preceded by a fresh hibernate. The operator must understand that `fresh` on wakeup is only valid if restore data exists from the same cycle.

### Option B: Fresh only refreshes snapshot, preserve restore data

`fresh=true` only affects `PlanSnapshot`; restore data is untouched.

- **Pros**: Wakeup still works.
- **Cons**: Hibernate fresh may leave stale restore data. Wakeup fresh may use restore data from a different target set than the fresh snapshot.

### Option C: Bind restore data to CycleID

Refactor restore data so each entry records its `CycleID`. On fresh cycle, old restore data is ignored. The new cycle generates new restore data under the new cycle ID.

- **Pros**: Clean isolation, supports fresh hibernate and fresh wakeup correctly.
- **Cons**: Larger refactor, affects `RestoreManager`, executor interfaces, and job labeling.

### Option D: Scope fresh by operation

- Fresh hibernate: clear old restore data, start new cycle.
- Fresh wakeup: require that restore data exists and was produced in the current cycle; error if mismatch.

- **Pros**: Operation-appropriate behavior.
- **Cons**: More complex rules to document and test.

### Option E: Fresh only applies to hibernate operations

`fresh=true` is only meaningful for hibernate operations. On wakeup, it is ignored and a warning is logged. Wakeup is a mid-cycle continuation and cannot safely start a fresh cycle without breaking restore-data consistency.

- **Pros**: Simplest lifecycle semantics; no restore-data mismatch.
- **Cons**: Less flexible on wakeup.

## Decision

Adopted **Option E**: `fresh` is only supported for hibernate operations (restart hibernate, override hibernate from Active). On wakeup, `fresh=true` is ignored and logged. This avoids the restore-data / `CurrentCycleID` mismatch entirely.

Restore-data semantics for hibernate fresh operations (whether to clear old data, how to handle partial-success live data, etc.) are still deferred to this issue and require further design.

## Related Issues

- hib-7f9: Integrate PlanSnapshot into operational workflow (PlanSnapshot-only fresh-cycle support implemented; fresh limited to hibernate)
- hib-1lj: Design restore data semantics for fresh-cycle annotation
