# EKS Managed NodeGroup Hibernation

**Tier:** `[MVP]`

**Personas:** Platform Engineer, SRE

---

## User Stories

**Story 1:** As a **Platform Engineer**, I want to **automatically scale EKS managed node groups to zero during off-hours**, so that **I can reduce compute costs without losing node group configuration**.

---

## When/Context

- Running EKS clusters with managed node groups that don't need 24/7 availability
- Need cost optimization while preserving ASG configuration and capacity settings
- Note: For Karpenter NodePools, use the separate `karpenter` executor type

---

## Business Outcome

Automatically scale EKS managed node groups from current capacity to zero during off-hours, then restore to previous capacity during business hours.

---

## When/Why This Journey Matters

- **Cost savings:** Managed node groups can scale to 0 during nights/weekends
- **Simplicity:** Unlike Karpenter (which requires node deletion), managed groups maintain scaling group config
- **Predictability:** ASG scaling is well-understood and reliable
- **Safety:** No data loss; pods evict gracefully before scale-down
- **Scope clarity:** EKS executor focused on managed node groups only

---

## Step-by-Step Flow

### 1. **Identify managed node groups to hibernate**

List all EKS managed node groups you want to scale down. Example:

```bash
# Get groups from your EKS cluster
aws eks describe-nodegroup \
  --cluster-name prod-eks-1 \
  --nodegroup-name default-ng
# Output: DesiredSize: 3, MinSize: 1, MaxSize: 5
```

### 2. **Configure in HibernatePlan**

Add an EKS target with `nodeGroups` configuration:

```yaml
targets:
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
```

**Key parameters:**
- **`clusterName`**: The EKS cluster name (required)
- **`nodeGroups[]`**: List of managed node group names to scale (optional; if empty, all groups in cluster are targeted)
  - Each entry has only `name` field

**Note:** If you need to hibernate Karpenter NodePools, use a separate target with `type: karpenter`:

```yaml
targets:
  - name: karpenter-compute
    type: karpenter
    connectorRef:
      kind: K8SCluster
      name: prod-eks-1
    parameters:
      nodePools:
        - default
        - gpu
```

### 3. **Understand what happens at hibernation**

When the schedule triggers hibernation:

1. **Managed node groups scaled to zero** → ASG DesiredSize = 0 (MinSize/MaxSize unchanged)
2. **Pods terminated** (no nodes available)
3. **Restore metadata saved**: Current DesiredSize captured for wakeup

Example state transitions:

```
Before hibernation:
  ManagedNodeGroup: DesiredSize=3, MinSize=1, MaxSize=5

During hibernation:
  ManagedNodeGroup: DesiredSize=0, MinSize=1, MaxSize=5
  (MinSize/MaxSize not modified; only DesiredSize scaled)

After wakeup:
  ManagedNodeGroup: DesiredSize=3, MinSize=1, MaxSize=5
  (Restored from captured metadata)
```

### 4. **Verify cluster config**

Before deploying, verify:
- [ ] EKS cluster exists and is accessible
- [ ] Managed node groups are named correctly
- [ ] CloudProvider has AWS credentials for the account
- [ ] Karpenter NodePools use separate `karpenter` executor (not `eks`)

```bash
# Verify connector
kubectl get cloudproviders aws-prod

# Check EKS API is accessible
aws eks describe-cluster --name prod-eks-1 --region us-east-1

# List managed node groups
aws eks list-nodegroups --cluster-name prod-eks-1
```

### 5. **Monitor hibernation and wakeup**

**Hibernation (20:00):**

```bash
# Watch plan status
kubectl describe hibernateplan prod-offhours

# Status output:
# status.phase: Hibernating
# status.executions:
#   - target: eks/compute-cluster
#     state: InProgress
#     message: "Scaling managed node groups to 0"
```

**Wakeup (06:00):**

```bash
# Check restoration
kubectl describe hibernateplan prod-offhours

# Status output:
# status.phase: WakingUp
# status.executions:
#   - target: eks/compute-cluster
#     state: Completed
#     message: "Restored managed node groups to DesiredSize=3"

# Verify nodes coming online
kubectl get nodes -w  # Watch nodes coming up
```

---

## Decision Branches

| Decision | Option | Notes |
|----------|--------|-------|
| **Scale all groups or specific ones?** | Specific (list in `nodeGroups`) | If you want to hibernate only certain groups, list them by name. Empty `nodeGroups` hibernates all groups. |
| **What about Karpenter NodePools?** | Use separate `karpenter` executor | EKS executor only handles managed node groups. Create a separate target with `type: karpenter` for NodePool scaling. |
| **What if nodes won't scale down?** | Check for pod disruption budgets (PDBs) | High PDB limits may prevent scale-down; relax temporarily or schedule hibernation during quiet time |

---

## Outcome

✓ EKS managed node groups automatically scale to zero during off-hours and restore capacity during business hours.

---

## Related Journeys

- [hibernation-plan-initial-design.md](hibernation-plan-initial-design.md) — Overview of plan structure
- [monitor-hibernation-execution.md](monitor-hibernation-execution.md) — Track execution status
- [troubleshoot-hibernation-failure.md](troubleshoot-hibernation-failure.md) — Debug node group issues

---

## Pain Points Solved

**RFC-0001:** Manual EKS scaling via console or scripts is error-prone. HibernationPlan automates with auditable execution.

---

## RFC References

- **RFC-0001:** Control Plane + Runner Model (EKS executor, restore metadata)
