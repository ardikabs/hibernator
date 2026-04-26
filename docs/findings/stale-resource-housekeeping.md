---
date: April 26, 2026
status: resolved
component: Runner
---

# Findings: Stale Resource Housekeeping in RestorePoint

## Problem Description

Stale resources (such as deleted RDS instances or removed EKS nodegroups) that are captured in a prior hibernation cycle are skipped during subsequent cycles (yielding `operationOutcomeSkippedStale`) but remain indefinitely in the RestorePoint `ConfigMap`. This leads to a continuously growing `ConfigMap` size, which eventually risks hitting the Kubernetes `ConfigMap` limit (1MB). It also unnecessarily consumes processing time during `WakeUp` operations, where Hibernator tries to restore resources that no longer exist.

## Root Cause Analysis

The root cause of this behavior resides in `Manager.SaveOrPreserve` in `internal/restore/manager.go` and how `runner.go` calls it.

1. **State Merge Logic**: `Manager.SaveOrPreserve` intentionally preserves existing state if new state is absent. This ensures that resources skipped intentionally (e.g., due to temporary API issues or cache misses) are not lost.
2. **Lack of Lifecycle Awareness**: Resources that are persistently absent or unmanageable across multiple hibernation cycles (`shutdown`) are never actively removed from the `ConfigMap`. Since they aren't provided in the new state, `SaveOrPreserve` silently preserves them over and over.

## Resolution

The resolution introduces a staleness tracking mechanism directly in the `Manager` to safely evict persistently missing resources over a configurable number of cycles.

### Implementation Details

1. **StaleCounts Field**: Added a `StaleCounts map[string]int` to `restore.Data` struct in `internal/restore/manager.go` to track how many consecutive hibernation cycles a resource has not been reported.
2. **HousekeepStaleResources**: Introduced `HousekeepStaleResources` method in `restore.Manager` which performs the eviction. It increments the `StaleCount` for any key present in `existing.State` but missing from `reportedKeys`. If a resource is reported, its `StaleCount` is reset. If `StaleCount` exceeds a threshold (3 cycles), the key is removed from both `State` and `StaleCounts`.
3. **Runner Integration**: Updated `cmd/runner/app/runner.go` `accumulator.flush()` to call `HousekeepStaleResources` at the end of the flush process, passing all collected `liveKeys` and `nonLiveKeys` as `reportedKeys`.

```go
// internal/restore/manager.go
func (m *Manager) HousekeepStaleResources(ctx context.Context, namespace, planName, targetName string, reportedKeys []string, maxStaleCount int) error {
	// ... logic to increment and evict stale resources ...
}
```

## Impact of Fix

- **Resource Efficiency**: The `ConfigMap` size is bounded, preventing uncontrolled growth.
- **Performance**: `WakeUp` phase will not attempt to start resources that were deleted long ago.
- **Safety**: By using a 3-cycle threshold, we maintain safety against temporary API failures or transient tagging issues while still actively managing state.
