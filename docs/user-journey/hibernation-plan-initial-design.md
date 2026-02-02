# Hibernation Plan Initial Design

**Tier:** `[MVP]`

**Personas:** Platform Engineer, DevOps Engineer, SRE

---

## User Stories

**Story 1:** As a **Platform Engineer**, I want to **design a hibernation strategy with explicit dependencies**, so that **resources are shut down in the correct order to prevent data corruption**.

**Story 2:** As a **DevOps Engineer**, I want to **declare the full hibernation intent in YAML**, so that **I can version-control it and apply it consistently across environments**.

**Story 3:** As an **SRE**, I want to **define decision branches and alternative execution paths**, so that **I can handle both in-cluster workloads and cloud infrastructure in one plan**.

---

## When/Context

- Project kickoff, new environment setup, or expanding hibernation coverage
- Need to coordinate shutdown and restoration across EKS clusters, RDS databases, and EC2 instances
- Existing CronJob scripts are fragile and lack dependency management

---

## Business Outcome

Design and deploy a `HibernatePlan` that reliably hibernates and restores multiple cloud resources according to a schedule, with explicit ordering to prevent data corruption and cost optimization across environments.

---

## When/Why This Journey Matters

- **Cost reduction:** Unneeded resources can be shut down during predictable off-hours (e.g., night hours, weekends)
- **Coordination:** Multi-resource hibernation must respect dependencies (e.g., shut down clusters *before* databases)
- **Observability:** Centralized status tracking replaces ad-hoc CronJob logs
- **Governance:** Declarative YAML fits GitOps workflows

---

## Step-by-Step Flow

### 1. **Define hibernation schedule**

Determine when resources should be hibernated and awake. Use human-readable time windows with timezone awareness.

```yaml
schedule:
  timezone: "America/New_York"  # or your region: Asia/Jakarta, Europe/London, etc.
  offHours:
    - start: "20:00"            # 8 PM
      end: "06:00"              # 6 AM next day
      daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]  # Weekdays only
```

**Decision branches:**
- Should hibernation include weekends? (No for business hours, Yes for lab/dev)
- Multiple sleep windows? (e.g., night + lunch break) → Create multiple `offHours` entries
- Timezone for your team's office or cloud region? → Choose one; can override via exception later

### 2. **Identify targets**

List all resources that should hibernate: clusters, databases, compute instances, workloads.

```yaml
targets:
  - name: database
    type: rds
    connectorRef:
      kind: CloudProvider
      name: aws-prod
    parameters:
      snapshotBeforeStop: true  # Optional: backup before shutdown

  - name: compute-cluster
    type: eks
    connectorRef:
      kind: CloudProvider
      name: aws-prod
    parameters:
      clusterName: prod-eks-1
      nodeGroups:
        - name: default-ng
        - name: gpu-ng

  - name: worker-instances
    type: ec2
    connectorRef:
      kind: CloudProvider
      name: aws-prod
    parameters:
      selector:
        tags:
          Hibernate: "true"
```

**Key concepts highlighted:**
- **`type`**: What to hibernate (rds, eks, ec2, workloadscaler, etc.)
- **`connectorRef`**: Where credentials come from (CloudProvider or K8SCluster CRs)
- **`parameters`**: Resource-specific options (e.g., snapshot RDS before stopping, which node pools to scale)

### 3. **Design execution strategy**

Choose how targets should be executed. Options: **Sequential**, **Parallel**, **DAG**, or **Staged**.

#### 3a. Sequential (Simplest)
Execute targets one at a time, in list order.
```yaml
execution:
  strategy:
    type: Sequential
```
**When to use:** Small plans, simple ordering, error debugging needed.

#### 3b. Parallel (Fastest)
Execute all targets at once, with optional `maxConcurrency` limit.
```yaml
execution:
  strategy:
    type: Parallel
    maxConcurrency: 3  # Don't overwhelm API limits
```
**When to use:** Independent targets, many resources, cost-time tradeoff acceptable.

#### 3c. DAG (Safest for dependencies)
Explicit dependency graph. Execute targets respecting "must-complete-before" relationships.
```yaml
execution:
  strategy:
    type: DAG
    maxConcurrency: 2
    dependencies:
      - from: database
        to: compute-cluster
      - from: compute-cluster
        to: worker-instances
```
**When to use:** Complex dependencies, prevent data corruption, ensure correct ordering.

**This means:**
- Shutdown database first ✓
- Then shut down cluster ✓
- Then shut down EC2 ✓

#### 3d. Staged (Grouped parallelism)
Group targets into stages; execute stages sequentially, parallelize within stages.
```yaml
execution:
  strategy:
    type: Staged
  stages:
    - name: "storage"
      parallel: true
      targets: [database]
    - name: "compute"
      parallel: true
      maxConcurrency: 2
      targets: [compute-cluster, worker-instances]
```
**When to use:** Clear grouping (storage → compute), natural sequencing.

**Decision:**
- Do targets have dependencies? → Use **DAG**
- Simple linear order? → Use **Sequential**
- All independent? → Use **Parallel**
- Natural grouping? → Use **Staged**

### 4. **Choose behavior mode**

Decide how to handle partial failures.

```yaml
behavior:
  mode: Strict          # Fail entire plan if any target fails
  # OR
  mode: BestEffort      # Skip failed targets, continue others
```

**Decision:**
- Mission-critical resources? → `Strict` (fail fast, alert ops)
- Non-critical lab/dev? → `BestEffort` (maximize hibernation savings)

### 5. **Create HibernatePlan CRD**

Combine all decisions into a Kubernetes manifest:

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: prod-offhours
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
      type: DAG
      maxConcurrency: 2
      dependencies:
        - from: database
          to: compute-cluster
        - from: compute-cluster
          to: worker-instances

  behavior:
    mode: Strict

  targets:
    - name: database
      type: rds
      connectorRef:
        kind: CloudProvider
        name: aws-prod
      parameters:
        snapshotBeforeStop: true

    - name: compute-cluster
      type: eks
      connectorRef:
        kind: CloudProvider
        name: aws-prod
      parameters:
        clusterName: prod-eks-1
        nodeGroups:
          - name: default-ng
          - name: gpu-ng

    - name: worker-instances
      type: ec2
      connectorRef:
        kind: CloudProvider
        name: aws-prod
      parameters:
        selector:
          tags:
            Hibernate: "true"
```

### 6. **Validate with webhook**

Apply the manifest. The **validation webhook** checks:
- ✓ Schedule format (valid times, valid day names)
- ✓ DAG acyclicity (no circular dependencies)
- ✓ Target uniqueness (no duplicate names)
- ✓ All referenced connectors exist

```bash
kubectl apply -f hibernateplan.yaml
# Webhook validates automatically
```

### 7. **Deploy and monitor first cycle**

Once applied, controller:
1. Evaluates schedule at each reconciliation
2. When current time ∈ `offHours`, enters hibernation phase
3. Executes targets according to strategy
4. Updates `status.executions[]` ledger with per-target progress
5. When `offHours` window ends, enters wakeup phase
6. Restores resources using captured metadata

**Check status:**
```bash
kubectl get hibernateplan prod-offhours
kubectl describe hibernateplan prod-offhours  # See status.executions[]
```

---

## Decision Branches

| Decision | Option A | Option B | Option C |
|----------|----------|----------|----------|
| **Execution strategy?** | Sequential (simple, debug-friendly) | DAG (safe, complex) | Parallel (fast, risky) |
| **Behavior on failure?** | Strict (fail fast) | BestEffort (maximize coverage) | — |
| **Hibernation window?** | Weekday nights | Weekends | Both |
| **Include snapshots before shutdown?** | Yes (safer restore) | No (faster, less storage) | — |

---

## Outcome

✓ Deployed a declarative **HibernatePlan** that:
- Schedules hibernation/wakeup automatically
- Respects resource dependencies
- Tracks execution status in a central ledger
- Persists restore metadata for safe restoration
- Integrates with GitOps pipelines

---

## Related Journeys

- [eks-managed-nodegroup-hibernation.md](eks-managed-nodegroup-hibernation.md) — Deep dive on EKS-specific parameters
- [rds-database-hibernation.md](rds-database-hibernation.md) — RDS-specific configuration
- [ec2-instance-hibernation.md](ec2-instance-hibernation.md) — EC2 targeting and tagging
- [monitor-hibernation-execution.md](monitor-hibernation-execution.md) — Track execution status
- [integrate-with-gitops.md](integrate-with-gitops.md) — Version-control HibernatePlans

---

## Pain Points Solved

**RFC-0001:** Existing CronJob-style scripts lack dependency handling, restore metadata tracking, and observability. HibernationPlan provides a declarative, auditable alternative.

**RFC-0002:** Cron expressions are hard to understand. HibernationPlan uses human-readable `start/end/daysOfWeek` format instead.

**RFC-0004:** Ad-hoc per-resource shutdown logic is fragile. HibernationPlan delegates to pluggable executors.

---

## RFC References

- **RFC-0001:** Control Plane + Runner Model (core architecture, execution strategies)
- **RFC-0002:** User-Friendly Schedule Format (schedule validation, timezone support)
- **RFC-0004:** Scale Subresource Executor (workload downscaling option)
