# Restore Data Management - Design and Implementation

## Overview

This document describes the restore data management system for the Hibernator Operator, which ensures reliable state preservation during hibernation cycles while handling edge cases like failures, suspensions, and manual interventions.

## Problem Statement

### The Data Loss Scenario

Without proper restore data management, the following scenario causes data loss:

```
1. Plan successfully shuts down → RestoreData saved
2. Wake-up fails (credential error) → Resources still hibernated
3. User suspends plan → Phase moves to Active without wake-up
4. User fixes credential and unsuspends
5. Resources remain hibernated but Plan thinks Active
6. Next shutdown finds NotFound → Captures empty/bad RestoreData
7. OVERWRITES good restore data with bad data
8. Wake-up fails with corrupted restore data
```

## Design Principles

### 1. Quality-Aware Restore Data

**RestoreData Structure:**
```go
type RestoreData struct {
    Type       string          `json:"type"`
    Data       json.RawMessage `json:"data"`
    IsLive     bool            `json:"isLive"`     // Captured from running resources?
    CapturedAt string          `json:"capturedAt"` // ISO8601 timestamp
}
```

**Quality Semantics:**
- `IsLive=true`: Data captured from running resources (high quality)
- `IsLive=false`: Data captured from already-shutdown state (low quality)

### 2. ConfigMap as Persistent Storage

**Storage Pattern:**
- ConfigMap: `hibernator-restore-{plan-name}`
- Keys: `{executor}_{target-name}.json`
- Annotations: Restoration tracking and locking
- **Persists across cycles** for debugging/troubleshooting

### 3. Annotation-Based Locking

**Lock Mechanism:**
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: hibernator-restore-my-plan
  annotations:
    hibernator.ardikabs.com/restored-db-1: "true"      # Target restored
    hibernator.ardikabs.com/restored-cluster-1: "true" # Target restored
data:
  rds_db-1.json: '{"type":"rds","data":"...","isLive":true}'
  eks_cluster-1.json: '{"type":"eks","data":"...","isLive":true}'
```

**Lock States:**
- **Unlocked**: No `restored-*` annotations → Available for next shutdown cycle
- **Locked**: Has `restored-*` annotations → Wake-up in progress
- **Unlock**: Clear all `restored-*` annotations after successful wake-up

## Core Components

### RestoreManager API

**New Methods:**

```go
// SaveOrPreserve saves restore data with quality-aware preservation logic.
// If existing data has IsLive=true and new has IsLive=false, preserves existing.
// If same quality, first-write-wins (preserves existing).
func (m *Manager) SaveOrPreserve(ctx context.Context, namespace, planName, targetName string, data *Data) error

// MarkTargetRestored marks a target as successfully restored.
// Sets annotation: hibernator.ardikabs.com/restored-{targetName}: "true"
func (m *Manager) MarkTargetRestored(ctx context.Context, namespace, planName, targetName string) error

// AllTargetsRestored checks if all targets have been restored.
func (m *Manager) AllTargetsRestored(ctx context.Context, namespace, planName string, targetNames []string) (bool, error)

// UnlockRestoreData clears all restored-* annotations without deleting ConfigMap data.
func (m *Manager) UnlockRestoreData(ctx context.Context, namespace, planName string) error

// HasRestoreData checks if restore ConfigMap exists.
func (m *Manager) HasRestoreData(ctx context.Context, namespace, planName string) (bool, error)

// Exists checks if restore data exists for specific target.
func (m *Manager) Exists(ctx context.Context, namespace, planName, targetName string) (bool, error)
```

### Executor Contract

**Shutdown Requirements:**

Executors must set `IsLive` flag based on resource state:

```go
func (e *Executor) Shutdown(ctx context.Context, log logr.Logger, spec executor.Spec) (executor.RestoreData, error) {
    // Check if resources at desired state
    instance, err := ec2Client.DescribeInstances(...)
    if err != nil {
        if isNotFoundError(err) {
            // Already shut down - return low-quality data
            return executor.RestoreData{
                Type: "ec2",
                Data: json.Marshal(emptyState),
                IsLive: false, // Not from running state
                CapturedAt: time.Now().Format(time.RFC3339),
            }, nil
        }
        return executor.RestoreData{}, err
    }

    if instance.State == "stopped" {
        // Already stopped - return low-quality data
        return executor.RestoreData{
            Type: "ec2",
            Data: json.Marshal(constructFromStoppedState(instance)),
            IsLive: false,
            CapturedAt: time.Now().Format(time.RFC3339),
        }, nil
    }

    // Running state - capture and shutdown
    restoreState := captureRunningState(instance)
    stopInstances(...)

    return executor.RestoreData{
        Type: "ec2",
        Data: json.Marshal(restoreState),
        IsLive: true, // Captured from running state!
        CapturedAt: time.Now().Format(time.RFC3339),
    }, nil
}
```

**Key Points:**
- Executors remain pure (shutdown/wakeup only)
- No restore point logic in executors
- Set `IsLive` based on actual resource state
- Handle NotFound/Stopped gracefully

### Runner Logic

**Shutdown Flow:**

```go
// Always call executor (validates actual state)
result, err := executor.Shutdown(ctx, log, spec)

if err != nil {
    // Failure: skip save to preserve existing restore point
    log.Error(err, "shutdown failed, preserving existing restore point")
    return err
}

// Success: save with quality-aware preservation
if err := restoreManager.SaveOrPreserve(ctx, namespace, planName, targetName, &result); err != nil {
    log.Error(err, "failed to save restore data")
    return err
}
```

**Wake-Up Flow:**

```go
// Load restore data
restoreData, err := restoreManager.Load(ctx, namespace, planName, targetName)
if err != nil {
    return err
}

// Call executor wake-up
if err := executor.WakeUp(ctx, log, spec, *restoreData); err != nil {
    return err
}

// Mark as restored (for cleanup coordination)
if err := restoreManager.MarkTargetRestored(ctx, namespace, planName, targetName); err != nil {
    log.Error(err, "failed to mark target as restored")
    // Continue - not critical
}
```

### Controller Logic

**Suspension Tracking:**

```go
// When suspend annotation is set
if plan.Annotations["hibernator.ardikabs.com/suspend"] == "true" {
    // Record current phase for unsuspend handling
    plan.Annotations["hibernator.ardikabs.com/suspended-at-phase"] = string(plan.Status.Phase)
    // Update plan...
}
```

**Force Wake-Up on Unsuspend:**

```go
// In reconcile loop - detect unsuspend
suspendedAtPhase := plan.Annotations["hibernator.ardikabs.com/suspended-at-phase"]
isSuspended := plan.Annotations["hibernator.ardikabs.com/suspend"] == "true"

if suspendedAtPhase != "" && !isSuspended {
    // Was suspended, now unsuspended
    hasRestoreData, _ := restoreManager.HasRestoreData(ctx, plan.Namespace, plan.Name)
    shouldBeActive := !scheduleEvaluator.ShouldHibernate(time.Now())

    if suspendedAtPhase != "Active" && hasRestoreData && shouldBeActive {
        log.Info("forcing wake-up after unsuspend",
            "suspendedAtPhase", suspendedAtPhase)
        return r.startWakeUp(ctx, log, plan)
    }
}
```

**Phase Guard:**

```go
// In reconcilePhase
if plan.Status.Phase == PhaseHibernated && shouldHibernate {
    // Already hibernated - skip duplicate shutdown
    log.Info("already hibernated, skipping duplicate shutdown")
    return ctrl.Result{RequeueAfter: nextRequeue}, nil
}
```

**Restore Point Check:**

```go
// In startWakeUp
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

**Unlock After Stability:**

```go
// After phase transitions to Active
if plan.Status.Phase == PhaseActive {
    allRestored, _ := r.RestoreManager.AllTargetsRestored(ctx, plan.Namespace, plan.Name, targetNames)

    if allRestored {
        // Unlock restore data (clear annotations)
        if err := r.RestoreManager.UnlockRestoreData(ctx, plan.Namespace, plan.Name); err != nil {
            log.Error(err, "failed to unlock restore data")
            // Continue - not critical
        }

        // Clean up suspension tracking
        delete(plan.Annotations, "hibernator.ardikabs.com/suspended-at-phase")
        if err := r.Update(ctx, plan); err != nil {
            return ctrl.Result{}, err
        }
    }
}
```

## Edge Cases Handled

### 1. Shutdown Failure → Retry

**Scenario:** First shutdown partially succeeds, then fails

**Handling:**
- Failed shutdown skips save → preserves existing data
- Retry loads existing high-quality data for completed targets
- Failed targets retry with fresh attempts

### 2. Wake-Up Failure → Retry

**Scenario:** Wake-up partially completes

**Handling:**
- Partial targets marked as `restored-*: "true"`
- Controller checks `AllTargetsRestored()` → false
- Retry continues, already-restored targets re-run (idempotent)
- After all complete → unlock

### 3. Suspend During Hibernation → Unsuspend During Active Hours

**Scenario:** The critical data loss scenario

**Handling:**
1. Suspend records: `suspended-at-phase: "Hibernated"`
2. Unsuspend detects: `suspended-at-phase != "Active"` + restore data exists + should be active
3. Force wake-up triggered
4. Resources restored to correct state
5. After successful wake-up → cleanup annotations

### 4. Manual Intervention During Hibernation

**Scenario:** Someone manually starts resources while Plan=Hibernated

**Handling:**
- Phase guard skips duplicate shutdown (efficient)
- Manual changes are temporary
- Wake-up restores to **original state** from restore point (eventual consistency)
- Restore point is the contract, not current cloud state

### 5. Second Shutdown Finds Resources Already Down

**Scenario:** Resources already at desired state (NotFound/Stopped)

**Handling:**
- Executor returns `RestoreData{IsLive: false}`
- `SaveOrPreserve()` compares quality:
  - Existing: `IsLive=true` (from running state)
  - New: `IsLive=false` (from already-shutdown)
  - Decision: **Preserve existing** (better quality)
- High-quality data protected

## Quality Preservation Logic

**SaveOrPreserve Decision Tree:**

```
New data arrives:
  ├─ Does restore point exist?
  │  ├─ No → Save new data
  │  └─ Yes → Compare quality:
  │     ├─ Existing IsLive=true, New IsLive=false → Preserve existing
  │     ├─ Existing IsLive=false, New IsLive=true → Save new (upgrade)
  │     └─ Same quality → Preserve existing (first-write-wins)
  └─ Return
```

## Testing Strategy

### Unit Tests

**RestoreManager:**
- `SaveOrPreserve` with various quality combinations
- `MarkTargetRestored` / `AllTargetsRestored` annotation logic
- `UnlockRestoreData` clears annotations but preserves data

**Executors:**
- Set `IsLive=true` when resources running
- Set `IsLive=false` when resources already shutdown
- Handle NotFound gracefully

**Runner:**
- Skip save on executor failure
- Call `SaveOrPreserve` on success
- Mark restored after wake-up

### Integration Tests

**Controller:**
- Force wake-up on unsuspend from non-Active phase
- Phase guard prevents duplicate shutdown
- Unlock after successful wake-up
- Suspension annotation cleanup

### E2E Tests

**Full Cycle:**
1. Shutdown → capture high-quality data
2. Wake-up fail → retry preserves data
3. Suspend → unsuspend → force wake-up
4. Manual intervention → wake-up restores original state

## Migration Notes

**Existing Deployments:**

- No breaking changes to existing RestoreData format
- `IsLive` and `CapturedAt` fields are additive
- Old restore data treated as `IsLive=true` (assumed high-quality)
- Controllers/runners upgrade automatically

**ConfigMap Cleanup:**

- Existing restore ConfigMaps remain intact
- No automatic cleanup (preserved for debugging)
- Manual cleanup: `kubectl delete cm hibernator-restore-{plan-name}`

## Monitoring and Debugging

**Key Metrics:**

- Restore data quality distribution (IsLive true/false ratio)
- Preserve vs save decisions count
- Force wake-up trigger count
- Unlock operation success rate

**Debug Commands:**

```bash
# View restore data for a plan
kubectl get cm hibernator-restore-my-plan -o yaml

# Check lock status (annotations)
kubectl get cm hibernator-restore-my-plan -o jsonpath='{.metadata.annotations}'

# View restore data quality
kubectl get cm hibernator-restore-my-plan -o json | jq '.data | to_entries[] | .key + ": isLive=" + (.value | fromjson | .isLive | tostring)'

# Check suspension tracking
kubectl get hibernateplan my-plan -o jsonpath='{.metadata.annotations.hibernator\.ardikabs\.com/suspended-at-phase}'
```

## References

- [Restore Data Lifecycle Scenarios](./restore-data-lifecycle-scenarios.md)
- [RFC-0001: Hibernator Operator](../enhancements/0001-hibernate-operator.md)
- [RestoreManager Implementation](../internal/restore/manager.go)
- [Executor Interface](../internal/executor/executor.go)
