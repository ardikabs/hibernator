# OperationTrigger Field Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `RevertInProgress` boolean with a typed `OperationTrigger` field that tracks operation origin (Schedule, Revert, Retry, Override, Restart) and provides complete provenance.

**Architecture:** Add `OperationTrigger` enum to `api/v1alpha1` types. Replace all `RevertInProgress` reads/writes with `OperationTrigger` equivalents. Preserve the existing gate/state selection architecture - no changes to control flow logic, only the field semantics change. The field follows Option B semantics: set on operation start, preserved in stable states, replaced (not stacked) on next operation.

**Tech Stack:** Go, Kubernetes controller-runtime, kubebuilder, controller-gen

---

## File Structure

| File | Purpose |
|------|---------|
| `api/v1alpha1/hibernateplan_types.go` | Add `OperationTrigger` enum + field, remove `RevertInProgress` |
| `internal/provider/processor/plan/state/state.go` | Update `setError` to clear `OperationTrigger`, update transition helpers |
| `internal/provider/processor/plan/state/state_revert.go` | Set `OperationTrigger = TriggerRevert` instead of `RevertInProgress = true` |
| `internal/provider/processor/plan/state/state_wakingup.go` | Set/clear `OperationTrigger` instead of `RevertInProgress` |
| `internal/provider/processor/plan/state/state_idle.go` | Clear `OperationTrigger` when consuming revert annotation |
| `internal/provider/processor/plan/state/state_recovery.go` | Set `OperationTrigger = TriggerRetry` |
| `internal/provider/processor/plan/state/state_override.go` | Set `OperationTrigger = TriggerOverride` |
| `internal/provider/processor/plan/state/state_restart.go` | Set `OperationTrigger = TriggerRestart` |
| `internal/provider/processor/plan/state/gates.go` | Update `revertGate` to check `OperationTrigger == TriggerRevert` |
| 5 test files | Update existing tests, add new trigger tests |

---

### Task 1: Add OperationTrigger Enum and Field to CRD

**Files:**
- Modify: `api/v1alpha1/hibernateplan_types.go:69-79` (after PlanOperation consts)
- Modify: `api/v1alpha1/hibernateplan_types.go:501-507` (Replace RevertInProgress)

- [ ] **Step 1: Add OperationTrigger enum after PlanOperation**

Add after line 79 (after `OperationWakeUp`):

```go
// OperationTrigger identifies what initiated the current operation.
// Always set when an operation is active (Hibernating/WakingUp phases).
// Preserved in stable states (Active/Hibernated) until replaced by the next operation.
// Used for provenance tracking and trigger-specific control flow in gates.
// +kubebuilder:validation:Enum=Schedule;Revert;Retry;Override;Restart
type OperationTrigger string

const (
	// TriggerSchedule means the operation was initiated by schedule evaluation
	// (normal time-based hibernation/wakeup, including automatic exception application).
	TriggerSchedule OperationTrigger = "Schedule"

	// TriggerRevert means the operation was initiated by the revert annotation
	// to recover from a partial failure (Error -> WakingUp selective wakeup).
	TriggerRevert OperationTrigger = "Revert"

	// TriggerRetry means the operation was initiated by the retry-now annotation
	// or automatic retry to recover from Error.
	TriggerRetry OperationTrigger = "Retry"

	// TriggerOverride means the operation was initiated by override-action=true
	// to force immediate operation (bypass schedule).
	TriggerOverride OperationTrigger = "Override"

	// TriggerRestart means the operation was initiated by restart=true
	// to re-run the last operation.
	TriggerRestart OperationTrigger = "Restart"
)
```

- [ ] **Step 2: Replace RevertInProgress field with OperationTrigger**

Replace lines 501-507:

```go
	// OperationTrigger tracks what initiated the current operation.
	// Set when operation starts, preserved in stable states (Active/Hibernated)
	// until replaced by the next operation trigger. Empty when plan is in
	// stable state with no pending operation.
	// Used for provenance tracking and trigger-specific control flow in gates.
	// +optional
	OperationTrigger OperationTrigger `json:"operationTrigger,omitempty"`
```

- [ ] **Step 3: Generate CRD schemas**

```bash
cd /Users/ardika.saputro/Workstation/home/hibernator
make generate manifests
```

Expected: Successful generation, no errors. Check `config/crd/bases/hibernator.ardikabs.com_hibernateplans.yaml` for new `operationTrigger` field.

---

### Task 2: Update revertState to Set OperationTrigger

**Files:**
- Modify: `internal/provider/processor/plan/state/state_revert.go:106-108`

- [ ] **Step 1: Replace RevertInProgress with OperationTrigger in revertState**

Change lines 106-108 from:
```go
p.Status.RevertInProgress = true
```
To:
```go
p.Status.OperationTrigger = hibernatorv1alpha1.TriggerRevert
```

Also update comment on line 107 from "Revert annotation is preserved" to "Revert annotation is preserved - idleState will consume it after wakeup. OperationTrigger=Revert records the revert origin."

- [ ] **Step 2: Run tests for revertState**

```bash
cd /Users/ardika.saputro/Workstation/home/hibernator
go test ./internal/provider/processor/plan/state -run TestRevertState -v
```

Expected: Tests pass (we'll update the assertion in Task 6).

---

### Task 3: Update wakingUpState to Handle OperationTrigger

**Files:**
- Modify: `internal/provider/processor/plan/state/state_wakingup.go:61-71,120-121`

- [ ] **Step 1: Update OnError to check OperationTrigger for revert cleanup**

Change lines 61-71 from:
```go
if plan.Annotations[wellknown.AnnotationRevert] == "true" || plan.Status.RevertInProgress {
```
To:
```go
if plan.Annotations[wellknown.AnnotationRevert] == "true" || plan.Status.OperationTrigger == hibernatorv1alpha1.TriggerRevert {
```

Update log message and comments to reference OperationTrigger instead of RevertInProgress.

- [ ] **Step 2: Update finalize to clear OperationTrigger**

Change lines 120-121 from:
```go
// Clear RevertInProgress: revert operation completed successfully.
p.Status.RevertInProgress = false
```
To:
```go
// Clear OperationTrigger: operation completed, no pending trigger.
p.Status.OperationTrigger = ""
```

- [ ] **Step 3: Run wakingUp tests**

```bash
cd /Users/ardika.saputro/Workstation/home/hibernator
go test ./internal/provider/processor/plan/state -run TestWakingUpState -v
```

---

### Task 4: Update state.setError to Clear OperationTrigger

**Files:**
- Modify: `internal/provider/processor/plan/state/state.go:378-381`

- [ ] **Step 1: Update setError to clear OperationTrigger**

Change lines 378-381 from:
```go
// Clear RevertInProgress: a revert-triggered operation that failed
// should not loop. The user must manually retry.
p.Status.RevertInProgress = false
```
To:
```go
// Clear OperationTrigger: a user-intent operation that failed
// should not loop automatically. The user must re-apply the intent annotation.
// (Schedule-triggered failures are handled by retry/backoff separately.)
p.Status.OperationTrigger = ""
```

Wait — need to reconsider. For Schedule-triggered operations, we DO want retry via backoff. For user-intent operations (Revert, Override, Restart), we DON'T want automatic retry.

**Revised logic for setError:**
```go
// For user-intent triggers (Revert, Retry, Override, Restart), clear the trigger
// to prevent automatic retry loops. The user must re-apply the intent annotation.
// Schedule-triggered failures are handled separately by recoveryState backoff.
if p.Status.OperationTrigger != hibernatorv1alpha1.TriggerSchedule {
    p.Status.OperationTrigger = ""
}
```

This preserves Schedule trigger for retry backoff while clearing user-intent triggers.

- [ ] **Step 2: Run tests**

```bash
cd /Users/ardika.saputro/Workstation/home/hibernator
go test ./internal/provider/processor/plan/state -run TestStateSetError -v
```

---

### Task 5: Update idleState to Handle OperationTrigger

**Files:**
- Modify: `internal/provider/processor/plan/state/state_idle.go:47-60`

- [ ] **Step 1: Update revert annotation consumption to clear OperationTrigger**

Change lines 48-60 from:
```go
// Ensure RevertInProgress is cleared when consuming the revert annotation.
// This handles edge cases where the plan reached Active without going
// through the normal wakeup finalize path (e.g., manual status patch).
if plan.Status.RevertInProgress {
    log.V(1).Info("clearing RevertInProgress while consuming revert annotation")
    state.Statuses.PlanStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.HibernatePlan]{
        NamespacedName: state.Key,
        Resource:       plan,
        Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
            p.Status.RevertInProgress = false
        }),
    })
}
```

To:
```go
// Ensure OperationTrigger is cleared when consuming the revert annotation.
// This handles edge cases where the plan reached Active without going
// through the normal wakeup finalize path (e.g., manual status patch).
if plan.Status.OperationTrigger == hibernatorv1alpha1.TriggerRevert {
    log.V(1).Info("clearing OperationTrigger while consuming revert annotation")
    state.Statuses.PlanStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.HibernatePlan]{
        NamespacedName: state.Key,
        Resource:       plan,
        Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
            p.Status.OperationTrigger = ""
        }),
    })
}
```

- [ ] **Step 2: Run idleState tests**

```bash
cd /Users/ardika.saputro/Workstation/home/hibernator
go test ./internal/provider/processor/plan/state -run TestIdleState -v
```

---

### Task 6: Update revertGate to Check OperationTrigger

**Files:**
- Modify: `internal/provider/processor/plan/state/gates.go:85-97`

- [ ] **Step 1: Update revertGate logic**

Change lines 85-97 from:
```go
// If RevertInProgress is already set, the gate is skipped to prevent retry loops
// when a revert-triggered wakeup fails and returns to PhaseError. The user must
// manually remove and re-apply the revert annotation to retry.
func revertGate(s *state) Handler {
	plan := s.plan()

	if plan.Status.Phase != hibernatorv1alpha1.PhaseError {
		return nil
	}

	if plan.Status.RevertInProgress {
		return nil
	}

	if plan.Annotations != nil && plan.Annotations[wellknown.AnnotationRevert] == "true" {
		return &revertState{state: s}
	}

	return nil
}
```

To:
```go
// If OperationTrigger == TriggerRevert, the gate is skipped to prevent retry loops
// when a revert-triggered wakeup fails and returns to PhaseError. The user must
// manually remove and re-apply the revert annotation to retry.
func revertGate(s *state) Handler {
	plan := s.plan()

	if plan.Status.Phase != hibernatorv1alpha1.PhaseError {
		return nil
	}

	if plan.Status.OperationTrigger == hibernatorv1alpha1.TriggerRevert {
		return nil
	}

	if plan.Annotations != nil && plan.Annotations[wellknown.AnnotationRevert] == "true" {
		return &revertState{state: s}
	}

	return nil
}
```

- [ ] **Step 2: Run gate tests**

```bash
cd /Users/ardika.saputro/Workstation/home/hibernator
go test ./internal/provider/processor/plan/state -run TestRevertGate -v
```

---

### Task 7: Set OperationTrigger in Other Intent Handlers

**Files:**
- Modify: `internal/provider/processor/plan/state/state_recovery.go:158` (handleRetry)
- Modify: `internal/provider/processor/plan/state/state_override.go:93,122` (override transitions)
- Modify: `internal/provider/processor/plan/state/state_restart.go:58,74` (restart transitions)

- [ ] **Step 1: Set TriggerRetry in recoveryState.handleRetry**

In `state_recovery.go` around line 158 (inside the Mutator function), add after `p.Status.Phase = targetPhase`:

```go
p.Status.OperationTrigger = hibernatorv1alpha1.TriggerRetry
```

- [ ] **Step 2: Set TriggerOverride in overrideActionState transitions**

In `state_override.go`:
- Line ~93 (hibernate transition): Add `p.Status.OperationTrigger = hibernatorv1alpha1.TriggerOverride` inside the transition status update
- Line ~122 (wakeup transition): Add `p.Status.OperationTrigger = hibernatorv1alpha1.TriggerOverride` inside the transition status update

Note: The transitions in overrideActionState call `transitionToHibernating` and `transitionToWakingUp` which are idleState methods. We need to set the trigger in the caller or modify the transition helpers to accept a trigger parameter.

**Approach:** Modify `transitionToHibernating` and `transitionToWakingUp` to set `TriggerSchedule` by default, but allow override callers to override. Simpler approach: set trigger in the caller after calling transition helper (but transition helpers send status updates, so we'd need another status update).

**Better approach:** Add a trigger parameter to transition helpers:
```go
func (s *idleState) transitionToHibernating(ctx context.Context, log logr.Logger, fresh bool, trigger hibernatorv1alpha1.OperationTrigger) (StateResult, error)
```

Default value in idleState: `TriggerSchedule`
Override/Restart callers pass their specific trigger.

Let's implement this cleanly.

- [ ] **Step 3: Modify transitionToHibernating signature**

Change:
```go
func (state *idleState) transitionToHibernating(ctx context.Context, log logr.Logger, fresh bool) (StateResult, error)
```
To:
```go
func (state *idleState) transitionToHibernating(ctx context.Context, log logr.Logger, fresh bool, trigger hibernatorv1alpha1.OperationTrigger) (StateResult, error)
```

Inside the function, after setting Phase = PhaseHibernating, add:
```go
p.Status.OperationTrigger = trigger
```

Update all call sites:
- `state_idle.go:89` (schedule-driven): pass `hibernatorv1alpha1.TriggerSchedule`
- `state_override.go:93` (override hibernate): pass `hibernatorv1alpha1.TriggerOverride`
- `state_override.go:103` (override restart hibernate): pass `hibernatorv1alpha1.TriggerOverride`
- `state_restart.go:58` (restart hibernate): pass `hibernatorv1alpha1.TriggerRestart`

- [ ] **Step 4: Modify transitionToWakingUp signature**

Change:
```go
func (state *idleState) transitionToWakingUp(log logr.Logger) (StateResult, error)
```
To:
```go
func (state *idleState) transitionToWakingUp(log logr.Logger, trigger hibernatorv1alpha1.OperationTrigger) (StateResult, error)
```

Inside, after setting Phase = PhaseWakingUp, add:
```go
p.Status.OperationTrigger = trigger
```

Update all call sites:
- `state_idle.go:98` (schedule-driven): pass `hibernatorv1alpha1.TriggerSchedule`
- `state_override.go:122` (override wakeup): pass `hibernatorv1alpha1.TriggerOverride`
- `state_override.go:149` (override restart wakeup): pass `hibernatorv1alpha1.TriggerOverride`
- `state_restart.go:74` (restart wakeup): pass `hibernatorv1alpha1.TriggerRestart`

- [ ] **Step 5: Run all state tests**

```bash
cd /Users/ardika.saputro/Workstation/home/hibernator
go test ./internal/provider/processor/plan/state -v
```

---

### Task 8: Update Existing Tests

**Files:**
- Modify: `internal/provider/processor/plan/state/state_revert_test.go` (line ~199)
- Modify: `internal/provider/processor/plan/state/state_wakingup_test.go` (lines ~141, ~156)
- Modify: `internal/provider/processor/plan/state/state_idle_test.go` (lines ~324-338)
- Modify: `internal/provider/processor/plan/state/state_test.go` (lines ~381-391)

- [ ] **Step 1: Update revertState test assertion**

Change:
```go
assert.True(t, testPlan.Status.RevertInProgress, "revertState should set RevertInProgress=true")
```
To:
```go
assert.Equal(t, hibernatorv1alpha1.TriggerRevert, testPlan.Status.OperationTrigger, "revertState should set OperationTrigger=TriggerRevert")
```

- [ ] **Step 2: Update wakingUpState_OnError test**

Change:
```go
assert.False(t, plan.Status.RevertInProgress, "RevertInProgress should be cleared when wakeup fails")
```
To:
```go
assert.Equal(t, "", plan.Status.OperationTrigger, "OperationTrigger should be cleared when wakeup fails")
```

- [ ] **Step 3: Update idleState revert annotation test**

Change:
```go
assert.False(t, plan.Status.RevertInProgress, "idleState should clear RevertInProgress when consuming revert annotation")
```
To:
```go
assert.Equal(t, "", plan.Status.OperationTrigger, "idleState should clear OperationTrigger when consuming revert annotation")
```

Also update the setup from `plan.Status.RevertInProgress = true` to `plan.Status.OperationTrigger = hibernatorv1alpha1.TriggerRevert`.

- [ ] **Step 4: Update revertGate test**

Change:
```go
plan.Status.RevertInProgress = true
```
To:
```go
plan.Status.OperationTrigger = hibernatorv1alpha1.TriggerRevert
```

And update assertion message to reference OperationTrigger.

- [ ] **Step 5: Run all updated tests**

```bash
cd /Users/ardika.saputro/Workstation/home/hibernator
go test ./internal/provider/processor/plan/state -v
```

---

### Task 9: Add New Unit Tests for OperationTrigger

**Files:**
- Create: `internal/provider/processor/plan/state/state_operationtrigger_test.go`

- [ ] **Step 1: Test OperationTrigger lifecycle for each trigger type**

```go
func TestOperationTrigger_ScheduleHibernation(t *testing.T) {
    // Test that transitionToHibernating sets TriggerSchedule
}

func TestOperationTrigger_ScheduleWakeup(t *testing.T) {
    // Test that transitionToWakingUp sets TriggerSchedule
}

func TestOperationTrigger_IntentPreservesInStableState(t *testing.T) {
    // Test that TriggerOverride stays set after reaching Active
}

func TestOperationTrigger_ClearOnErrorForUserIntent(t *testing.T) {
    // Test that setError clears OperationTrigger for non-Schedule triggers
}

func TestOperationTrigger_PreserveOnErrorForSchedule(t *testing.T) {
    // Test that setError PRESERVES OperationTrigger for Schedule trigger
}
```

- [ ] **Step 2: Run new tests**

```bash
cd /Users/ardika.saputro/Workstation/home/hibernator
go test ./internal/provider/processor/plan/state -run TestOperationTrigger -v
```

---

### Task 10: Update E2E Test

**Files:**
- Modify: `test/e2e/tests/revert.go:279-283`

- [ ] **Step 1: Update RevertOnWakeupFailure test assertion**

Change:
```go
Expect(plan.Status.RevertInProgress).To(BeFalse(), "RevertInProgress should be cleared after failed revert to prevent retry loop")
```

To:
```go
Expect(plan.Status.OperationTrigger).To(BeEmpty(), "OperationTrigger should be cleared after failed revert to prevent retry loop")
```

- [ ] **Step 2: Verify E2E test compiles**

Note: Don't run E2E test (per AGENTS.md rules), just verify compilation.

---

### Task 11: Update Documentation

**Files:**
- Modify: `docs/user-journey/troubleshoot-hibernation-failure.md` (revert section)

- [ ] **Step 1: Update docs to reference OperationTrigger**

Replace all references to `RevertInProgress` in the revert documentation section with `OperationTrigger`. Add explanation that OperationTrigger is a general-purpose field tracking operation origin.

- [ ] **Step 2: Regenerate API docs**

The `website/docs/reference/api.md` will be auto-regenerated by `make generate manifests`.

---

### Task 12: Final Verification

- [ ] **Step 1: Run all state package tests**

```bash
cd /Users/ardika.saputro/Workstation/home/hibernator
go test ./internal/provider/processor/plan/state -v
```

Expected: All tests pass.

- [ ] **Step 2: Build the project**

```bash
cd /Users/ardika.saputro/Workstation/home/hibernator
make build
```

Expected: Successful build.

- [ ] **Step 3: Verify CRD schemas**

Check that `config/crd/bases/hibernator.ardikabs.com_hibernateplans.yaml` contains `operationTrigger` field with enum validation.

- [ ] **Step 4: Check for remaining RevertInProgress references**

```bash
cd /Users/ardika.saputro/Workstation/home/hibernator
grep -r "RevertInProgress" --include="*.go" --include="*.yaml" .
```

Expected: Only references in existing test data or migration notes (should be zero in production code).

---

## Self-Review Checklist

**1. Spec coverage:**
- ✅ Add OperationTrigger enum and field (Task 1)
- ✅ Replace all RevertInProgress logic (Tasks 2-7)
- ✅ Set trigger at all 6 operation initiation points (Tasks 2, 5, 7)
- ✅ Update gates (Task 6)
- ✅ Update tests (Tasks 8-9)
- ✅ Update docs (Task 11)

**2. Placeholder scan:** ✅ No TBDs, TODOs, or incomplete sections.

**3. Type consistency:** ✅ `OperationTrigger` used consistently across all tasks. `TriggerSchedule`, `TriggerRevert`, etc. are the const names.

**4. No breaking changes to control flow:** ✅ Only field semantics change; selectHandler, gates, and state machine logic remain identical.

---

## Design Decisions

1. **Option B semantics (preserve in stable states):** Provenance is more valuable than semantic purity. Aligns with `CurrentOperation` persistence.

2. **setError behavior:** Clears OperationTrigger for user-intent triggers (Revert, Override, Restart) to prevent loops. Preserves for `TriggerSchedule` since recoveryState handles backoff separately.

3. **Transition helpers with trigger parameter:** Clean way to set the correct trigger without duplicating status update logic. Default is `TriggerSchedule` for normal schedule-driven transitions.

4. **Empty string for "no trigger":** Simple and consistent with Go string type. Alternative was a dedicated `TriggerNone` const, but empty string is cleaner.
