# Staged Hibernation by Criticality

You manage a large shared test cluster for an enterprise. Hundreds of workloads live here, and nightly hibernation saves real money — but you want it to happen **politely, in criticality order**:

1. **Dev namespaces first** (`team-a`, `team-b`, `team-c`) — nobody notices, instant win.
2. **Batch compute second** — Karpenter pools need drain time, and you want them isolated in their own step.
3. **Shared infrastructure last** — the shared node groups and the reporting database, only after everything else is safely down.

The `Staged` strategy gives you exactly this: named stages, executed in order, with parallelism control *within* each stage.

## Goal

- Three stages, executed strictly in declaration order: `dev-workloads` → `batch-compute` → `shared-infra`.
- Within the dev stage, all three namespaces hibernate in parallel (bounded).
- Wakeup runs the stages in reverse: shared infra first, dev namespaces last.

## Configuration

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: test-staged
  namespace: hibernator-system
spec:
  schedule:
    timezone: "America/New_York"
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]

  execution:
    strategy:
      type: Staged
      stages:
        - name: dev-workloads
          parallel: true
          maxConcurrency: 3
          targets:
            - dev-ns-a
            - dev-ns-b
            - dev-ns-c

        - name: batch-compute
          parallel: false
          targets:
            - batch-nodepools

        - name: shared-infra
          parallel: false
          targets:
            - shared-nodegroups
            - reporting-db

  behavior:
    mode: Strict
    retries: 2

  targets:
    # Stage 1: dev namespaces — parallel
    - name: dev-ns-a
      type: workloadscaler
      connectorRef:
        kind: K8SCluster
        name: test-eks
      parameters:
        namespace:
          literals: [team-a]
        awaitCompletion:
          enabled: true

    - name: dev-ns-b
      type: workloadscaler
      connectorRef:
        kind: K8SCluster
        name: test-eks
      parameters:
        namespace:
          literals: [team-b]
        awaitCompletion:
          enabled: true

    - name: dev-ns-c
      type: workloadscaler
      connectorRef:
        kind: K8SCluster
        name: test-eks
      parameters:
        namespace:
          literals: [team-c]
        awaitCompletion:
          enabled: true

    # Stage 2: batch compute — needs drain time
    - name: batch-nodepools
      type: karpenter
      connectorRef:
        kind: K8SCluster
        name: test-eks
      parameters:
        nodeSelector:
          matchLabels:
            workload: batch
        awaitCompletion:
          enabled: true
          timeout: "10m"

    # Stage 3: shared infrastructure — sequential, most critical last
    - name: shared-nodegroups
      type: eks
      connectorRef:
        kind: CloudProvider
        name: aws-production
      parameters:
        clusterName: test-cluster
        nodeGroups:
          - name: shared-1
          - name: shared-2
        awaitCompletion:
          enabled: true
          timeout: "10m"

    - name: reporting-db
      type: rds
      connectorRef:
        kind: CloudProvider
        name: aws-production
      parameters:
        selector:
          instanceIds: [reporting-db]
        snapshotBeforeStop: true
        awaitCompletion:
          enabled: true
          timeout: "15m"
```

This assumes the `aws-production` CloudProvider from [Dependency-Ordered Shutdown](dependency-ordered-shutdown.md) and a `test-eks` K8SCluster connector for the test cluster (see [Connectors](../concepts/connectors.md)).

## How It Executes

**Shutdown — 20:00**

| Stage | Targets | Mode | Why this position |
|-------|---------|------|-------------------|
| 1. `dev-workloads` | `dev-ns-a`, `dev-ns-b`, `dev-ns-c` | Parallel (max 3) | Zero-risk wins first |
| 2. `batch-compute` | `batch-nodepools` | Sequential | Drains in isolation — if pods hang, nothing else is mid-flight |
| 3. `shared-infra` | `shared-nodegroups` → `reporting-db` | Sequential | Shared infra only after every tenant workload is down; DB last |

A stage starts **only after every target in the previous stage completes** — stages are hard barriers, not suggestions.

**Wakeup — 06:00** — stages run in reverse order: `shared-infra` (DB, then node groups), then `batch-compute`, then `dev-workloads`. Tenants get their namespaces back only after shared infrastructure is healthy.

!!! tip "Staged vs DAG"
    Both order executions. Choose **Staged** when your mental model is *coarse tiers with group parallelism*; choose [DAG](dependency-ordered-shutdown.md) when you need *fine-grained per-target edges* (e.g., target X waits on Y but not Z). Full comparison in [Execution Strategies](../user-guides/execution-strategies.md).

## Verification

Track which stage is currently executing:

```bash
kubectl get hibernateplan test-staged -n hibernator-system \
  -o jsonpath='{.status.currentStageIndex}{"\n"}{.status.currentOperation}{"\n"}'
# 1
# shutdown
```

(`currentStageIndex` is 0-based: `1` = the `batch-compute` stage is running.)

Per-target detail, as always:

```bash
kubectl get hibernateplan test-staged -n hibernator-system \
  -o jsonpath='{range .status.executions[*]}{.target}{"\t"}{.state}{"\n"}{end}'
```

## Variations

- **Mixed parallelism per stage** — make dev sequential (`parallel: false`) if the API server struggles at window start; make batch parallel if several independent pools exist.
- **Many namespaces** — replace per-namespace targets with one `workloadscaler` target using `namespace.selector` on a team/environment label, and keep stages for the compute tiers.
- **Pause between stages** — stages do not support built-in delays. If you need a human checkpoint between tiers, use two plans and [suspend](../user-guides/plan-suspension.md) the second one until you are ready.

## Next Steps

- [Execution Strategies](../user-guides/execution-strategies.md) — stage execution rules in full
- [Event-Mode Overrides](event-mode-overrides.md) — next: temporary per-target overrides for special events
- [Troubleshooting](../user-guides/troubleshooting.md) — when a stage stalls
