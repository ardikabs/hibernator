# PlanSnapshot Lifecycle Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix `PlanSnapshot` and `AppliedExceptionOverride` to persist across the entire hibernation/wakeup cycle, only being replaced when a new cycle starts.

**Architecture:** Remove premature clears from `hibernatingState.finalize()` and `wakingUpState.finalize()`. Modify `transitionToWakingUp()` to stop rebuilding from live exceptions and instead trust the existing snapshot (with `CycleID` guard). Add/update tests to reflect the correct lifecycle.

**Tech Stack:** Go, controller-runtime, testify

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/provider/processor/plan/state/state_hibernating.go` | Hibernating phase handler — remove snapshot clear from finalize |
| `internal/provider/processor/plan/state/state_wakingup.go` | WakingUp phase handler — remove snapshot clear from finalize |
| `internal/provider/processor/plan/state/state_idle.go` | Idle phase handler — stop rebuilding snapshot in transitionToWakingUp |
| `internal/provider/processor/plan/state/state_execution_override_test.go` | Unit tests for execution override and snapshot lifecycle |

---

### Task 1: Fix hibernatingState.finalize() to preserve snapshot

**Files:**
- Modify: `internal/provider/processor/plan/state/state_hibernating.go:100-105`
- Test: `internal/provider/processor/plan/state/state_execution_override_test.go:742-767`

- [ ] **Step 1: Remove snapshot clear from hibernatingState.finalize()**

In `state_hibernating.go`, remove these two lines from the Mutator in `finalize()`:

```go
p.Status.AppliedExceptionOverride = ""
p.Status.PlanSnapshot = nil
```

The Mutator block should look like:

```go
Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
    p.Status.Phase = hibernatorv1alpha1.PhaseHibernated
    p.Status.LastTransitionTime = ptr.To(metav1.NewTime(state.Clock.Now()))

    cycleIdx := findOrAppendCycle(&p.Status, currentCycleID)
    p.Status.ExecutionHistory[cycleIdx].ShutdownExecution = summary
    pruneCycleHistory(&p.Status)

    p.Status.RetryCount = 0
    p.Status.LastRetryTime = nil
    p.Status.ErrorMessage = ""
    // PlanSnapshot and AppliedExceptionOverride are preserved across the cycle
}),
```

- [ ] **Step 2: Update test TestHibernatingState_Finalize_ClearsPlanSnapshot**

In `state_execution_override_test.go`, rename and fix the test to assert fields are **preserved**:

```go
func TestHibernatingState_Finalize_PreservesPlanSnapshot(t *testing.T) {
    plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernating)
    plan.Status.CurrentCycleID = "cycle-001"
    plan.Status.PlanSnapshot = &hibernatorv1alpha1.PlanSnapshot{
        CycleID:       "cycle-001",
        ExceptionName: "override-exc",
    }
    plan.Status.AppliedExceptionOverride = "override-exc"
    plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
        {Target: "db", State: hibernatorv1alpha1.StateCompleted},
    }

    c := newHandlerFakeClient(plan)
    st := newHandlerState(plan, c)
    h := &hibernatingState{state: st}

    h.finalize(nil, st.Log, scheduler.ExecutionPlan{})

    upd := <-planStatuses(st).C()
    require.NotNil(t, upd.Mutator)

    testPlan := plan.DeepCopy()
    upd.Mutator.Mutate(testPlan)

    assert.NotNil(t, testPlan.Status.PlanSnapshot)
    assert.Equal(t, "override-exc", testPlan.Status.PlanSnapshot.ExceptionName)
    assert.Equal(t, "override-exc", testPlan.Status.AppliedExceptionOverride)
}
```

- [ ] **Step 3: Run tests**

```bash
cd /Users/ardika.saputro/Workstation/home/hibernator
go test ./internal/provider/processor/plan/state/... -run TestHibernatingState_Finalize_PreservesPlanSnapshot -v
```

Expected: PASS

---

### Task 2: Fix wakingUpState.finalize() to preserve snapshot

**Files:**
- Modify: `internal/provider/processor/plan/state/state_wakingup.go:103-107`
- Test: `internal/provider/processor/plan/state/state_execution_override_test.go:769-794`

- [ ] **Step 1: Remove snapshot clear from wakingUpState.finalize()**

In `state_wakingup.go`, remove these two lines from the Mutator in `finalize()`:

```go
p.Status.AppliedExceptionOverride = ""
p.Status.PlanSnapshot = nil
```

The Mutator block should look like:

```go
Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
    p.Status.Phase = hibernatorv1alpha1.PhaseActive
    p.Status.LastTransitionTime = ptr.To(metav1.NewTime(state.Clock.Now()))

    cycleIdx := findOrAppendCycle(&p.Status, currentCycleID)
    p.Status.ExecutionHistory[cycleIdx].WakeupExecution = summary
    pruneCycleHistory(&p.Status)

    p.Status.RetryCount = 0
    p.Status.LastRetryTime = nil
    p.Status.ErrorMessage = ""
    // PlanSnapshot and AppliedExceptionOverride are preserved across the cycle
}),
```

- [ ] **Step 2: Update test TestWakingUpState_Finalize_ClearsPlanSnapshot**

In `state_execution_override_test.go`, rename and fix the test to assert fields are **preserved**:

```go
func TestWakingUpState_Finalize_PreservesPlanSnapshot(t *testing.T) {
    plan := basePlanForState("p", hibernatorv1alpha1.PhaseWakingUp)
    plan.Status.CurrentCycleID = "cycle-001"
    plan.Status.PlanSnapshot = &hibernatorv1alpha1.PlanSnapshot{
        CycleID:       "cycle-001",
        ExceptionName: "override-exc",
    }
    plan.Status.AppliedExceptionOverride = "override-exc"
    plan.Status.Executions = []hibernatorv1alpha1.ExecutionStatus{
        {Target: "db", State: hibernatorv1alpha1.StateCompleted},
    }

    c := newHandlerFakeClient(plan)
    st := newHandlerState(plan, c)
    h := &wakingUpState{state: st}

    h.finalize(nil, st.Log, scheduler.ExecutionPlan{})

    upd := <-planStatuses(st).C()
    require.NotNil(t, upd.Mutator)

    testPlan := plan.DeepCopy()
    upd.Mutator.Mutate(testPlan)

    assert.NotNil(t, testPlan.Status.PlanSnapshot)
    assert.Equal(t, "override-exc", testPlan.Status.PlanSnapshot.ExceptionName)
    assert.Equal(t, "override-exc", testPlan.Status.AppliedExceptionOverride)
}
```

- [ ] **Step 3: Run tests**

```bash
cd /Users/ardika.saputro/Workstation/home/hibernator
go test ./internal/provider/processor/plan/state/... -run TestWakingUpState_Finalize_PreservesPlanSnapshot -v
```

Expected: PASS

---

### Task 3: Fix transitionToWakingUp to reuse existing snapshot

**Files:**
- Modify: `internal/provider/processor/plan/state/state_idle.go:138-197`
- Test: `internal/provider/processor/plan/state/state_execution_override_test.go` (new tests)

- [ ] **Step 1: Remove live exception rebuild from transitionToWakingUp()**

The current `transitionToWakingUp()` builds `effectivePlan` from live exceptions and sets `PlanSnapshot`. This should stop — the snapshot was already captured during `transitionToHibernating()` and should be reused.

Replace the body of `transitionToWakingUp()` with a version that does NOT rebuild from live exceptions:

```go
func (state *idleState) transitionToWakingUp(log logr.Logger) (StateResult, error) {
    plan := state.plan()

    now := state.Clock.Now()

    // Use the existing PlanSnapshot targets if available for this cycle.
    // The snapshot was captured during transitionToHibernating and should be
    // reused for the wakeup operation to ensure cycle intent locking.
    var targetList []hibernatorv1alpha1.Target
    if snap := plan.Status.PlanSnapshot; snap != nil && snap.CycleID == plan.Status.CurrentCycleID {
        targetList = snap.Targets
        log.V(1).Info("reusing plan snapshot targets for wakeup", "cycleID", snap.CycleID, "exception", snap.ExceptionName)
    } else {
        targetList = plan.Spec.Targets
        log.V(1).Info("no plan snapshot for current cycle, using live plan targets")
    }

    executions := make([]hibernatorv1alpha1.ExecutionStatus, len(targetList))
    for i, t := range targetList {
        executions[i] = hibernatorv1alpha1.ExecutionStatus{
            Target:   t.Name,
            Executor: t.Type,
            State:    hibernatorv1alpha1.StatePending,
            Message:  "Target pending wakeup",
        }
    }

    previousPhase := plan.Status.Phase
    state.Statuses.PlanStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.HibernatePlan]{
        NamespacedName: state.Key,
        Resource:       plan,
        Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
            p.Status.Phase = hibernatorv1alpha1.PhaseWakingUp
            p.Status.CurrentStageIndex = 0
            p.Status.CurrentOperation = hibernatorv1alpha1.OperationWakeUp
            p.Status.Executions = executions
            p.Status.LastTransitionTime = ptr.To(metav1.NewTime(now))
            // PlanSnapshot and AppliedExceptionOverride are preserved from hibernation
        }),
        PostHook: chainHooks(
            state.notifyHook(hibernatorv1alpha1.EventStart, func(p *hibernatorv1alpha1.HibernatePlan) notification.Payload {
                return buildPayload(p, hibernatorv1alpha1.EventStart, state.Clock.Now)
            }),
            state.phaseChangePostHook(previousPhase),
        ),
    })

    log.V(1).Info("queued transition to WakingUp", "cycleID", plan.Status.CurrentCycleID)
    return StateResult{Requeue: true}, nil
}
```

- [ ] **Step 2: Add test for snapshot reuse on wakeup**

Add to `state_execution_override_test.go`:

```go
func TestTransitionToWakingUp_ReusesPlanSnapshot(t *testing.T) {
    plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernated)
    plan.Status.CurrentCycleID = "cycle-001"
    plan.Status.PlanSnapshot = &hibernatorv1alpha1.PlanSnapshot{
        CycleID:       "cycle-001",
        ExceptionName: "override-exc",
        Targets: []hibernatorv1alpha1.Target{
            {Name: "db", Type: "rds"},
        },
        Execution: hibernatorv1alpha1.ExecutionConfig{
            Strategy: hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategySequential},
        },
    }
    plan.Status.AppliedExceptionOverride = "override-exc"
    plan.Spec.Targets = []hibernatorv1alpha1.Target{
        {Name: "app", Type: "eks"},
        {Name: "db", Type: "rds"},
    }

    c := newHandlerFakeClient(plan)
    st := newHandlerState(plan, c)
    i := &idleState{state: st}

    _, err := i.transitionToWakingUp(st.Log)
    require.NoError(t, err)

    upd := <-planStatuses(st).C()
    require.NotNil(t, upd.Mutator)

    testPlan := plan.DeepCopy()
    upd.Mutator.Mutate(testPlan)

    assert.Equal(t, hibernatorv1alpha1.PhaseWakingUp, testPlan.Status.Phase)
    // Should reuse snapshot targets (only "db"), not live spec targets ("app" + "db")
    require.Len(t, testPlan.Status.Executions, 1)
    assert.Equal(t, "db", testPlan.Status.Executions[0].Target)
    // Snapshot and override should be preserved
    require.NotNil(t, testPlan.Status.PlanSnapshot)
    assert.Equal(t, "override-exc", testPlan.Status.PlanSnapshot.ExceptionName)
    assert.Equal(t, "override-exc", testPlan.Status.AppliedExceptionOverride)
}

func TestTransitionToWakingUp_FallsBackToLiveWhenNoSnapshot(t *testing.T) {
    plan := basePlanForState("p", hibernatorv1alpha1.PhaseHibernated)
    plan.Status.CurrentCycleID = "cycle-001"
    // No PlanSnapshot set
    plan.Spec.Targets = []hibernatorv1alpha1.Target{
        {Name: "app", Type: "eks"},
        {Name: "db", Type: "rds"},
    }

    c := newHandlerFakeClient(plan)
    st := newHandlerState(plan, c)
    i := &idleState{state: st}

    _, err := i.transitionToWakingUp(st.Log)
    require.NoError(t, err)

    upd := <-planStatuses(st).C()
    require.NotNil(t, upd.Mutator)

    testPlan := plan.DeepCopy()
    upd.Mutator.Mutate(testPlan)

    assert.Equal(t, hibernatorv1alpha1.PhaseWakingUp, testPlan.Status.Phase)
    // Should use live spec targets
    require.Len(t, testPlan.Status.Executions, 2)
    assert.Equal(t, "app", testPlan.Status.Executions[0].Target)
    assert.Equal(t, "db", testPlan.Status.Executions[1].Target)
}
```

- [ ] **Step 3: Run tests**

```bash
cd /Users/ardika.saputro/Workstation/home/hibernator
go test ./internal/provider/processor/plan/state/... -run "TestTransitionToWakingUp_ReusesPlanSnapshot|TestTransitionToWakingUp_FallsBackToLiveWhenNoSnapshot" -v
```

Expected: PASS

---

### Task 4: Verify full test suite

- [ ] **Step 1: Run the full state package test suite**

```bash
cd /Users/ardika.saputro/Workstation/home/hibernator
go test ./internal/provider/processor/plan/state/... -v
```

Expected: All tests PASS

- [ ] **Step 2: Check for any other tests that may encode the buggy behavior**

Search for tests that assert `PlanSnapshot == nil` or `AppliedExceptionOverride == ""` after finalize:

```bash
cd /Users/ardika.saputro/Workstation/home/hibernator
grep -rn "PlanSnapshot.*nil\|AppliedExceptionOverride.*\"\"" internal/provider/processor/plan/state/*_test.go
```

If any remaining tests assert the old behavior, update them.

---

### Task 5: Update finding document status

- [ ] **Step 1: Mark finding as resolved**

In `docs/findings/plansnapshot-midcycle-clear.md`, change:
```yaml
status: investigated
```
to:
```yaml
status: resolved
```

- [ ] **Step 2: Add implementation details section**

Append to the finding document:

```markdown
## Implementation Details

Fixed in commit [TBD]:
- `hibernatingState.finalize()`: Removed `AppliedExceptionOverride = ""` and `PlanSnapshot = nil`
- `wakingUpState.finalize()`: Removed `AppliedExceptionOverride = ""` and `PlanSnapshot = nil`
- `idleState.transitionToWakingUp()`: Stopped rebuilding from live exceptions; now reuses existing snapshot when `CycleID` matches `CurrentCycleID`
- Tests updated to assert snapshot preservation across the full cycle
```

---

## Self-Review Checklist

### Spec Coverage
- [x] Preserve snapshot in hibernating finalize → Task 1
- [x] Preserve snapshot in wakingUp finalize → Task 2
- [x] Reuse snapshot in transitionToWakingUp → Task 3
- [x] Backward compatibility (no snapshot = fallback) → Task 3 Step 2
- [x] Tests updated and new tests added → Tasks 1-3

### Placeholder Scan
- [x] No TBD/TODO/fill-in
- [x] Exact file paths provided
- [x] Complete code blocks in every step
- [x] Exact test commands with expected output

### Type Consistency
- [x] `PlanSnapshot` type matches `hibernatorv1alpha1.PlanSnapshot`
- [x] `AppliedExceptionOverride` is `string`
- [x] `CurrentCycleID` is `string`
- [x] `effectivePlan()` guard `snap.CycleID == plan.Status.CurrentCycleID` used consistently
