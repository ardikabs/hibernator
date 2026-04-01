# HibernatePlan

The `HibernatePlan` is the primary Custom Resource Definition (CRD) in Hibernator. It declares the complete hibernation intent: what to hibernate, when, and how.

## Lifecycle Phases

A HibernatePlan transitions through these phases:

```
Pending → Active → Hibernating → Hibernated → WakingUp → Active → ...
                                                              ↕
                                                          Suspended
              ↘ Error (from any active phase)
```

| Phase | Description |
|-------|-------------|
| `Pending` | Plan is created but not yet evaluated |
| `Active` | Plan is active, waiting for the next schedule window |
| `Hibernating` | Shutdown operation is in progress |
| `Hibernated` | All targets successfully hibernated |
| `WakingUp` | Wakeup operation is in progress |
| `Suspended` | Plan is manually suspended via `spec.suspend: true` |
| `Error` | An error occurred; may auto-retry based on configuration |

## Schedule

Schedules use a human-friendly format with timezone awareness:

```yaml
schedule:
  timezone: "Asia/Jakarta"
  offHours:
    - start: "20:00"       # HH:MM format (24-hour)
      end: "06:00"         # Next eligible day when end < start
      daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
```

All executions — both hibernation and wakeup — are bounded to `daysOfWeek`. Wakeup only triggers on days listed in the schedule. For example, Friday 20:00 hibernation does not wake up Saturday 06:00; resources stay hibernated until Monday 06:00.

The controller applies a schedule buffer (default: 1 minute) and safety buffer (10 seconds) to every execution, so actual actions occur up to **1m 10s** after the nominal schedule time. See [Execution Timing](../user-guides/schedule-boundaries.md#execution-timing-schedule-buffer-and-safety-buffer) for details.

### Multiple Windows

You can define multiple off-hour windows that are evaluated with OR-logic:

```yaml
schedule:
  timezone: "US/Eastern"
  offHours:
    # Weeknight window
    - start: "20:00"
      end: "06:00"
      daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
    # Full weekend
    - start: "00:00"
      end: "23:59"
      daysOfWeek: ["SAT", "SUN"]
```

Hibernation triggers when **any** window condition is met. The next event time is computed as the earliest across all windows.

## Execution Strategies

The execution strategy determines the order in which targets are processed:

=== "Sequential"

    ```yaml
    execution:
      strategy:
        type: Sequential
    ```

    Targets execute one at a time, in declaration order.

=== "Parallel"

    ```yaml
    execution:
      strategy:
        type: Parallel
        maxConcurrency: 3
    ```

    All targets execute simultaneously, bounded by `maxConcurrency`.

=== "DAG"

    ```yaml
    execution:
      strategy:
        type: DAG
        maxConcurrency: 3
        dependencies:
          - from: karpenter-nodes
            to: eks-nodegroups
          - from: app-servers
            to: database
    ```

    Topological order based on explicit dependency edges. Cycles are detected and rejected at admission time.

=== "Staged"

    ```yaml
    execution:
      strategy:
        type: Staged
        stages:
          - name: frontend
            parallel: true
            targets: [web, api-gateway]
          - name: backend
            parallel: true
            targets: [app-server, worker]
          - name: data
            parallel: false
            targets: [cache, database]
    ```

    Stages execute in order. Within each stage, targets can run in parallel or sequentially.

## Behavior

Control how failures are handled:

```yaml
behavior:
  mode: Strict        # Strict or BestEffort
  failFast: true      # Stop on first failure
  retries: 3          # Max retry attempts (0-10)
```

| Mode | Description |
|------|-------------|
| `Strict` | Fail the entire plan if any target fails |
| `BestEffort` | Continue with remaining targets even if some fail |

## Targets

Each target defines a resource to hibernate:

```yaml
targets:
  - name: my-database          # Unique name within the plan
    type: rds                   # Executor type
    connectorRef:
      kind: CloudProvider       # CloudProvider or K8SCluster
      name: aws-prod
    parameters:                 # Executor-specific config
      snapshotBeforeStop: true
```

### Supported Target Types

| Type | Description | Connector Kind |
|------|-------------|----------------|
| `eks` | EKS managed node groups | CloudProvider |
| `karpenter` | Karpenter NodePools | K8SCluster |
| `rds` | RDS database instances | CloudProvider |
| `ec2` | EC2 instances | CloudProvider |
| `workloadscaler` | Kubernetes workloads | K8SCluster |

## Suspension

Temporarily disable a plan without deleting it:

```yaml
spec:
  suspend: true   # Stops all operations; running jobs complete naturally
```

Set back to `false` to resume.

## See Also

- [API Reference: HibernatePlan](../reference/api.md#hibernateplan) — Full field documentation
- [User Guide: Hibernation Lifecycle](../user-guides/hibernation-lifecycle.md) — Step-by-step operations
- [User Guide: Execution Strategies](../user-guides/execution-strategies.md) — Strategy deep dive
