# Suspend Target Override Implementation Plan (Delta: disabled-as-skipped)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow `type: suspend` ScheduleExceptions to carry `targetOverrides` for partial wakeup (2 of 10), with auto-revert to base plan on expiry — implemented by seeding disabled targets as instantly-completed rather than removing them, so all four strategies work unchanged.

**Architecture:** `disabled` never removes targets from `Spec.Targets`. Transitions seed disabled executions as `StateCompleted/Skipped`; the DAG-disable webhook rejection is deleted; the WakeUp working set freezes to `Status.Executions` once started. Validator otherwise reuses existing checks; scheduler untouched.

**Tech Stack:** Go 1.24, controller-runtime webhooks + fake client tests, `executorparams.ValidateParams`, `samber/lo`.

**Spec:** `docs/superpowers/specs/2026-09-05-suspend-target-override-design.md` (see Delta 2026-09-06 section; supersedes filtering approach)

## Global Constraints

- Only what is specified in override changes; everything else follows base plan spec as-is.
- `suspend` allows only `targetOverrides` (`disabled`, `parameters`); `executionOverride` on `suspend` stays `Forbidden`.
- `parameters` is full replacement per target, not merge. `disabled: true` + `parameters` on one entry: target skipped, parameters ignored.
- `disabled` is always allowed, on any strategy, upstream or downstream (D1/D2). No dependency gate.
- Only one active exception with overrides per plan (extend/replace/suspend share the slot).
- No scheduler change (`applySuspend` trigger, `PlanDAG`, `PlanStaged` untouched).
- All Go binaries to `bin/`; never auto-run `test/e2e/...`.
- No git operations in this plan (no `git add/commit/status/push`). User handles version control separately.

---

## File Structure

- Done: `api/v1alpha1/scheduleexception_types.go` — doc comments (TargetOverrides incl. suspend, ExecutionOverride excl. suspend, Disabled+Parameters interaction).
- Modify: `internal/validationwebhook/scheduleexception_validator.go` — delete `hasDependencyOn` block + function; keep Rule 1 split (executionOverride forbidden, targetOverrides fall through).
- Test: `internal/validationwebhook/scheduleexception_validator_test.go` — flip DAG test to Accepted; keep all other suspend tests.
- Modify: `internal/provider/processor/plan/state/state.go` — `applyTargetOverrides` becomes params-only `applyParameterOverrides`; add `isDisabled` + `buildExecutionsWithSeededSkips`; `effectivePlan` WakeUp branch freezes to executions.
- Modify: `internal/provider/processor/plan/state/state_idle.go` — both transitions seed skipped executions; suspend wakeup no longer subsets the target list.
- Modify: `internal/provider/processor/plan/state/state_execution.go` — `validateRuntimeOverrides` suspend branch validates effective targets (keep `effective` param name from prior fix).
- Test: `internal/provider/processor/plan/state/state_execution_override_test.go` + any other state tests asserting removal — update to seeded expectations.
- Generated/docs: `make generate` refresh; `website/docs` DAG note (disabled always allowed, upstream treated as succeeded).

---

## Status of prior work (filtering approach, already in tree)

- Task 1 (API comments): DONE, kept.
- Task 2 (validator Rule 1 split + 6 suspend tests): DONE, kept except DAG test flip (Task 5).
- Task 3 (filtering runtime + subset tests): SUPERSEDED — rework per Tasks 6–7 below. `findActiveSuspendOverride` helper is kept and reused.
- Task 4 (regen/docs): redo at the end (Task 8).

---

### Task 5: Validator — drop DAG-disable rejection

**Files:**
- Modify: `internal/validationwebhook/scheduleexception_validator.go:465-473,546-557`
- Test: `internal/validationwebhook/scheduleexception_validator_test.go` (`SuspendDisableDAGDependency_Rejected`)

**Interfaces:**
- Consumes: `plan.Spec.Execution.Strategy`, `exception.Spec.TargetOverrides`.
- Produces: `validateExecutionOverrides` accepts any `disabled` combination; safety now comes from runtime seeding (D1).

- [ ] **Step 1: Delete the Disabled/DAG block and helper**

Delete in `validateExecutionOverrides`:

```go
// Validate disabled doesn't break DAG dependencies
if override.Disabled {
	if hasDependencyOn(plan, override.TargetName) {
		allErrs = append(allErrs, field.Invalid(
			overridePath.Child("disabled"),
			true,
			fmt.Sprintf("cannot disable target %q: other targets have dependencies on it", override.TargetName),
		))
	}
}
```

Delete `hasDependencyOn` (lines ~546-557). Check `fmt` still used elsewhere in file (yes: many `Sprintf`).

- [ ] **Step 2: Flip the DAG test to Accepted**

Rename `TestScheduleExceptionValidator_ValidateCreate_SuspendDisableDAGDependency_Rejected` → `..._Accepted`, change final assertions to `require.NoError(t, err)`. Rationale in comment: disabled upstream is seeded instantly-completed at runtime (D1/D2).

- [ ] **Step 3: Run validator tests**

Run: `go test ./internal/validationwebhook/ -count=1`
Expected: PASS

---

### Task 6: Runtime — params-only helper + seeded executions + wakeup freeze

**Files:**
- Modify: `internal/provider/processor/plan/state/state.go` (helper + `effectivePlan`)
- Modify: `internal/provider/processor/plan/state/state_idle.go` (both transitions)
- Test: `internal/provider/processor/plan/state/state_execution_override_test.go`

**Interfaces:**
- Consumes: `s.PlanCtx.Exceptions`, `s.Clock.Now()`, `plan.Spec.Targets`, `plan.Status.{PlanSnapshot,CycleID,CurrentOperation,Executions}`.
- Produces:
  - `func applyParameterOverrides(base []Target, overrides []TargetOverride, log logr.Logger) []Target` (params only; unknown names skipped with log; disabled entries ignored here)
  - `func isDisabled(overrides []TargetOverride, name string) bool`
  - `func buildExecutionsWithSeededSkips(targetList []Target, overrides []TargetOverride, excName, pendingMsg string) []ExecutionStatus` — pending entries normal; disabled entries `State: StateCompleted, Message: "Skipped: disabled by exception <excName>"`
  - `effectivePlan` WakeUp branch: apply live suspend params if active; then if `len(plan.Status.Executions) > 0`, freeze membership to execution-listed names.

- [ ] **Step 1: Rename helper to params-only**

In `state.go`, rename `applyTargetOverrides` → `applyParameterOverrides` and delete the disabled-filter first pass (keep target-map + parameters loop; `Disabled` entries `continue`). Update the 2 call sites (`buildEffectivePlan`, `effectivePlan` WakeUp branch). `lo` import becomes unused — remove it (check: `lo.Filter` was the only `lo` use in state.go; `lo.Ternary` is in state_execution.go, separate file).

- [ ] **Step 2: Add seeding helpers**

```go
// isDisabled reports whether overrides disable the named target.
func isDisabled(overrides []hibernatorv1alpha1.TargetOverride, name string) bool {
	for _, o := range overrides {
		if o.TargetName == name && o.Disabled {
			return true
		}
	}
	return false
}

// buildExecutionsWithSeededSkips builds ExecutionStatus entries for a transition.
// Disabled targets are seeded StateCompleted ("virtually succeeded") so strategy
// artifacts (DAG edges, stage membership) stay whole and stages can complete.
func buildExecutionsWithSeededSkips(targetList []hibernatorv1alpha1.Target, overrides []hibernatorv1alpha1.TargetOverride, excName, pendingMsg string) []hibernatorv1alpha1.ExecutionStatus {
	executions := make([]hibernatorv1alpha1.ExecutionStatus, len(targetList))
	for i, t := range targetList {
		if excName != "" && isDisabled(overrides, t.Name) {
			executions[i] = hibernatorv1alpha1.ExecutionStatus{
				Target:   t.Name,
				Executor: t.Type,
				State:    hibernatorv1alpha1.StateCompleted,
				Message:  fmt.Sprintf("Skipped: disabled by exception %s", excName),
			}
			continue
		}
		executions[i] = hibernatorv1alpha1.ExecutionStatus{
			Target:   t.Name,
			Executor: t.Type,
			State:    hibernatorv1alpha1.StatePending,
			Message:  pendingMsg,
		}
	}
	return executions
}
```

`fmt` is already imported in state.go.

- [ ] **Step 3: Seed at transitionToHibernating**

In `state_idle.go transitionToHibernating`, replace the executions loop with:

```go
var overrideTargets []hibernatorv1alpha1.TargetOverride
if exc := state.findActiveExceptionOverride(); exc != nil {
	overrideTargets = exc.Spec.TargetOverrides
}
executions := buildExecutionsWithSeededSkips(effectivePlan.Spec.Targets, overrideTargets, appliedExceptionName, "Target pending hibernation")
```

(`appliedExceptionOverride` is already resolved just above; when empty, `excName == ""` disables seeding.)

- [ ] **Step 4: Seed at transitionToWakingUp, drop subsetting**

Replace the suspend-subset branch with full-list + seeding:

```go
var targetList []hibernatorv1alpha1.Target
if snap := plan.Status.PlanSnapshot; snap != nil && snap.CycleID == plan.Status.CurrentCycleID {
	targetList = snap.Targets
	// ... existing logs unchanged
} else if ...
```

then:

```go
var wakeOverrides []hibernatorv1alpha1.TargetOverride
wakeExcName := ""
if sus := state.findActiveSuspendOverride(); sus != nil {
	wakeOverrides = sus.Spec.TargetOverrides
	wakeExcName = sus.Name
	log.V(1).Info("seeding skipped executions for suspend wakeup", "exception", sus.Name)
}
executions := buildExecutionsWithSeededSkips(targetList, wakeOverrides, wakeExcName, "Target pending wakeup")
```

Delete the previous `applyTargetOverrides` subset branch. Snapshot/AppliedExceptionOverride still preserved (anti-leak unchanged).

- [ ] **Step 5: Freeze WakeUp working set in effectivePlan**

```go
if plan.Status.CurrentOperation == hibernatorv1alpha1.OperationWakeUp {
	if sus := s.findActiveSuspendOverride(); sus != nil {
		log := s.Log.WithValues("plan", s.Key.String(), "exception", sus.Name)
		effective := plan.DeepCopy()
		effective.Spec.Targets = applyParameterOverrides(effective.Spec.Targets, sus.Spec.TargetOverrides, log)
		return freezeToExecutions(effective, plan.Status.Executions)
	}
	if len(plan.Status.Executions) > 0 {
		// Exception expired/deleted mid-wakeup: run the frozen execution set
		// with base params instead of stranding the cycle.
		return freezeToExecutions(plan.DeepCopy(), plan.Status.Executions)
	}
}
```

with:

```go
// freezeToExecutions keeps only targets listed in the frozen Status.Executions
// (locked intent). Stages and executions can never disagree mid-cycle.
func freezeToExecutions(effective *hibernatorv1alpha1.HibernatePlan, executions []hibernatorv1alpha1.ExecutionStatus) *hibernatorv1alpha1.HibernatePlan {
	keep := make(map[string]bool, len(executions))
	for _, e := range executions {
		keep[e.Target] = true
	}
	kept := effective.Spec.Targets[:0]
	for _, t := range effective.Spec.Targets {
		if keep[t.Name] {
			kept = append(kept, t)
		}
	}
	effective.Spec.Targets = kept
	return effective
}
```

Note: `kept := effective.Spec.Targets[:0]` reuses backing array of the DeepCopy — safe (never aliases live plan).

- [ ] **Step 6: Update tests to seeded expectations**

Rewrite the two suspend tests from the filtering implementation:

```go
func TestEffectivePlan_SuspendOverride_KeepsFullTargets(t *testing.T) {
	// ... same setup (2 targets, snapshot, suspend disabling db-1)
	ep := st.effectivePlan(plan)
	require.NotNil(t, ep)
	require.Len(t, ep.Spec.Targets, 2, "disabled never removes targets")
}
```

```go
func TestTransitionToWakingUp_SuspendOverride_SeedsSkippedExecutions(t *testing.T) {
	// ... same setup, call h.transitionToWakingUp, apply mutator
	require.Len(t, testPlan.Status.Executions, 2)
	assert.Equal(t, "db-0", testPlan.Status.Executions[0].Target)
	assert.Equal(t, hibernatorv1alpha1.StatePending, testPlan.Status.Executions[0].State)
	assert.Equal(t, "db-1", testPlan.Status.Executions[1].Target)
	assert.Equal(t, hibernatorv1alpha1.StateCompleted, testPlan.Status.Executions[1].State)
	assert.Contains(t, testPlan.Status.Executions[1].Message, "Skipped: disabled by exception weekend")
	require.NotNil(t, testPlan.Status.PlanSnapshot)
	assert.Len(t, testPlan.Status.PlanSnapshot.Targets, 2)
	assert.Equal(t, "", testPlan.Status.AppliedExceptionOverride)
}
```

Add freeze test:

```go
func TestEffectivePlan_WakeUpExpiredSuspend_FrozenToExecutions(t *testing.T) {
	// plan WakingUp, executions = [db-0 pending, db-1 completed-skipped],
	// NO active suspend (validity past) → effectivePlan keeps 2 targets (frozen),
	// snapshot (if any) cannot resurrect removed... (targets still full here;
	// freeze matters when base spec changed mid-cycle: add db-2 to Spec.Targets
	// and assert it is excluded)
}
```

Then run the FULL state suite and fix every removal-assuming test to seeded expectations:

Run: `go test ./internal/provider/processor/plan/state/ -count=1`
Known affected (grep `Disabled: true`): `TestBuildEffectivePlan_DisabledTarget`, tests at lines ~433/562/644/706, fresh-cycle test (~952, expects 1 execution → now 2 with 1 seeded), `state_override_test.go:281,420`, `state_restart_test.go:83`. Update each: full target list retained; executions include seeded `StateCompleted` with Skipped message. `TestBuildEffectivePlan_UnknownTarget_Skipped` and `SuspendException_ReturnsNil` behavior unchanged (unknown still skipped; hibernate still ignores suspend).

- [ ] **Step 7: Run state + validator suites**

Run: `go test ./internal/provider/processor/plan/state/ ./internal/validationwebhook/ -count=1`
Expected: PASS

---

### Task 7: Docs + regen

**Files:**
- `website/docs/user-guides/schedule-exceptions.md` (suspend example comment already says "disabled targets stay stopped" — still true; add DAG sentence)
- `website/docs/scenarios/event-mode-overrides.md` (no change needed unless it claims otherwise)
- Generated CRDs/API docs via `make generate` (picks up Disabled godoc if changed — none planned)

- [ ] **Step 1: Document upstream-disable on DAG**

In `schedule-exceptions.md` Execution Overrides section append:

```md
!!! note "Disabling on DAG plans"
    Disabling any target — upstream or downstream — is allowed. A disabled target is treated as instantly succeeded, so dependents proceed normally.
```

- [ ] **Step 2: Regen + full affected suites**

Run: `make generate`
Run: `go test ./api/... ./internal/validationwebhook/... ./internal/scheduler/... ./internal/provider/processor/plan/state/... -count=1`
Expected: all PASS.

---

## Self-Review

- D1 → Task 6 Steps 2-4 (seeding at both transitions; params-only helper Step 1).
- D2 → Task 5 (delete gate + helper; flip test).
- D3 → Task 6 Step 5 (freeze helper + branch) + freeze test Step 6.
- D4 → no planner/scheduler task; regression in Task 7 Step 2.
- No placeholders: file paths, code blocks, exact test commands in every step.
- Type consistency: `applyParameterOverrides([]Target, []TargetOverride, logr.Logger) []Target`; `buildExecutionsWithSeededSkips([]Target, []TargetOverride, string, string) []ExecutionStatus`; `freezeToExecutions(*HibernatePlan, []ExecutionStatus) *HibernatePlan`; `isDisabled([]TargetOverride, string) bool`.
