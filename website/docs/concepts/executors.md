# Executors

Executors are the **hands** of the Hibernator operator. While the control plane (brain) decides *when* and *in what order* to act, executors know *how* to shut down and wake up a specific type of resource.

## Executor Contract

Every executor implements three operations:

| Operation | Purpose |
|-----------|---------|
| **Validate** | Verify parameters and connectivity before execution |
| **Shutdown** | Stop or scale-down the resource, capturing restore metadata |
| **WakeUp** | Restore the resource to its pre-hibernation state using saved metadata |

Executors own **idempotency** — calling Shutdown on an already-stopped resource or WakeUp on an already-running resource must succeed without side effects.

## Intent Preservation Contract

Hibernator implements a **first-capture-wins** intent preservation strategy to handle retries and partial failures during shutdown operations.

### Demanded State

Each executor defines a **demanded state** — the condition that qualifies a resource for hibernator management:

| Executor | Demanded State | Intent Field |
|----------|---------------|--------------|
| **EC2** | Instance state is `running` | `wasRunning` |
| **EKS** | Node group `desiredSize > 0` | `wasScaled` |
| **RDS** | DB instance/cluster status is `available` | `wasRunning` |
| **Karpenter** | NodePool exists | (full spec captured) |
| **WorkloadScaler** | Workload `replicas > 0` | `wasScaled` |

Only resources in their demanded state are captured and managed by hibernator. Resources not in demanded state are observed passively.

### First-Capture-Wins Semantics

When a resource is first captured during a hibernation cycle:

1. **Intent is locked**: The `wasRunning` or `wasScaled` value is preserved indefinitely
2. **Cycle tracking**: An internal `_managedByCycleID` field marks which cycle first captured the resource
3. **Immutable intent**: On subsequent hibernation attempts (retries), the original intent is preserved even if the resource's current state has changed

This ensures that:
- **Retry safety**: If shutdown fails and user retries, the original intent from the first capture is preserved
- **Consistency**: Once hibernator decides a resource should be managed, that decision persists until successful wakeup
- **Idempotency**: Multiple shutdown attempts don't corrupt restore data

### Stale Resource Eviction

If a resource is not reported for 3 consecutive hibernation cycles:

1. The resource is evicted from restore data
2. The `_managedByCycleID` marker is cleared
3. On the next hibernation, the resource can be freshly captured

This prevents permanently retaining data for deleted or unmanageable resources while allowing temporary absences (e.g., API failures) without data loss.

### Edge Case Handling

**Resource state changes between hibernation and wakeup:**
- If a resource is manually stopped/deleted after hibernation, the executor skips it during wakeup
- The executor handles "resource not found" or "already in desired state" gracefully
- Hibernator's contract is: *"restore to the captured intent, but tolerate reality"*

**Example EC2 flow:**
```
Cycle 1 (abc123): Instance is running → captured with wasRunning=true
Retry (def456):   Shutdown blocked, instance still running → wasRunning=true preserved
WakeUp (ghi789):  Instance may be running/stopped/terminated → executor handles each case
```

## How Executors Run

Executors do not run inside the controller. Instead, the controller creates an isolated **Runner Job** for each target. The runner:

1. Loads the executor matching the target's `type` field
2. Calls `Validate` to verify parameters
3. Calls `Shutdown` or `WakeUp` depending on the operation
4. Streams logs and progress to the control plane via gRPC
5. Persists restore metadata in a ConfigMap (`restore-data-{plan-name}`)

Each runner gets an ephemeral ServiceAccount with the minimum permissions needed.

## Restore Data

During shutdown, executors capture metadata about the resource's current state (e.g., replica counts, scaling configs, instance IDs). This metadata is stored as JSON in a ConfigMap and used during wakeup to restore the resource to its exact pre-hibernation configuration.

The restore data ConfigMap is namespaced as `restore-data-{plan-name}` with keys formatted as `{executor}_{target-name}`.

## Built-in Executors

| Executor | Resource | Provider | Connector | Status |
|----------|----------|----------|-----------|--------|
| [`eks`](#eks) | EKS Managed Node Groups | AWS | CloudProvider | :white_check_mark: Implemented |
| [`karpenter`](#karpenter) | Karpenter NodePools | Kubernetes | K8SCluster | :white_check_mark: Implemented |
| [`ec2`](#ec2) | EC2 Instances | AWS | CloudProvider | :white_check_mark: Implemented |
| [`rds`](#rds) | RDS Instances & Clusters | AWS | CloudProvider | :white_check_mark: Implemented |
| [`workloadscaler`](#workloadscaler) | Kubernetes Workloads | Kubernetes | K8SCluster | :white_check_mark: Implemented |
| [`noop`](#noop) | None (testing) | — | Any | :white_check_mark: Implemented |
| [`gke`](#gke) | GKE Node Pools | GCP | K8SCluster | :construction: Not Implemented |
| [`cloudsql`](#cloudsql) | Cloud SQL Instances | GCP | CloudProvider | :construction: Not Implemented |

---

## EKS

**Type:** `eks` · **Connector:** `CloudProvider` (AWS)

Manages **EKS Managed Node Groups** by scaling them to zero during hibernation and restoring original scaling configuration on wakeup.

!!! note
    This executor only handles Managed Node Groups via the AWS EKS API. For Karpenter-managed NodePools, use the separate [`karpenter`](#karpenter) executor.

### Shutdown Flow

1. **Discover node groups** — If `nodeGroups` is empty, lists all node groups in the cluster via `ListNodegroups`. Otherwise, uses the specified list.
2. **Capture state** — For each node group, calls `DescribeNodegroup` to record the current `desiredSize`, `minSize`, and `maxSize`.
3. **Persist restore data** — Saves the scaling configuration per node group to the restore ConfigMap.
4. **Scale to zero** — Calls `UpdateNodegroupConfig` setting `minSize=0` and `desiredSize=0` (keeps `maxSize` unchanged).
5. **Await (optional)** — If `awaitCompletion` is enabled, polls until all nodes with label `eks.amazonaws.com/nodegroup={name}` are deleted.

### Wakeup Flow

1. **Load restore data** — Reads the saved scaling configuration from the ConfigMap.
2. **Restore scaling** — For each node group, calls `UpdateNodegroupConfig` with the original `desiredSize`, `minSize`, and `maxSize`.
3. **Await (optional)** — Polls `DescribeNodegroup` until the node group status returns to `ACTIVE` and node counts match.

### Restore Data Shape

Each node group is stored under its name:

```json
{
  "app-nodes": { "desired": 3, "min": 1, "max": 5 },
  "worker-nodes": { "desired": 2, "min": 0, "max": 4 }
}
```

### Prerequisites

| Requirement | Details |
|-------------|---------|
| **Connector** | `CloudProvider` with `type: aws` |
| **IAM Permissions** | `eks:ListNodegroups`, `eks:DescribeNodegroup`, `eks:UpdateNodegroupConfig` |
| **Await Timeout** | Default: 10 minutes |

### Limitations

- Does **not** drain nodes — relies on AWS default graceful termination behavior.
- The EKS cluster itself stays up; only node groups are scaled.
- Multi-AZ distribution is handled transparently by AWS.

---

## Karpenter

**Type:** `karpenter` · **Connector:** `K8SCluster`

Manages **Karpenter NodePools** by deleting them during hibernation (which tells Karpenter to drain and remove all managed nodes) and recreating them with the original spec on wakeup.

### Shutdown Flow

1. **Discover NodePools** — If `nodePools` is empty, lists all NodePools via the `karpenter.sh/v1` API. Otherwise, uses the specified names.
2. **Capture state** — For each NodePool, retrieves the full spec and labels using a `Get` call.
3. **Persist restore data** — Saves the complete NodePool definition (name, spec, labels) to the restore ConfigMap.
4. **Delete NodePools** — Calls `Delete` on each NodePool. Karpenter automatically evicts pods and terminates the underlying nodes.
5. **Await (optional)** — Polls until all nodes with label `karpenter.sh/nodepool={name}` are gone.

### Wakeup Flow

1. **Load restore data** — Reads saved NodePool definitions.
2. **Recreate NodePools** — Reconstructs each NodePool object with the original spec, labels, and API version, then calls `Create`.
3. **Await (optional)** — Polls NodePool status until the `Ready` condition is `True`.

### Restore Data Shape

Each NodePool is stored under its name with the full spec:

```json
{
  "default": {
    "name": "default",
    "spec": { "template": {}, "limits": {}, "disruption": {} },
    "labels": { "team": "platform" }
  }
}
```

### Prerequisites

| Requirement | Details |
|-------------|---------|
| **Connector** | `K8SCluster` with access to the target cluster |
| **RBAC** | `karpenter.sh nodepools` (get, list, delete, create), `v1 nodes` (list, get) |
| **Await Timeout** | Default: 5 minutes |

### Limitations

- Assumes `karpenter.sh/v1` API version. Earlier Karpenter versions using `v1beta1` may require adaptation.
- Karpenter respects Pod Disruption Budgets during eviction — the shutdown may not complete within the timeout if PDBs block.
- NodePool admission webhooks with side effects could interfere with deletion or recreation.

---

## EC2

**Type:** `ec2` · **Connector:** `CloudProvider` (AWS)

Manages **EC2 instances** by stopping running instances during hibernation and starting them back on wakeup. Automatically excludes instances managed by Auto Scaling Groups or Karpenter.

### Shutdown Flow

1. **Discover instances** — Calls `DescribeInstances` with tag filters (from `selector.tags`) or explicit `selector.instanceIds`. Filters out terminated/shutting-down instances and those managed by ASGs or Karpenter.
2. **Capture state** — Records each instance's ID and whether it was running (`wasRunning`).
3. **Persist restore data** — Saves instance states to the restore ConfigMap.
4. **Stop instances** — Calls `StopInstances` for all instances that were running. Already-stopped instances are skipped.
5. **Await (optional)** — Polls `DescribeInstances` until all instances reach the `stopped` state.

### Wakeup Flow

1. **Load restore data** — Reads saved instance states.
2. **Start instances** — Calls `StartInstances` only for instances where `wasRunning=true`. Instances that were already stopped before hibernation remain stopped.
3. **Await (optional)** — Polls until all started instances reach the `running` state.

### Restore Data Shape

Each instance is stored under its ID:

```json
{
  "i-0abc123def456789a": { "instanceId": "i-0abc123def456789a", "wasRunning": true },
  "i-0def456789abc0123": { "instanceId": "i-0def456789abc0123", "wasRunning": false }
}
```

### Prerequisites

| Requirement | Details |
|-------------|---------|
| **Connector** | `CloudProvider` with `type: aws` |
| **IAM Permissions** | `ec2:DescribeInstances`, `ec2:StopInstances`, `ec2:StartInstances` |
| **Await Timeout** | Default: 5 minutes |

### Limitations

- **ASG-managed instances are excluded** — instances owned by Auto Scaling Groups are skipped to avoid conflicts with ASG desired-count reconciliation.
- **Karpenter-managed instances are excluded** — same logic applies.
- Elastic IPs remain associated through stop/start cycles.
- EBS volumes are preserved; instance store data is lost on stop (standard EC2 behavior).

---

## RDS

**Type:** `rds` · **Connector:** `CloudProvider` (AWS)

Manages **RDS DB instances and Aurora clusters** with support for optional snapshot creation before stopping. Features a sophisticated selector system for targeting resources by tags, explicit IDs, or discovery mode.

### Shutdown Flow

1. **Determine resource types** — Based on the selector:
      - Explicit `instanceIds`/`clusterIds` → resource types inferred from which IDs are provided.
      - Tag-based or `includeAll` → requires `discoverInstances` and/or `discoverClusters` flags to be explicitly set.
2. **Discover resources** — Calls `DescribeDBInstances` and/or `DescribeDBClusters` with appropriate filters.
3. **For each DB instance:**
      - Checks status is `available` (skips if not stoppable).
      - If `snapshotBeforeStop=true`, creates a snapshot via `CreateDBSnapshot` and waits for it to complete (30-minute waiter).
      - Calls `StopDBInstance`.
      - Saves state: instance ID, previous status, snapshot ID if created.
4. **For each DB cluster:**
      - Same logic via `StopDBCluster` and `CreateDBClusterSnapshot`.
5. **Await (optional)** — Polls until all resources reach the `stopped` status.

### Wakeup Flow

1. **Load restore data** — Reads saved instance/cluster states.
2. **Start resources** — Calls `StartDBInstance` or `StartDBCluster` for each resource that was running before hibernation.
3. **Await (optional)** — Polls until all resources return to `available` status.

### Restore Data Shape

Keys use a type prefix to distinguish instances from clusters:

```json
{
  "instance:production-db": {
    "instanceId": "production-db",
    "wasStopped": false,
    "snapshotId": "production-db-hibernate-1711500000",
    "instanceType": "db.r5.2xlarge"
  },
  "cluster:aurora-prod": {
    "clusterId": "aurora-prod",
    "wasStopped": false,
    "snapshotId": "aurora-prod-hibernate-1711500000"
  }
}
```

### Selector Modes

The RDS executor supports three mutually exclusive selection methods:

| Mode | Fields | Discovery Flags Required? |
|------|--------|--------------------------|
| **Tag-based** | `tags` or `excludeTags` | Yes — must set `discoverInstances` and/or `discoverClusters` |
| **Explicit IDs** | `instanceIds` and/or `clusterIds` | No — inferred from which IDs are provided |
| **Discovery** | `includeAll` | Yes — must set `discoverInstances` and/or `discoverClusters` |

!!! warning
    Setting `tags` without `discoverInstances` or `discoverClusters` results in a **no-op** — nothing will be discovered.

### Prerequisites

| Requirement | Details |
|-------------|---------|
| **Connector** | `CloudProvider` with `type: aws` |
| **IAM Permissions** | `rds:DescribeDBInstances`, `rds:DescribeDBClusters`, `rds:StopDBInstance`, `rds:StartDBInstance`, `rds:StopDBCluster`, `rds:StartDBCluster`, `rds:CreateDBSnapshot` (if snapshots enabled) |
| **Await Timeout** | Default: 15 minutes |

### Limitations

- **Read replicas** are not managed — only primary instances and clusters.
- **Aurora Serverless** supports stop/start but auto-scaling behavior on wakeup may differ.
- RDS Proxy connections are not managed by this executor.
- The 7-day auto-restart limit imposed by AWS still applies — RDS automatically restarts instances that have been stopped for more than 7 days.

---

## WorkloadScaler

**Type:** `workloadscaler` · **Connector:** `K8SCluster`

Manages **Kubernetes workloads** (Deployments, StatefulSets, ReplicaSets, or any CRD with a scale subresource) by scaling replicas to zero during hibernation and restoring original counts on wakeup.

### Shutdown Flow

1. **Resolve target namespaces** — Uses `namespace.literals` (explicit list) or `namespace.selector` (label-based discovery).
2. **Resolve workload kinds** — Uses `includedGroups` (defaults to `["Deployment"]`). Custom CRDs use the format `group/version/resource` (e.g., `argoproj.io/v1alpha1/rollouts`).
3. **Discover workloads** — Lists resources in each namespace, optionally filtered by `workloadSelector` labels.
4. **For each workload:**
      - Reads the scale subresource via `GetScale()` to capture current replica count.
      - Saves state: namespace, kind, name, replica count, GVR.
      - Updates the scale subresource to `replicas: 0`.
5. **Await (optional)** — Polls until each workload's scale status reflects zero replicas.

### Wakeup Flow

1. **Load restore data** — Reads saved workload states.
2. **Restore replicas** — For each workload, updates the scale subresource back to the original replica count.
3. **Await (optional)** — Polls until replica counts match the desired state.

### Restore Data Shape

Keys use a `namespace/kind/name` format:

```json
{
  "default/Deployment/api-server": {
    "group": "apps", "version": "v1", "resource": "deployments",
    "kind": "Deployment", "namespace": "default",
    "name": "api-server", "replicas": 3
  },
  "default/Deployment/worker": {
    "group": "apps", "version": "v1", "resource": "deployments",
    "kind": "Deployment", "namespace": "default",
    "name": "worker", "replicas": 2
  }
}
```

### Prerequisites

| Requirement | Details |
|-------------|---------|
| **Connector** | `K8SCluster` with access to the target cluster |
| **RBAC** | `apps deployments/scale`, `apps statefulsets/scale`, `apps replicasets/scale` (get, update); `v1 namespaces` (list, get) for namespace discovery |
| **Await Timeout** | Default: 5 minutes |

### Limitations

- Only works with resources that implement the Kubernetes **scale subresource** API.
- Namespace-scoped only — does not work with cluster-scoped resources.
- The executor does not check Pod readiness during wakeup; it relies on the workload controller's reconciliation.
- Custom CRDs require the `group/version/resource` format in `includedGroups`.

---

## NoOp

**Type:** `noop` · **Connector:** `CloudProvider` or `K8SCluster` (either works)

A **testing executor** that simulates hibernation operations without touching any real resources. Useful for validating schedules, execution strategies, DAG dependencies, and error recovery flows.

### Shutdown Flow

1. Simulates work with a random delay between 0 and `randomDelaySeconds`.
2. If `failureMode` is `"shutdown"` or `"both"`, returns a simulated error with the configured `failureMessage`.
3. Otherwise, generates restore data (parameters, timestamp, UUID) and returns success.

### Wakeup Flow

1. Simulates work with the same random delay.
2. If `failureMode` is `"wakeup"` or `"both"`, returns a simulated error.
3. Otherwise, returns success.

### Parameters

| Parameter | Default | Description |
|-----------|---------|-------------|
| `randomDelaySeconds` | 1 | Maximum random delay (0–30 seconds) |
| `failureMode` | `"none"` | When to fail: `"none"`, `"shutdown"`, `"wakeup"`, `"both"` |
| `failureMessage` | *(auto)* | Custom error message for simulated failures |

### Use Cases

- Test scheduling logic without cloud credentials
- Validate DAG dependency ordering
- Test execution strategies (Sequential, Parallel, DAG, Staged)
- Simulate error recovery and manual retry workflows
- CI/CD integration tests

---

## GKE

**Type:** `gke` · **Connector:** `K8SCluster`

!!! warning "Under Construction"
    The GKE executor is **not yet implemented**. The codebase contains a placeholder that validates parameters but does not make actual GCP API calls. Do not use in production.

**Planned behavior:** Manage GKE node pool scaling via the GCP Container API, similar to how the EKS executor manages managed node groups.

### Planned Parameters

| Parameter | Description |
|-----------|-------------|
| `nodePools` | List of GKE node pool names to hibernate (required) |

---

## CloudSQL

**Type:** `cloudsql` · **Connector:** `CloudProvider` (GCP)

!!! warning "Under Construction"
    The Cloud SQL executor is **not yet implemented**. The codebase contains a placeholder that validates parameters but does not make actual GCP API calls. Do not use in production.

**Planned behavior:** Stop and start Cloud SQL instances via the Cloud SQL Admin API, similar to how the RDS executor manages database instances.

### Planned Parameters

| Parameter | Description |
|-----------|-------------|
| `instanceName` | Cloud SQL instance name (required) |
| `project` | GCP project ID (required) |

---

## Choosing an Executor

| I want to hibernate... | Use executor | Notes |
|------------------------|-------------|-------|
| EKS managed node groups | `eks` | Scales to zero; cluster stays up |
| Karpenter NodePools | `karpenter` | Deletes and recreates pools |
| Standalone EC2 instances | `ec2` | Stops/starts; excludes ASG-managed |
| RDS databases | `rds` | Supports instances, clusters, and pre-stop snapshots |
| Kubernetes Deployments/StatefulSets | `workloadscaler` | Scales replicas to zero |
| Argo Rollouts or other CRDs | `workloadscaler` | Use `group/version/resource` format in `includedGroups` |
| GKE node pools | `gke` | :construction: Not yet implemented |
| Cloud SQL instances | `cloudsql` | :construction: Not yet implemented |

For the full parameter schema of each executor, see the [Executor Parameters Reference](../reference/executor-parameters.md).

**Operational Guides:**

- [EKS Executor](../user-guides/eks-executor.md)
- [Karpenter Executor](../user-guides/karpenter-executor.md)
- [EC2 Executor](../user-guides/ec2-executor.md)
- [RDS Executor](../user-guides/rds-executor.md)
- [WorkloadScaler Executor](../user-guides/workloadscaler-executor.md)
- [NoOp Executor](../user-guides/noop-executor.md)
