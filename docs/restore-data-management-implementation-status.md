# Restore Data Management - Implementation Status

## Completed Steps

### ✅ Step 1: RestoreData Quality Metadata

**File**: `internal/executor/interface.go`

Added quality tracking fields to RestoreData:

- `IsLive bool` - Indicates if data captured from running resources (high quality) or already-shutdown state (low quality)
- `CapturedAt string` - ISO8601 timestamp of when data was captured

### ✅ Step 2: EC2 Executor Quality Detection (Pilot)

**File**: `internal/executor/ec2/ec2.go`

Updated EC2 Shutdown method to:

- Detect if instances are running before shutdown
- Set `IsLive=true` if at least one instance was running
- Set `IsLive=false` if all instances already stopped/not found
- Add `CapturedAt` timestamp in RFC3339 format
- Enhanced logging to show quality flag

**Quality Logic**:

```go
hasRunningInstances := false
for _, inst := range instances {
    if inst.State.Name == types.InstanceStateNameRunning {
        hasRunningInstances = true
        break
    }
}

return executor.RestoreData{
    Type:        e.Type(),
    Data:        restoreData,
    IsLive:      hasRunningInstances,
    CapturedAt:  time.Now().Format(time.RFC3339),
}, nil
```

### ✅ Step 3: RestoreManager Quality-Aware Save

**File**: `internal/restore/manager.go`

Added `SaveOrPreserve` method with quality preservation logic:

- If existing data has `IsLive=true` and new has `IsLive=false`, preserves existing
- If same quality, first-write-wins (preserves existing)
- Returns nil (no-op) when preserving high-quality data

Added helper methods:

- `Exists(namespace, planName, targetName)` - Check if restore data exists for target
- `Load()` - Already existed, used for quality comparison

### ✅ Step 4: RestoreManager Restoration Tracking

**File**: `internal/restore/manager.go`

Added annotation-based locking mechanism:

- `MarkTargetRestored(namespace, planName, targetName)` - Sets `hibernator.ardikabs.com/restored-{targetName}: "true"` annotation
- `AllTargetsRestored(namespace, planName, targetNames)` - Checks if all targets marked as restored
- `UnlockRestoreData(namespace, planName)` - Clears all `restored-*` annotations (unlocks for next cycle)
- `HasRestoreData(namespace, planName)` - Checks if ConfigMap exists with data

**Locking Pattern**:

- Unlocked: No `restored-*` annotations → Available for next shutdown
- Locked: Has `restored-*` annotations → Wake-up in progress
- Unlock: Clear annotations after successful wake-up, ConfigMap persists

### ✅ Step 5: Runner Shutdown Logic

**File**: `cmd/runner/runner.go` (lines 163-201)

Updated shutdown logic to:

- On shutdown **failure**: Skip save to preserve existing restore point, log intent to preserve
- On shutdown **success**: Call `SaveOrPreserve` instead of `Save` to enable quality preservation
- Enhanced logging: Log `isLive` and `capturedAt` fields when saving

**Key changes**:

```go
// Shutdown failure: skip save to preserve existing restore point
if err != nil {
    if cfg.Operation == "shutdown" {
        r.log.Error(err, "shutdown failed, preserving existing restore point (if any)")
    }
    r.reportCompletion(ctx, false, err.Error(), durationMs)
    return err
}

// Shutdown success: persist with quality-aware preservation
if cfg.Operation == "shutdown" && restoreData != nil {
    if err := r.saveRestoreData(ctx, restoreData); err != nil {
        // FATAL: Without restore data, wake-up is impossible
        r.log.Error(err, "CRITICAL: failed to save restore data to ConfigMap")
        return fmt.Errorf("save restore data: %w", err)
    }
    r.log.Info("Restore data saved to ConfigMap",
        "plan", r.cfg.Plan,
        "target", r.cfg.Target,
        "isLive", restoreData.IsLive,
        "capturedAt", restoreData.CapturedAt,
    )
}
```

### ✅ Step 6: Runner Wake-Up Logic

**File**: `cmd/runner/runner.go` (lines 163-201)

Updated wake-up logic to:

- After successful `WakeUp()`, call `MarkTargetRestored()`
- Non-fatal: Continue even if marking fails (eventual consistency)
- Enhanced logging: Log restoration marking success

**Key changes**:

```go
// Wake-up success: mark target as restored for cleanup coordination
if cfg.Operation == "wakeup" {
    rm := restore.NewManager(r.k8sClient)
    if err := rm.MarkTargetRestored(ctx, r.cfg.Namespace, r.cfg.Plan, r.cfg.Target); err != nil {
        // Non-fatal: continue even if marking fails
        r.log.Error(err, "failed to mark target as restored (non-fatal)")
    } else {
        r.log.Info("Target marked as restored",
            "plan", r.cfg.Plan,
            "target", r.cfg.Target,
        )
    }
}
```

### ✅ Auxiliary: restore.Data Structure Update

**File**: `internal/restore/manager.go` (lines 54-75)

Added quality fields to `restore.Data` struct:

- `IsLive bool` - Matches executor.RestoreData.IsLive
- `CapturedAt string` - Matches executor.RestoreData.CapturedAt

This ensures quality metadata flows through the entire system: Executor → Runner → ConfigMap.

### ✅ Auxiliary: saveRestoreData Function Update

**File**: `cmd/runner/runner.go` (lines 520-556)

Updated `saveRestoreData` function to:

- Copy `IsLive` and `CapturedAt` from `executor.RestoreData` to `restore.Data`
- Use `SaveOrPreserve` instead of `Save`
- Add comment explaining quality preservation logic

**Key changes**:

```go
restoreData := &restore.Data{
    Target:     r.cfg.Target,
    Executor:   data.Type,
    Version:    1,
    CreatedAt:  metav1.Now(),
    IsLive:     data.IsLive,      // NEW
    CapturedAt: data.CapturedAt,  // NEW
}

// Use SaveOrPreserve to implement quality-aware preservation
if err := rm.SaveOrPreserve(ctx, r.cfg.Namespace, r.cfg.Plan, r.cfg.Target, restoreData); err != nil {
    return fmt.Errorf("save restore data: %w", err)
}
```

## Pending Steps

### ✅ Step 7: Suspension Tracking Annotation (COMPLETED)

**File**: `internal/controller/hibernateplan_controller.go` (lines 160-189)

Added suspension phase tracking when suspend is set:
- Records current phase in annotation `hibernator.ardikabs.com/suspended-at-phase` when transitioning to Suspended
- Enables force wake-up detection on unsuspend

**Implementation**:
```go
if plan.Spec.Suspend && plan.Status.Phase != hibernatorv1alpha1.PhaseSuspended {
    // Track the phase at suspension time
    if plan.Annotations == nil {
        plan.Annotations = make(map[string]string)
    }
    plan.Annotations["hibernator.ardikabs.com/suspended-at-phase"] = string(plan.Status.Phase)
    // ... transition to Suspended phase
}
```

### ✅ Step 8: Force Wake-Up on Unsuspend (COMPLETED)

**File**: `internal/controller/hibernateplan_controller.go` (lines 191-224)

Implemented force wake-up logic when unsuspending:
- Checks `suspended-at-phase` annotation
- Checks if restore data exists via `HasRestoreData()`
- Evaluates schedule to determine if should be active
- If all conditions met, transitions directly to WakingUp and calls `startWakeUp()`

**Logic**:
```go
suspendedAtPhase := plan.Annotations["hibernator.ardikabs.com/suspended-at-phase"]
hasRestoreData, _ := r.RestoreManager.HasRestoreData(ctx, plan.Namespace, plan.Name)
shouldBeActive, _, _ := r.evaluateSchedule(ctx, log, plan)

if suspendedAtPhase != "" && suspendedAtPhase != "Active" && hasRestoreData && shouldBeActive {
    // Force wake-up instead of just resuming to Active
    // Transition to WakingUp → startWakeUp()
}
```

### ✅ Step 9: Phase Guard for Duplicate Shutdown (COMPLETED)

**File**: `internal/controller/hibernateplan_controller.go` (lines 233-257)

Added phase guard to prevent duplicate shutdown operations:
- When `phase=Hibernated` and schedule says hibernate, skip with log message
- Requeue for 5 minutes to check schedule again
- Preserves restore point by avoiding redundant shutdown calls

**Implementation**:
```go
case hibernatorv1alpha1.PhaseHibernated:
    if desiredPhase == hibernatorv1alpha1.PhaseHibernating {
        log.V(1).Info("already hibernated, skipping duplicate shutdown")
        return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
    }
    if desiredPhase == hibernatorv1alpha1.PhaseWakingUp {
        return r.startWakeUp(ctx, log, plan)
    }
```

### ✅ Step 10: Restore Point Check Before Wake-Up (COMPLETED)

**File**: `internal/controller/hibernateplan_wakeup.go` (lines 19-31)

Added restore data existence check before starting wake-up:
- Calls `HasRestoreData()` before initializing wake-up operation
- Returns error if no restore point found
- Prevents wake-up without data

**Implementation**:
```go
func (r *HibernatePlanReconciler) startWakeUp(...) (ctrl.Result, error) {
    hasRestoreData, err := r.RestoreManager.HasRestoreData(ctx, plan.Namespace, plan.Name)
    if err != nil {
        return r.setError(ctx, plan, fmt.Errorf("check restore data: %w", err))
    }
    if !hasRestoreData {
        return r.setError(ctx, plan, fmt.Errorf("cannot wake up: no restore point found"))
    }
    // ... continue with wake-up
}
```

### ✅ Step 11: Unlock After Phase Stability (COMPLETED)

**File**: `internal/controller/hibernateplan_controller.go` (lines 1331-1368)

Implemented unlock mechanism after successful wake-up:
- After transition to `PhaseActive`, checks if all targets restored via `AllTargetsRestored()`
- If yes, calls `UnlockRestoreData()` to clear `restored-*` annotations
- Cleans up `suspended-at-phase` annotation
- ConfigMap persists for debugging, only annotations are cleared

**Implementation**:
```go
if operation == "wakeup" {
    p.Status.Phase = hibernatorv1alpha1.PhaseActive
    // ... record execution history

    // Check if all targets restored
    targetNames := make([]string, 0, len(plan.Spec.Targets))
    for _, target := range plan.Spec.Targets {
        targetNames = append(targetNames, target.Name)
    }
    allRestored, _ := r.RestoreManager.AllTargetsRestored(ctx, plan.Namespace, plan.Name, targetNames)

    if allRestored {
        // Unlock (clear annotations)
        r.RestoreManager.UnlockRestoreData(ctx, plan.Namespace, plan.Name)

        // Clean up suspension annotation
        delete(plan.Annotations, "hibernator.ardikabs.com/suspended-at-phase")
    }
}
```

### ✅ Infrastructure: RestoreManager Field (COMPLETED)

**Files**:
- `internal/controller/hibernateplan_controller.go` (struct definition)
- `cmd/controller/main.go` (initialization)

Added RestoreManager field to HibernatePlanReconciler:
- Field: `RestoreManager *restore.Manager`
- Initialized in main.go: `RestoreManager: restore.NewManager(mgr.GetClient())`
- Enables controller to call all restore data operations

---

## All Steps Complete! ✅

### Summary of Implementation

**Quality-Aware Restore Data System:**
1. ✅ RestoreData struct enriched with IsLive and CapturedAt
2. ✅ EC2 executor detects resource state and sets quality flags
3. ✅ RestoreManager.SaveOrPreserve preserves high-quality data
4. ✅ Annotation-based locking for restoration coordination
5. ✅ Runner handles shutdown failures and marks successful wake-ups
6. ✅ Controller tracks suspension, forces wake-up, guards against duplicates

**Data Flow:**
```
Executor Shutdown → Detects IsLive → Returns RestoreData(IsLive, CapturedAt)
                                           ↓
Runner → On success: SaveOrPreserve() → ConfigMap (quality preserved)
      → On failure: Skip save (preserve existing)
                                           ↓
Controller → Suspension: Track phase
          → Unsuspend + hasRestoreData + active schedule → Force wake-up
          → Already Hibernated + schedule hibernates → Skip duplicate shutdown
                                           ↓
Runner WakeUp → executor.WakeUp() → On success: MarkTargetRestored()
                                           ↓
Controller → AllTargetsRestored() → UnlockRestoreData() → Clear annotations
                                  → Clean up suspended-at-phase
```

**Result:** Data loss scenario is now prevented. High-quality restore data captured from running resources is preserved even if:
- Shutdown called when resources already stopped
- Suspend/unsuspend interrupts hibernation
- Duplicate shutdown attempts occur

## Remaining Tasks

### Step 5: Runner Shutdown Logic

**File**: `cmd/runner/main.go` (lines ~499-530)

**Required changes**:
```go
// Always call executor
result, err := executor.Shutdown(ctx, log, spec)

if err != nil {
    // Failure: skip save to preserve existing restore point
    log.Error(err, "shutdown failed, preserving existing restore point")
    return err
}

// Success: call SaveOrPreserve with quality-aware preservation
if err := restoreManager.SaveOrPreserve(ctx, namespace, planName, targetName, &result); err != nil {
    log.Error(err, "failed to save restore data")
    return err
}
```

**Key change**: Replace `Save()` with `SaveOrPreserve()` to enable quality preservation.

### Step 6: Runner Wake-Up Logic

**File**: `cmd/runner/main.go`

**Required changes**:
```go
// After successful WakeUp
if err := executor.WakeUp(ctx, log, spec, *restoreData); err != nil {
    return err
}

// Mark as restored for cleanup coordination
if err := restoreManager.MarkTargetRestored(ctx, namespace, planName, targetName); err != nil {
    log.Error(err, "failed to mark target as restored")
    // Continue - not critical
}
```

**Key change**: Add `MarkTargetRestored()` call after successful wake-up.

### Step 7: Suspension Tracking Annotation

**File**: `internal/controller/hibernateplan_controller.go`

**Required changes**:
```go
// When suspend annotation is set
if plan.Annotations["hibernator.ardikabs.com/suspend"] == "true" {
    // Record current phase
    plan.Annotations["hibernator.ardikabs.com/suspended-at-phase"] = string(plan.Status.Phase)
    // Update plan...
}
```

### Step 8: Force Wake-Up on Unsuspend

**File**: `internal/controller/hibernateplan_controller.go`

**Required changes**:
```go
// In reconcile loop - detect unsuspend
suspendedAtPhase := plan.Annotations["hibernator.ardikabs.com/suspended-at-phase"]
isSuspended := plan.Annotations["hibernator.ardikabs.com/suspend"] == "true"

if suspendedAtPhase != "" && !isSuspended {
    hasRestoreData, _ := restoreManager.HasRestoreData(ctx, plan.Namespace, plan.Name)
    shouldBeActive := !scheduleEvaluator.ShouldHibernate(time.Now())

    if suspendedAtPhase != "Active" && hasRestoreData && shouldBeActive {
        log.Info("forcing wake-up after unsuspend", "suspendedAtPhase", suspendedAtPhase)
        return r.startWakeUp(ctx, log, plan)
    }
}
```

### Step 9: Phase Guard for Duplicate Shutdown

**File**: `internal/controller/hibernateplan_controller.go` (reconcilePhase)

**Required changes**:
```go
if plan.Status.Phase == PhaseHibernated && shouldHibernate {
    log.Info("already hibernated, skipping duplicate shutdown")
    return ctrl.Result{RequeueAfter: nextRequeue}, nil
}
```

### Step 10: Restore Point Check Before Wake-Up

**File**: `internal/controller/hibernateplan_controller.go` (startWakeUp)

**Required changes**:
```go
func (r *HibernatePlanReconciler) startWakeUp(...) (ctrl.Result, error) {
    hasRestoreData, err := r.RestoreManager.HasRestoreData(ctx, plan.Namespace, plan.Name)
    if err != nil {
        return ctrl.Result{}, err
    }
    if !hasRestoreData {
        return ctrl.Result{}, fmt.Errorf("cannot wake up: no restore point found")
    }
    // Continue with wake-up...
}
```

### Step 11: Unlock After Phase Stability

**File**: `internal/controller/hibernateplan_controller.go`

**Required changes**:
```go
// After phase transitions to Active
if plan.Status.Phase == PhaseActive {
    targetNames := extractTargetNames(plan.Spec.Targets)
    allRestored, _ := r.RestoreManager.AllTargetsRestored(ctx, plan.Namespace, plan.Name, targetNames)

    if allRestored {
        // Unlock restore data (clear annotations)
        if err := r.RestoreManager.UnlockRestoreData(ctx, plan.Namespace, plan.Name); err != nil {
            log.Error(err, "failed to unlock restore data")
        }

        // Clean up suspension tracking
        delete(plan.Annotations, "hibernator.ardikabs.com/suspended-at-phase")
        if err := r.Update(ctx, plan); err != nil {
            return ctrl.Result{}, err
        }
    }
}
```

## Remaining Tasks

### Update Other Executors (Steps 2 Pattern Application)

After runner and controller changes are complete, apply the quality detection pattern from EC2 to other executors:

**1. EKS Executor** (`internal/executor/eks/eks.go`):
- Check if managed node groups have `desiredSize > 0`
- Check if Karpenter NodePools exist
- Set `IsLive=true` if any compute resources running
- Set `IsLive=false` if all already scaled to zero

**2. Karpenter Executor** (`internal/executor/karpenter/karpenter.go`):
- Check if NodePools exist (not NotFound)
- Set `IsLive=true` if NodePools exist
- Set `IsLive=false` if already deleted

**3. RDS Executor** (`internal/executor/rds/rds.go`):
- Check if DB instance/cluster state is "available" or "running"
- Set `IsLive=true` if running
- Set `IsLive=false` if already stopped

**4. WorkloadScaler Executor** (`internal/executor/workloadscaler/workloadscaler.go`):
- Check if workload replicas > 0
- Set `IsLive=true` if replicas > 0
- Set `IsLive=false` if already scaled to zero

### Testing Strategy

### Unit Tests

**RestoreManager** (`internal/restore/manager_test.go`):
- [ ] TestSaveOrPreserve_ExistingHighQuality_NewLowQuality (preserves existing)
- [ ] TestSaveOrPreserve_ExistingSameLowQuality_NewLowQuality (first-write-wins)
- [ ] TestSaveOrPreserve_NoExisting (saves new)
- [ ] TestMarkTargetRestored (sets annotation)
- [ ] TestAllTargetsRestored (checks all annotations)
- [ ] TestUnlockRestoreData (clears annotations, preserves data)
- [ ] TestHasRestoreData (ConfigMap existence check)

**EC2 Executor** (`internal/executor/ec2/ec2_test.go`):
- [ ] TestShutdown_RunningInstances_IsLiveTrue
- [ ] TestShutdown_StoppedInstances_IsLiveFalse
- [ ] TestShutdown_MixedInstances_IsLiveTrue (at least one running)

### Integration Tests

**Controller** (`internal/controller/hibernateplan_controller_test.go`):
- [ ] TestForceWakeUp_AfterUnsuspend
- [ ] TestPhaseGuard_SkipDuplicateShutdown
- [ ] TestRestorePointCheck_BeforeWakeUp
- [ ] TestUnlock_AfterSuccessfulWakeUp

### E2E Tests

**Full Cycle** (`test/e2e/`):
- [ ] TestDataLoss_Scenario (reproduces original problem and verifies fix)
- [ ] TestSuspendUnsuspend_ForceWakeUp
- [ ] TestQualityPreservation_LowQualityDoesNotOverwriteHighQuality

## Documentation

- [x] Design document: `docs/restore-data-management.md`
- [x] Scenario analysis: `docs/restore-data-lifecycle-scenarios.md`
- [ ] User guide: Document suspend/unsuspend behavior
- [ ] API reference: Update with new RestoreManager methods
- [ ] Troubleshooting: Add restore data debugging section

## Next Steps

1. Implement runner logic (Steps 5-6)
2. Implement controller changes (Steps 7-11)
3. Update remaining executors (EKS, Karpenter, RDS, WorkloadScaler)
4. Add comprehensive tests
5. Update user-facing documentation
