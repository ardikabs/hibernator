# Tolerating Partial Failures (BestEffort)

You run a large test cluster for a retail platform: six namespace groups plus a fleet of batch EC2 instances, all hibernated nightly. The problem: *something* always fails. A stuck PodDisruptionBudget in `pricing`, a finalizer that never clears in `reports`. With the default `Strict` behavior, one stuck namespace aborts the entire cycle — the other five targets stay running, and the cost savings evaporate.

You switch to **`BestEffort`**: every target gets its fair chance (including retries), failures are recorded honestly, and the rest of the fleet hibernates anyway. You review the failures over coffee in the morning.

## Goal

- All targets attempt hibernation every weeknight, regardless of individual failures.
- A failing target retries (3 attempts) before being marked `Failed`.
- Failures are visible in the plan status the next morning — never silently ignored.

## Strict vs BestEffort

| | `Strict` (default) | `BestEffort` |
|---|---|---|
| On target failure | Halts immediately — no further targets start | Continues executing remaining targets |
| Plan phase after failure | `Error` | Completes the cycle (failures recorded in status) |
| Failed target | Recorded in `status.executions` | Recorded in `status.executions` |
| Retries | Applied per target before declaring failure | Same |
| Best for | Production, ordered dependencies | Large fleets, non-critical environments |

!!! note "Retries still apply"
    `BestEffort` is not "fire once and ignore". Each target still retries up to `behavior.retries` times before it is marked `Failed`. You are choosing *isolation between targets*, not fewer attempts.

## Configuration

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: fleet-nightly
  namespace: hibernator-system
spec:
  schedule:
    timezone: "America/New_York"
    offHours:
      - start: "21:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]

  execution:
    strategy:
      type: Parallel
      maxConcurrency: 5

  behavior:
    mode: BestEffort
    retries: 3

  targets:
    - name: ns-shop
      type: workloadscaler
      connectorRef:
        kind: K8SCluster
        name: fleet-eks
      parameters:
        namespace:
          literals: [shop-web, shop-api]
        awaitCompletion:
          enabled: true

    - name: ns-inventory
      type: workloadscaler
      connectorRef:
        kind: K8SCluster
        name: fleet-eks
      parameters:
        namespace:
          literals: [inventory]
        awaitCompletion:
          enabled: true

    - name: ns-pricing
      type: workloadscaler
      connectorRef:
        kind: K8SCluster
        name: fleet-eks
      parameters:
        namespace:
          literals: [pricing]
        awaitCompletion:
          enabled: true

    - name: ns-reports
      type: workloadscaler
      connectorRef:
        kind: K8SCluster
        name: fleet-eks
      parameters:
        namespace:
          literals: [reports]
        includedGroups: [Deployment, StatefulSet]
        awaitCompletion:
          enabled: true

    - name: batch-instances
      type: ec2
      connectorRef:
        kind: CloudProvider
        name: aws-staging
      parameters:
        selector:
          tags:
            Environment: test
            Role: batch
        awaitCompletion:
          enabled: true
```

This assumes the `aws-staging` CloudProvider from [Partial Hibernation](partial-hibernation-critical-services.md) plus a `fleet-eks` K8SCluster connector pointing at the test cluster (see [Connectors](../concepts/connectors.md)).

## How It Executes

1. **21:00** — all five targets start (bounded by `maxConcurrency: 5`).
2. `ns-pricing` hits the stuck PDB. It retries 3 times, then is marked **`Failed`**.
3. The other four targets are unaffected and complete normally.
4. The plan reaches `Hibernated` — with the failure recorded. Wakeup at 06:00 proceeds for the completed targets.

## Verification

The next morning, read the execution ledger:

```bash
kubectl get hibernateplan fleet-nightly -n hibernator-system \
  -o jsonpath='{range .status.executions[*]}{.target}{"\t"}{.state}{"\t"}{.message}{"\n"}{end}'
```

```
ns-shop           Completed    8 workload(s) scaled down
ns-inventory      Completed    3 workload(s) scaled down
ns-pricing        Failed       waiting for workload replicas to scale: deadline exceeded
ns-reports        Completed    5 workload(s) scaled down
batch-instances   Completed    12 instance(s) stopped
```

```bash
kubectl get hibernateplan fleet-nightly -n hibernator-system -o jsonpath='{.status.phase}'
# Hibernated
```

Now fix the root cause in `pricing`, then re-run just the cycle — see [Error Recovery](../user-guides/error-recovery.md) and the `retry-now` annotation in [Override Actions](../user-guides/override-actions.md).

!!! warning "BestEffort is not 'ignore failures'"
    Failures still land in the ledger and in metrics. Pair this setup with [Notifications](../user-guides/notifications.md) (`Failure` events) or a morning status check, or `Failed` targets will quietly pile up. For unattended production environments, prefer [Strict with a DAG](dependency-ordered-shutdown.md).

## Variations

- **BestEffort + DAG** — a failed target causes its *downstream dependents* to be marked `Aborted` and skipped, while independent branches continue. See [Dependency-Ordered Shutdown](dependency-ordered-shutdown.md).
- **Tighter concurrency** — drop `maxConcurrency` to `2` to reduce API pressure on the cluster at window start.
- **Escalate important targets** — split the fleet into two plans: a Strict plan for the namespaces that matter and a BestEffort plan for the long tail.

## Next Steps

- [Error Recovery](../user-guides/error-recovery.md) — retry semantics and recovery flows
- [Override Actions](../user-guides/override-actions.md) — `retry-now`, restart, and manual overrides
- [Dependency-Ordered Shutdown](dependency-ordered-shutdown.md) — next tier: ordering with DAG
