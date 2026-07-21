---
date: 2026-07-21
status: draft
component: RDS Executor
beads: hib-qxu
---

# Design: RDS Executor — Graceful Skip on InvalidDBInstanceState / InvalidDBClusterState

## Problem

The RDS executor treats `InvalidDBInstanceState` as a fatal error that immediately aborts the entire target. This causes the executor to "exit too early" when encountering instances that AWS restricts from stopping:

- **Read Replica source**: An instance with read replicas cannot be stopped (AWS returns `InvalidDBInstanceState`)
- **SQL Server Multi-AZ**: SQL Server instances with Multi-AZ enabled cannot be stopped (AWS returns `InvalidDBInstanceState`)

Similarly, `InvalidDBClusterState` is not handled gracefully for Aurora clusters.

The same issue exists on the **WakeUp** path with `StartDBInstance` / `StartDBCluster`.

### Current Behavior

In `instanceStrategy.Stop()`:
```go
if _, err = client.StopDBInstance(ctx, &rds.StopDBInstanceInput{...}); err != nil {
    var apiErr smithy.APIError
    if errors.As(err, &apiErr) && apiErr.ErrorCode() == "DBInstanceNotFound" {
        log.Info("instance not found, skipping ...", ...)
        return DBInstanceState{Outcome: operationOutcomeSkippedStale}, nil
    }
    return nil, err  // <-- InvalidDBInstanceState falls here, aborting the target
}
```

When `processResources()` encounters this error, it immediately returns, skipping all remaining instances/clusters in that target.

## Decision

Keep the **fail-fast** behavior for unexpected errors. Only change the error classification for known AWS restrictions.

Add `InvalidDBInstanceState` alongside `DBInstanceNotFound` as a graceful skip. Same for clusters: add `InvalidDBClusterState` alongside `DBClusterNotFoundFault`.

### Rationale

- **Fail-fast is intentional**: For truly unexpected errors, we want to stop and let the user investigate rather than blindly continuing.
- **Known restrictions should not be fatal**: If AWS tells us an instance can't be stopped for a structural reason (read replica source, engine limitation), that's not an operational failure — it's a configuration characteristic. Treating it as a skip is consistent with how we handle "already stopped" or "not found".
- **Future unknown errors**: Any new AWS error code will still fail fast, preserving the existing safety net.

## Scope

### In Scope

1. `instanceStrategy.Stop()` — add `InvalidDBInstanceState` graceful skip after `StopDBInstance`
2. `instanceStrategy.Start()` — add `InvalidDBInstanceState` graceful skip after `StartDBInstance`
3. `clusterStrategy.Stop()` — add `InvalidDBClusterState` graceful skip after `StopDBCluster`
4. `clusterStrategy.Start()` — add `InvalidDBClusterState` graceful skip after `StartDBCluster`
5. Update log messages to reflect the new skip reason
6. Add unit tests for all four paths

### Out of Scope

- Changing the fail-fast behavior for unexpected errors
- Collect-all-errors behavior (deferred to future if needed)
- Adding per-instance configuration to override AWS restrictions

## Implementation

### Error Code Mapping

| Error Code | Affected API | Current Handling | New Handling |
|------------|--------------|------------------|--------------|
| `DBInstanceNotFound` | `StopDBInstance` / `DescribeDBInstances` / `StartDBInstance` | Skip stale | Unchanged |
| `InvalidDBInstanceState` | `StopDBInstance` / `StartDBInstance` | Fatal error | **Skip stale** |
| `DBClusterNotFoundFault` | `StopDBCluster` / `DescribeDBClusters` / `StartDBCluster` | Skip stale | Unchanged |
| `InvalidDBClusterState` | `StopDBCluster` / `StartDBCluster` | Fatal error | **Skip stale** |

### Log Messages

```go
log.Info("instance is in an invalid state for this operation, skipping ...",
    "instanceId", id, "errorCode", apiErr.ErrorCode())
```

```go
log.Info("cluster is in an invalid state for this operation, skipping ...",
    "clusterId", id, "errorCode", apiErr.ErrorCode())
```

### Tests

Add tests in `rds_test.go` covering:
- `TestShutdown_StopInstance_InvalidDBInstanceState` — mock `StopDBInstance` returning `InvalidDBInstanceState`, assert `SkippedStale` outcome and continuation
- `TestShutdown_StopCluster_InvalidDBClusterState` — mock `StopDBCluster` returning `InvalidDBClusterState`
- `TestWakeUp_StartInstance_InvalidDBInstanceState` — mock `StartDBInstance` returning `InvalidDBInstanceState`
- `TestWakeUp_StartCluster_InvalidDBClusterState` — mock `StartDBCluster` returning `InvalidDBClusterState`

## Acceptance Criteria

- [ ] When `StopDBInstance` returns `InvalidDBInstanceState`, the instance is skipped gracefully and remaining instances continue processing
- [ ] When `StartDBInstance` returns `InvalidDBInstanceState`, the instance is skipped gracefully
- [ ] When `StopDBCluster` returns `InvalidDBClusterState`, the cluster is skipped gracefully
- [ ] When `StartDBCluster` returns `InvalidDBClusterState`, the cluster is skipped gracefully
- [ ] Existing `DBInstanceNotFound` / `DBClusterNotFoundFault` behavior is unchanged
- [ ] All unexpected errors still fail fast (abort the target)
- [ ] Unit tests cover all four new skip paths
