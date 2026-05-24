---
date: May 23, 2026
status: investigated
component: Plan State Machine (suspend/unsuspend)
---

# Findings: Suspend / Unsuspend Resume Routing Defects and Data-Hygiene Gaps

## Problem Description

Cross-checking the full suspend/unsuspend lifecycle across `gates.go`, `state_pre_suspension.go`, `state_suspended.go`, and `state_idle.go` revealed three distinct issues that affect correctness and crash-safety when a `HibernatePlan` is resumed after suspension.

### Issue 1 — Suspended-at-Hibernated Re-triggers Shutdown (P1)

**Scenario:** A plan is in `PhaseHibernated` (resources are already fully shut down). An operator suspends it. Later, during the same off-hours window (`ShouldHibernate=true`), the plan is resumed.

**Current behaviour:**
1. `resumeFromError()` → skipped (not `PhaseError`).
2. `resumeFromExecution()` → skipped (not `PhaseHibernating` or `PhaseWakingUp`).
3. `shouldForceWakeUpOnResume()` → returns `false` because `ShouldHibernate=true`.
4. **Default → `PhaseActive`**.
5. `idleState` sees `PhaseActive + ShouldHibernate=true` → transitions to `PhaseHibernating`.

**Impact:** A fully hibernated plan is forced to re-run the entire shutdown process. This is redundant, potentially dangerous (re-invoking shutdown Jobs against already-shut-down resources), and operationally inefficient.

**Expected behaviour:** Resume directly to `PhaseHibernated`, since the resources are already shut down and the schedule still indicates off-hours.

### Issue 2 — `forceWakeUpOnResume` Is Not Crash-Safe (P1)

**Scenario:** A plan was suspended at `PhaseHibernated`, then resumed during on-hours (`ShouldHibernate=false`). `shouldForceWakeUpOnResume()` returns `true`, so `forceWakeUpOnResume()` is invoked.

**Current behaviour:**

```go
func (state *suspendedState) forceWakeUpOnResume(...) (StateResult, error) {
    plan.Status.Phase = PhaseHibernated   // in-memory ONLY
    cleanupSuspensionAnnotations(ctx, log, plan)  // PATCH to K8s: deletes suspended-at-phase annotation
    return StateResult{Requeue: true}, nil       // queues NO status update here
}
```

The `PhaseHibernated` assignment is **only in-memory**; the actual status update is deferred to the next dispatch cycle (`Requeue=true`). Before that happens, the `suspended-at-phase` annotation is removed via a `Patch` call.

**Impact:** If the worker crashes after the annotation patch succeeds but before the next dispatch writes `PhaseHibernated` to K8s, on restart:
- The K8s object shows `PhaseSuspended`, `Spec.Suspend=false`, and **no** `suspended-at-phase` annotation.
- `suspendedState` dispatches again → `resume()` → `shouldForceWakeUpOnResume()` returns `false` (annotation missing).
- Default → `PhaseActive`.
- `idleState` sees `PhaseActive + ShouldHibernate=false` → no-op.

**Result:** The plan stays `PhaseActive` while underlying resources remain hibernated. The restore data is never consumed, stranding the plan in an inconsistent state.

### Issue 3 — Stale Execution Bookmarks Left in Status (P3)

**Scenario:** `resumeFromExecution()` routes a plan to `PhaseActive` or `PhaseHibernated` after a mid-execution suspension.

**Current behaviour:** The status update queued by `resumeFromExecution()` only changes `Phase` and `LastTransitionTime`:

```go
state.Statuses.PlanStatuses.Send(statusprocessor.Update[...]{
    Mutator: statusprocessor.MutatorFunc[...](func(p *hibernatorv1alpha1.HibernatePlan) {
        p.Status.Phase = targetPhase
        p.Status.LastTransitionTime = ptr.To(metav1.NewTime(now))
    }),
})
```

It does **not** clear `CurrentCycleID`, `CurrentOperation`, `CurrentStageIndex`, or `Executions`.

**Impact:** The K8s object ends up with `Phase=Active` (or `Hibernated`) but still carries `CurrentOperation=Hibernate` and stale execution metadata. Any downstream logic that inspects these fields (observability, UI, other controllers) sees inconsistent data.

---

## Root Cause Analysis

### Issue 1 — Missing Handler for `PhaseHibernated` in `resume()`

The `resume()` method defines an explicit priority chain:

```go
// Priority order:
//  1. Suspended-at-Error → resumeFromError()
//  2. Suspended-mid-execution → resumeFromExecution()
//  3. Force-wakeup conditions met → forceWakeUpOnResume()
//  4. Default → PhaseActive
```

`PhaseHibernated` is not explicitly handled in any of the first three branches. The `forceWakeUpOnResume()` branch is designed specifically for the on-hours case (when `ShouldHibernate=false`). There is no counterpart for the off-hours case (`ShouldHibernate=true`), so the code falls through to the default `PhaseActive` — which is a conservative but incorrect choice for a plan that was already fully hibernated.

### Issue 2 — Split Mutation Between Patch and Status Update

`forceWakeUpOnResume()` performs two independent mutations:
1. An in-memory `plan.Status.Phase = PhaseHibernated` (not persisted).
2. A `patchAndPreserveStatus` call that deletes the `suspended-at-phase` annotation (persisted immediately).

Because these are not atomic and the status update is deferred to the next dispatch cycle, a crash between step 2 and the next cycle leaves the object in an unrecoverable intermediate state.

### Issue 3 — Incomplete Status Reset in `resumeFromExecution()`

The `resumeFromExecution()` function was designed to preserve execution bookmarks so that mid-execution suspension can continue exactly where it left off. However, when the function routes to an idle phase (`PhaseActive` or `PhaseHibernated`) because the schedule window has shifted, the bookmarks are no longer meaningful. The function fails to differentiate between "continue from bookmark" and "abort to idle" semantics.

---

## Author's Likely Intent (Charitable Reading)

Before treating the above as outright defects, it is worth reconstructing the probable mental model of the original author. The code is internally consistent with a specific design philosophy; the gaps only become visible when that philosophy is stress-tested.

### Core Mental Model

> **"Suspension is a pause button, not a save-state. Resume means 'unpause and let the schedule re-evaluate from the safest baseline.' Idempotency and Requeue guarantee correctness."**

### Issue 1 — Why Default to `PhaseActive`?

The author likely viewed `PhaseActive` as the **universal safe baseline** — the "ground state" of the lifecycle. From there, `idleState` sees `ShouldHibernate=true` and transitions to `PhaseHibernating`. The reasoning was probably:

- *"Hibernating from Active is idempotent anyway — the executor contract says Shutdown is idempotent."*
- *"Re-running shutdown on an already-hibernated resource is a no-op at worst."*
- *"This avoids a whole matrix of 'resume-to-Hibernated' logic that we don't need."*

**Assessment:** This was an intentional simplification. The author chose a single default path (`PhaseActive`) to avoid branching on every possible pre-suspension phase, trusting executor idempotency to absorb redundant work. It is defensible as a first-pass design, but breaks down when re-running Jobs is expensive or observability-sensitive.

### Issue 2 — Why Delete the Annotation Before Persisting Status?

The `forceWakeUpOnResume()` function performs two mutations: an in-memory phase assignment and a `Patch` call that deletes the annotation. The author probably reasoned:

- *"Requeue is reliable — the worker won't drop this."*
- *"Even if it re-queues, idleState will see `PhaseHibernated + !ShouldHibernate + HasRestoreData` and do the right thing."*
- *"Front-loading the annotation cleanup keeps the code DRY — `cleanupSuspensionAnnotations()` is reused from `resume()`."*

**Assessment:** The author viewed `Requeue` as an atomic handoff and did not consider the crash-between-patch-and-status-write edge case. The annotation cleanup was front-loaded to avoid a separate patch later. This is a durability gap, not a logic error.

### Issue 3 — Why Preserve Stale Bookmarks?

`resumeFromExecution()` was designed as a **state-preservation** system for mid-execution suspension. The design was:

> "When you suspend mid-execution, we freeze the bookmark exactly as-is. When you resume, we either restore the same phase and continue (bookmarks needed), or route to an idle phase (bookmarks no longer needed, but harmless)."

The author likely reasoned:

- *"Leaving stale bookmarks in `PhaseActive` or `PhaseHibernated` doesn't hurt — the next `transitionToHibernating` or `transitionToWakingUp` will overwrite them anyway."*
- *"Clearing them adds risk: what if we accidentally clear something we shouldn't? Safer to leave them."*

**Assessment:** This was defensive programming prioritising "don't lose in-flight state" over "keep the status object perfectly clean." It is reasonable when the alternative is accidentally wiping a mid-execution bookmark, but it leaves the status object inconsistent for observability and external tooling.

### Synthesis

The suspend/unsuspend mechanism is **architecturally sound** but **not production-safe at the edges**. The author made deliberate simplifications:

| Simplification | Rationale | Where It Breaks |
|----------------|-----------|-----------------|
| Default resume to `PhaseActive` | Universal safe baseline; avoids phase matrix | Re-triggers shutdown on already-hibernated resources |
| Front-load annotation cleanup | DRY reuse of `cleanupSuspensionAnnotations`; trust `Requeue` | Crash leaves plan in unrecoverable intermediate state |
| Preserve all bookmarks unconditionally | Defensive: never accidentally wipe state | Stale metadata visible to observability / external tools |

These are **design gaps masquerading as simplifications** — defensible in isolation, but collectively they create correctness and crash-safety issues.

---

## Proposed Solutions

### Option A: Add Explicit `PhaseHibernated` Branch in `resume()` (for Issue 1)

Introduce a dedicated check in `resume()` before the default `PhaseActive` fallback:

```go
if suspendedAtPhase == string(hibernatorv1alpha1.PhaseHibernated) {
    if shouldHibernate {
        targetPhase = PhaseHibernated
    } else {
        // Already handled by shouldForceWakeUpOnResume() above
    }
}
```

- **Pros:** Minimal change; directly addresses the missing case.
- **Cons:** Slightly increases branching complexity in `resume()`.

### Option B: Queue Status Update Before Annotation Cleanup in `forceWakeUpOnResume()` (for Issue 2)

Restructure `forceWakeUpOnResume()` so the status update is queued **before** the annotation patch:

```go
// 1. Queue the PhaseHibernated status update via PlanStatuses.Send
// 2. Only then call cleanupSuspensionAnnotations()
```

Alternatively, keep the annotation but change the resume logic to rely on `Spec.Suspend` (already `false`) plus `Status.Phase == PhaseSuspended` plus `HasRestoreData` to determine force-wakeup, rather than requiring the annotation.

- **Pros:** Eliminates the split-mutation vulnerability.
- **Cons:** Requires careful ordering to avoid race conditions with the KeyedWorkerPool.

### Option C: Clear Execution Bookmarks When Routing to Idle (for Issue 3)

In `resumeFromExecution()`, when `targetPhase` is `PhaseActive` or `PhaseHibernated`, extend the mutator to clear stale fields:

```go
Mutator: statusprocessor.MutatorFunc[...](func(p *hibernatorv1alpha1.HibernatePlan) {
    p.Status.Phase = targetPhase
    p.Status.LastTransitionTime = ptr.To(metav1.NewTime(now))
    p.Status.CurrentCycleID = ""
    p.Status.CurrentOperation = ""
    p.Status.CurrentStageIndex = 0
    p.Status.Executions = nil
}),
```

- **Pros:** Clean status, no stale metadata.
- **Cons:** Must be careful not to break `resumeFromExecution` when continuing mid-execution (`PhaseHibernating` or `PhaseWakingUp`), where bookmarks must be preserved.

---

## Appendix: Verified Sound Behaviours

The following aspects of the suspend/unsuspend mechanism were reviewed and found to be correct:

| Aspect | Assessment |
|--------|------------|
| **Graceful drain** (`preSuspensionState`) | Solid. Mid-execution suspension waits for in-flight Jobs to reach a terminal state before writing `PhaseSuspended`. |
| **Operation-aware resume** (`resumeFromError`) | Correctly routes `PhaseError` back to `PhaseActive` or `PhaseHibernated` based on `CurrentOperation`. |
| **Window-aware resume** (`resumeFromExecution`) | Correctly continues or aborts mid-execution based on current schedule. |
| **Auto-suspend (`suspend-until`)** | Deadline parsing, timer scheduling, and auto-resume logic are clean. |
| **Gate priority** | `deletionGate → suspensionGate → phase dispatch` is correct. |

## Missing Test Coverage

There are no existing tests for:
- `suspendedAtPhase=Hibernated + ShouldHibernate=true` (the Issue 1 path).
- Crash-recovery behaviour of `forceWakeUpOnResume` (the Issue 2 path).
- Post-resume status field hygiene (the Issue 3 path).
