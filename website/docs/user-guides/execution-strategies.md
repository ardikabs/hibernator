# Execution Strategies

Execution strategies control the order and parallelism of target processing during hibernation and wakeup cycles.


## Sequential

**Note:** For all execution strategies, wakeup order is always the reverse of shutdown order. This ensures dependencies are restored in the correct sequence.

Targets execute one at a time in the order they are declared in `spec.targets`.

```yaml
execution:
  strategy:
    type: Sequential
```

**Best for**: Simple plans with few targets where order matters but explicit dependencies are overkill.

**Wakeup order**: Reverse of shutdown order.

## Parallel

All targets execute simultaneously, bounded by `maxConcurrency`.

```yaml
execution:
  strategy:
    type: Parallel
    maxConcurrency: 3    # At most 3 targets running at once
```

**Best for**: Independent targets that don't depend on each other.

!!! note
    If `maxConcurrency` is not set, all targets start at the same time.

## DAG (Directed Acyclic Graph)

Targets execute in topological order based on explicit dependency edges. The controller uses Kahn's algorithm to determine execution order.

```yaml
execution:
  strategy:
    type: DAG
    maxConcurrency: 3
    dependencies:
      - from: karpenter-nodes
        to: eks-nodegroups      # Karpenter before managed node groups
      - from: app-servers
        to: database            # Apps before database
      - from: worker-pool
        to: database            # Workers before database
```


### Dependency Semantics

- `from: A, to: B` means **A must complete before B starts** during shutdown.
- During wakeup, the order is **reversed**: B starts before A. This ensures that dependencies are restored in the correct order, mirroring the shutdown DAG in reverse.
- Targets with no dependencies execute as soon as possible (respecting `maxConcurrency`).

### Cycle Detection

The validation webhook detects cycles at admission time:

```
# This would be rejected:
dependencies:
  - from: A
    to: B
  - from: B
    to: A     # Creates a cycle!
```

### BestEffort with DAG

When using `behavior.mode: BestEffort` with DAG strategy, if a target fails, its downstream dependents are marked as `Aborted` (not `Failed`) and skipped. Independent branches continue executing.

```yaml
behavior:
  mode: BestEffort
  failFast: false
execution:
  strategy:
    type: DAG
    dependencies:
      - from: frontend
        to: backend
      - from: cache
        to: database
```

If `frontend` fails, `backend` is aborted, but `cache` and `database` continue independently.

## Staged

Targets are grouped into named stages that execute in order. Within each stage, targets can run in parallel or sequentially.

```yaml
execution:
  strategy:
    type: Staged
    stages:
      - name: frontend-tier
        parallel: true
        maxConcurrency: 2
        targets:
          - frontend-web
          - frontend-api

      - name: backend-tier
        parallel: true
        targets:
          - backend-service
          - worker-service

      - name: data-tier
        parallel: false
        targets:
          - cache-redis
          - database
```

### Stage Execution Rules

- Stages execute in declaration order (top to bottom)
- A stage starts only after all targets in the previous stage complete
- Within a stage:
    - `parallel: true` — Targets run simultaneously (bounded by `maxConcurrency`)
    - `parallel: false` — Targets run sequentially in declaration order
- During wakeup, stages execute in reverse order

**Best for**: Tiered architectures where you want explicit grouping with fine-grained parallelism control.

## Choosing a Strategy

| Strategy | Use When |
|----------|----------|
| Sequential | Simple plans, guaranteed ordering |
| Parallel | Independent targets, fastest execution |
| DAG | Complex dependencies between targets |
| Staged | Tiered architecture, grouped execution |

---

## Complete Examples

### Example: Sequential — Dev Environment Shutdown

A simple development environment with a few resources shut down in order:

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: dev-sequential
  namespace: hibernator-system
spec:
  schedule:
    timezone: America/New_York
    offHours:
      - start: "19:00"
        end: "07:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
  execution:
    strategy:
      type: Sequential
  behavior:
    mode: Strict
    retries: 2
  targets:
    # 1. Scale down application workloads first
    - name: dev-workloads
      type: workloadscaler
      connectorRef:
        kind: K8SCluster
        name: eks-dev
      parameters:
        namespace:
          literals: [default, dev]
        includedGroups: [Deployment]
        awaitCompletion:
          enabled: true

    # 2. Then stop EC2 bastion hosts
    - name: dev-bastions
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

    # 3. Finally stop the database
    - name: dev-database
      type: rds
      connectorRef:
        kind: CloudProvider
        name: aws-dev
      parameters:
        selector:
          instanceIds: [dev-postgres]
        snapshotBeforeStop: false
        awaitCompletion:
          enabled: true
```

**Shutdown order:** `dev-workloads` → `dev-bastions` → `dev-database`
**Wakeup order (reversed):** `dev-database` → `dev-bastions` → `dev-workloads`

### Example: Parallel — Independent Staging Environments

Multiple independent staging environments that don't share resources:

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: staging-parallel
  namespace: hibernator-system
spec:
  schedule:
    timezone: UTC
    offHours:
      - start: "22:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
      - start: "00:00"
        end: "23:59"
        daysOfWeek: ["SAT", "SUN"]
  execution:
    strategy:
      type: Parallel
      maxConcurrency: 4
  behavior:
    mode: BestEffort
    retries: 3
  targets:
    # All targets are independent — execute in parallel
    - name: staging-a-eks
      type: eks
      connectorRef:
        kind: CloudProvider
        name: aws-staging
      parameters:
        clusterName: staging-a
        nodeGroups: []
        awaitCompletion:
          enabled: true

    - name: staging-b-eks
      type: eks
      connectorRef:
        kind: CloudProvider
        name: aws-staging
      parameters:
        clusterName: staging-b
        nodeGroups: []
        awaitCompletion:
          enabled: true

    - name: staging-a-rds
      type: rds
      connectorRef:
        kind: CloudProvider
        name: aws-staging
      parameters:
        selector:
          tags:
            Environment: staging-a
          discoverInstances: true
        awaitCompletion:
          enabled: true

    - name: staging-b-rds
      type: rds
      connectorRef:
        kind: CloudProvider
        name: aws-staging
      parameters:
        selector:
          tags:
            Environment: staging-b
          discoverInstances: true
        awaitCompletion:
          enabled: true

    - name: staging-ec2
      type: ec2
      connectorRef:
        kind: CloudProvider
        name: aws-staging
      parameters:
        selector:
          tags:
            Environment: staging
        awaitCompletion:
          enabled: true
```

**Behavior:** Up to 4 targets run simultaneously. With `BestEffort` mode, if one staging environment fails, the others continue independently.

### Example: DAG — Production Infrastructure with Dependencies

A production environment with explicit dependency ordering between resource tiers:

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: production-dag
  namespace: hibernator-system
spec:
  schedule:
    timezone: Asia/Jakarta
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
  execution:
    strategy:
      type: DAG
      maxConcurrency: 3
      dependencies:
        # Application layer shuts down first
        - from: app-workloads
          to: karpenter-pools
        - from: app-workloads
          to: eks-nodegroups
        # Infrastructure layer before data layer
        - from: karpenter-pools
          to: database
        - from: eks-nodegroups
          to: database
        # Worker instances depend on database
        - from: worker-ec2
          to: database
  behavior:
    mode: BestEffort
    failFast: false
    retries: 3
  targets:
    - name: app-workloads
      type: workloadscaler
      connectorRef:
        kind: K8SCluster
        name: eks-production
      parameters:
        namespace:
          literals: [default, app]
        includedGroups: [Deployment]
        workloadSelector:
          matchLabels:
            tier: application
        awaitCompletion:
          enabled: true

    - name: karpenter-pools
      type: karpenter
      connectorRef:
        kind: K8SCluster
        name: eks-production
      parameters:
        nodePools: []
        awaitCompletion:
          enabled: true
          timeout: "5m"

    - name: eks-nodegroups
      type: eks
      connectorRef:
        kind: CloudProvider
        name: aws-production
      parameters:
        clusterName: production-cluster
        nodeGroups:
          - name: app-nodes
          - name: worker-nodes
        awaitCompletion:
          enabled: true
          timeout: "10m"

    - name: worker-ec2
      type: ec2
      connectorRef:
        kind: CloudProvider
        name: aws-production
      parameters:
        selector:
          tags:
            Component: worker
        awaitCompletion:
          enabled: true

    - name: database
      type: rds
      connectorRef:
        kind: CloudProvider
        name: aws-production
      parameters:
        selector:
          instanceIds: [production-db]
        snapshotBeforeStop: true
        awaitCompletion:
          enabled: true
          timeout: "15m"
```

**Shutdown order:**

```
app-workloads ──→ karpenter-pools ──→ database
             └──→ eks-nodegroups ──→ database
worker-ec2 ─────────────────────────→ database
```

1. `app-workloads` and `worker-ec2` start first (no upstream dependencies)
2. `karpenter-pools` and `eks-nodegroups` start after `app-workloads` completes
3. `database` starts last, after all upstream targets complete

**Wakeup order (reversed):** `database` → `karpenter-pools` + `eks-nodegroups` + `worker-ec2` → `app-workloads`

### Example: Staged — Three-Tier Architecture

A classic three-tier application with explicit stage grouping:

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: three-tier-staged
  namespace: hibernator-system
spec:
  schedule:
    timezone: America/New_York
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
  execution:
    strategy:
      type: Staged
      stages:
        # Stage 1: Presentation tier (parallel — all frontends at once)
        - name: presentation-tier
          parallel: true
          maxConcurrency: 3
          targets:
            - frontend-web
            - frontend-api
            - frontend-admin

        # Stage 2: Application tier (parallel — backend services)
        - name: application-tier
          parallel: true
          targets:
            - backend-service
            - worker-service
            - scheduler-service

        # Stage 3: Infrastructure tier (sequential — careful ordering)
        - name: infrastructure-tier
          parallel: false
          targets:
            - karpenter-pools
            - eks-nodegroups

        # Stage 4: Data tier (sequential — most critical last)
        - name: data-tier
          parallel: false
          targets:
            - cache-redis
            - database-rds
  behavior:
    mode: Strict
    retries: 2
  targets:
    # Presentation tier targets
    - name: frontend-web
      type: workloadscaler
      connectorRef:
        kind: K8SCluster
        name: eks-production
      parameters:
        namespace:
          literals: [frontend]
        workloadSelector:
          matchLabels:
            component: web
        includedGroups: [Deployment]
        awaitCompletion:
          enabled: true

    - name: frontend-api
      type: workloadscaler
      connectorRef:
        kind: K8SCluster
        name: eks-production
      parameters:
        namespace:
          literals: [frontend]
        workloadSelector:
          matchLabels:
            component: api-gateway
        includedGroups: [Deployment]
        awaitCompletion:
          enabled: true

    - name: frontend-admin
      type: workloadscaler
      connectorRef:
        kind: K8SCluster
        name: eks-production
      parameters:
        namespace:
          literals: [frontend]
        workloadSelector:
          matchLabels:
            component: admin
        includedGroups: [Deployment]
        awaitCompletion:
          enabled: true

    # Application tier targets
    - name: backend-service
      type: workloadscaler
      connectorRef:
        kind: K8SCluster
        name: eks-production
      parameters:
        namespace:
          literals: [backend]
        workloadSelector:
          matchLabels:
            component: api
        includedGroups: [Deployment]
        awaitCompletion:
          enabled: true

    - name: worker-service
      type: workloadscaler
      connectorRef:
        kind: K8SCluster
        name: eks-production
      parameters:
        namespace:
          literals: [backend]
        workloadSelector:
          matchLabels:
            component: worker
        includedGroups: [Deployment]
        awaitCompletion:
          enabled: true

    - name: scheduler-service
      type: workloadscaler
      connectorRef:
        kind: K8SCluster
        name: eks-production
      parameters:
        namespace:
          literals: [backend]
        workloadSelector:
          matchLabels:
            component: scheduler
        includedGroups: [Deployment]
        awaitCompletion:
          enabled: true

    # Infrastructure tier targets
    - name: karpenter-pools
      type: karpenter
      connectorRef:
        kind: K8SCluster
        name: eks-production
      parameters:
        nodePools: []
        awaitCompletion:
          enabled: true

    - name: eks-nodegroups
      type: eks
      connectorRef:
        kind: CloudProvider
        name: aws-production
      parameters:
        clusterName: production-cluster
        nodeGroups: []
        awaitCompletion:
          enabled: true
          timeout: "10m"

    # Data tier targets
    - name: cache-redis
      type: ec2
      connectorRef:
        kind: CloudProvider
        name: aws-production
      parameters:
        selector:
          tags:
            Component: redis-cache
        awaitCompletion:
          enabled: true

    - name: database-rds
      type: rds
      connectorRef:
        kind: CloudProvider
        name: aws-production
      parameters:
        selector:
          instanceIds: [production-db]
        snapshotBeforeStop: true
        awaitCompletion:
          enabled: true
          timeout: "15m"
```

**Shutdown order:**

| Stage | Targets | Mode |
|-------|---------|------|
| 1. presentation-tier | `frontend-web`, `frontend-api`, `frontend-admin` | Parallel (max 3) |
| 2. application-tier | `backend-service`, `worker-service`, `scheduler-service` | Parallel |
| 3. infrastructure-tier | `karpenter-pools` → `eks-nodegroups` | Sequential |
| 4. data-tier | `cache-redis` → `database-rds` | Sequential |

**Wakeup order (reversed):** data-tier → infrastructure-tier → application-tier → presentation-tier
