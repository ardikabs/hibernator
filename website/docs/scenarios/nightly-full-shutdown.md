# Nightly Full Shutdown & Wakeup

You are a platform engineer at a startup. Your team works in a single development environment on AWS during the day — and nobody touches it at night. The whole stack (EKS node groups, the RDS database, and a couple of EC2 bastion hosts) should **shut down every weeknight at 20:00 and come back at 06:00**, fully automatically.

This is the canonical Hibernator scenario: one plan, multiple targets, full shutdown and full wakeup.

## Goal

- Every weeknight at **20:00 Asia/Jakarta**: stop all node groups in `dev-cluster`, stop the `dev-postgres` database, stop all dev bastion instances.
- Every weekday morning at **06:00 Asia/Jakarta**: restore everything to its previous state.
- If anything fails to stop, stop the world and alert (Strict behavior) — a partially hibernated environment is worse than none.

## Resources Involved

| Target | Type | What it manages | Selection |
|--------|------|-----------------|-----------|
| `dev-nodegroups` | `eks` | All managed node groups in `dev-cluster` | `nodeGroups: []` (all) |
| `dev-database` | `rds` | The `dev-postgres` instance | Explicit `instanceIds` |
| `dev-bastion` | `ec2` | Dev bastion hosts | Tags `Environment=dev`, `Role=bastion` |

All targets are reached through a single `CloudProvider` connector using IRSA with role assumption.

## Configuration

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: CloudProvider
metadata:
  name: aws-dev
  namespace: hibernator-system
spec:
  type: aws
  aws:
    accountId: "123456789012"
    region: ap-southeast-3
    assumeRoleArn: arn:aws:iam::123456789012:role/hibernator-runner
    auth:
      serviceAccount: {}
---
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: dev-nightly
  namespace: hibernator-system
spec:
  schedule:
    timezone: "Asia/Jakarta"
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]

  execution:
    strategy:
      type: Parallel
      maxConcurrency: 3

  behavior:
    mode: Strict
    retries: 2

  targets:
    - name: dev-nodegroups
      type: eks
      connectorRef:
        kind: CloudProvider
        name: aws-dev
      parameters:
        clusterName: dev-cluster
        nodeGroups: []
        awaitCompletion:
          enabled: true

    - name: dev-database
      type: rds
      connectorRef:
        kind: CloudProvider
        name: aws-dev
      parameters:
        selector:
          instanceIds: [dev-postgres]
        snapshotBeforeStop: true
        awaitCompletion:
          enabled: true

    - name: dev-bastion
      type: ec2
      connectorRef:
        kind: CloudProvider
        name: aws-dev
      parameters:
        selector:
          tags:
            Environment: dev
            Role: bastion
        awaitCompletion:
          enabled: true
```

```bash
kubectl apply -f dev-nightly.yaml
```

!!! tip "Why Parallel?"
    The three targets are independent — nothing about the database depends on the node groups in this simple setup. `Parallel` with `maxConcurrency: 3` lets everything stop and start as fast as possible. When targets *do* depend on each other, use a [DAG](dependency-ordered-shutdown.md) instead.

!!! note "`snapshotBeforeStop: true`"
    RDS creates a final snapshot before stopping the instance. It costs a little storage and adds a few minutes to shutdown, but it is cheap insurance for a database. See the [RDS executor guide](../user-guides/rds-executor.md).

## How It Executes

1. **Weekday 20:00** — the schedule window opens. The plan transitions `Active → Hibernating`. Three runner Jobs are dispatched (at most 3 concurrently), one per target.
2. Each executor stops its resources and saves **restore data** (original node group sizes, instance state, DB identifier) to ConfigMaps in the operator namespace.
3. When all targets complete, the plan transitions to **`Hibernated`** and stays there for the rest of the window.
4. **Weekday 06:00** — the window closes. The plan transitions to **`WakingUp`**. Runner Jobs restore every target from its saved restore data.
5. When wakeup completes, the plan returns to **`Active`** and waits for the next window.

| Phase | Meaning |
|-------|---------|
| `Active` | Waiting for the next off-hours window |
| `Hibernating` | Shutdown in progress |
| `Hibernated` | Everything is stopped, waiting for window end |
| `WakingUp` | Restore in progress |
| `Suspended` | Plan manually paused ([details](../user-guides/plan-suspension.md)) |
| `Error` | Execution failed after all retries ([details](../user-guides/error-recovery.md)) |

## Verification

```bash
# Watch the phase transitions in real time
kubectl get hibernateplan dev-nightly -n hibernator-system -w

# Inspect the per-target execution ledger
kubectl get hibernateplan dev-nightly -n hibernator-system \
  -o jsonpath='{range .status.executions[*]}{.target}{"\t"}{.state}{"\t"}{.message}{"\n"}{end}'

# Follow runner Job logs
kubectl logs -n hibernator-system -l hibernator.ardikabs.com/plan=dev-nightly
```

Expected ledger after a successful shutdown:

```
dev-nodegroups    Completed    2 node group(s) scaled to zero
dev-database      Completed    instance dev-postgres stopped (snapshot created)
dev-bastion       Completed    2 instance(s) stopped
```

## Variations

- **Bound the blast radius** — set `maxConcurrency: 1` to stop targets one at a time (order follows the `targets` list).
- **Discover the database by tags** instead of an explicit ID:

    ```yaml
    parameters:
      selector:
        tags:
          Environment: dev
        discoverInstances: true   # required — tags alone discover nothing
    ```

    !!! warning
        Dynamic RDS selection requires `discoverInstances: true` and/or `discoverClusters: true`. A tag selector by itself is a **no-op**. See the [executor parameters reference](../reference/executor-parameters.md#rdsparameters).

- **Cover weekends too** — see [Weekend Hibernation](weekend-hibernation.md).

## Next Steps

- [Hibernation Lifecycle](../user-guides/hibernation-lifecycle.md) — deeper look at phases and cycles
- [Weekend Hibernation](weekend-hibernation.md) — extend this setup across the weekend
- [Manage Hibernation via CLI](../user-guides/cli.md) — status, logs, suspend, and retry from your terminal
