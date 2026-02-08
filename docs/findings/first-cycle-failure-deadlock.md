---
date: February 8, 2026
status: investigated
component: HibernatePlan Lifecycle
---

# Findings: First-Cycle Hibernation Failure & Wakeup Deadlock

## Problem Description

A critical operational constraint has been identified regarding the very first execution cycle of a `HibernatePlan`. If the initial hibernation (shutdown) attempt fails, the plan enters a "Dependency Deadlock" that prevents all subsequent automated restorations.

### The Deadlock Scenario
1.  **Initial Shutdown Fails:** A new `HibernatePlan` triggers its first hibernation. A target fails (e.g., due to incorrect permissions or parameters).
2.  **No Restore Point:** Because the shutdown did not complete successfully for the failing target, no "Restore Data" (ConfigMap) is captured or persisted.
3.  **Wakeup Trigger:** The schedule window for hibernation ends, and business hours begin. The Controller attempts to transition to `WakingUp`.
4.  **Immediate Failure:** The `WakingUp` operation requires a valid restore point to know the original state of the resources. Since none exists from the failed first cycle, the wakeup fails immediately with: `"cannot wake up: no restore point found"`.
5.  **State Persistence:** The plan remains in `PhaseError`, and the resources remain in their current state (likely still running or partially stopped), but the automation is stuck.

### The Partial Integrity Problem
A more subtle deadlock occurs when the first cycle is **partially successful**, compounded by the fact that the **Wakeup operation runs in reverse order** (LIFO logic):

1.  **Hibernation Sequence (A -> B):**
    - Target A succeeds and saves its state.
    - Target B fails *before* state capture.
2.  **Controller Perspective:** Because Target A saved data, `HasRestoreData` returns `true`. The Controller proceeds to `WakingUp`.
3.  **Wakeup Sequence (B -> A):**
    - Due to the execution strategy, the wakeup operation typically restores resources in reverse order of their hibernation.
    - **Target B is executed FIRST.** The Runner for Target B calls `loadRestoreData`, which fails because no entry exists for Target B.
    - **Result:** The entire `WakingUp` operation fails immediately at Target B.
4.  **The Deadlock:** Target A (which was successfully hibernated) is **never reached** in the wakeup chain.
    - Target A remains stuck in a hibernated state.
    - Target B remains in an unknown/unmanaged state.
    - The plan transitions to `PhaseError`.
5.  **Mental Model:** If an earlier target in the hibernation sequence succeeds, but a later one fails, that successful target is effectively "orphaned" in hibernation because the reverse-order wakeup will likely crash on the failed target before ever reaching it.

## Root Cause Analysis

The current architecture is "Success-Dependent" for its first cycle. The `internal/controller/hibernateplan/wakeup.go` logic strictly requires the existence of a restore point:

```go
hasRestoreData, err := r.RestoreManager.HasRestoreData(ctx, plan.Namespace, plan.Name)
if !hasRestoreData {
    return r.setError(ctx, plan, fmt.Errorf("cannot wake up: no restore point found"))
}
```

This assumes that every `WakingUp` phase was preceded by a successful `Hibernating` phase. It does not account for the "bootstrap" failure where the first-ever state capture never happened.

## Proposed Guidelines / Solutions

### User Guideline (Manual Intervention Required)
If a `HibernatePlan` fails during its **very first** hibernation attempt, the operator **must** fix the root cause before the next wakeup window arrives. If the wakeup window is reached while the plan is in `PhaseError` without a restore point, the system cannot automatically recover.

### Technical Mitigation: The "Graceful Bootstrap"
The manual retry signal (e.g., the proposed `retry-now` annotation) should be enhanced with "Bootstrap Awareness":
- If `retry-now` is triggered during a **Wakeup** window.
- AND `hasRestoreData` is **false**.
- The Controller should transition to **`PhaseActive`** instead of `PhaseError`.
- **Rationale:** If no restore data exists, we assume the resources were never successfully hibernated and are still in their "Active" state. This breaks the deadlock and lets the plan align with the schedule.

## Impact

- **Operational Safety**: Prevents plans from getting permanently stuck in a "no restore data" error loop.
- **User Experience**: Provides a clear path for users to onboard new plans and recover from initial configuration errors.
- **System Robustness**: Moves from a strict "State-Dependent" model to a more "Schedule-Aligned" model during recovery.
