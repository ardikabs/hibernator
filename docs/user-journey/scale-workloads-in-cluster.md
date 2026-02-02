# Scale Workloads in Cluster

**Tier:** `[Enhanced]`

**Personas:** Platform Engineer, SRE

**When:** Hibernating in-cluster workloads (Deployments, StatefulSets, etc.) to save compute costs

**Why:** Downscaling workloads reduces Kubernetes pod resource usage during off-hours when services aren't needed.

---

## User Stories

**Story 1:** As a **Platform Engineer**, I want to **downscale in-cluster workloads during hibernation**, so that **I can save compute costs on Kubernetes pods**.

---

## When/Context

- **In-cluster resources:** Pods, Deployments, StatefulSets that run inside Kubernetes
- **Replica management:** Scale from N replicas to 0 during hibernation, restore to N during wakeup
- **Restore accuracy:** Capture current replica counts before scaling to zero
- **Multi-workload:** Downscale multiple workload types with single target

---

## Business Outcome

Automatically downscale in-cluster workloads to zero during hibernation, reducing compute costs and node requirements. Restore to original replica counts during wakeup.

---

## Step-by-Step Flow

### 1. **Plan your workload targets**

Identify which Kubernetes resources to downscale:

```yaml
# All Deployments in app-* namespaces
includedGroups:
  - Deployment          # Built-in: apps/v1/deployments
  - StatefulSet         # Built-in: apps/v1/statefulsets
  - ReplicaSet          # Built-in: apps/v1/replicasets

namespace:
  literals:             # Literal namespace list (preferred)
    - app-payment
    - app-shipping
    - app-inventory
  # OR:
  # selector:           # Namespace label selector (mutually exclusive)
  #   environment: staging

workloadSelector:       # Label selector for workloads within namespaces
  matchLabels:
    app.kubernetes.io/part-of: "payments"
  # matchExpressions:
  #   - key: tier
  #     operator: In
  #     values: ["backend", "worker"]
```

### 2. **Create HibernationPlan with workloadscaler target**

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernationPlan
metadata:
  name: app-hibernation
  namespace: hibernator-system
spec:
  schedule:
    timezone: "UTC"
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]

  execution:
    strategy:
      type: Sequential

  targets:
    - name: downscale-apps
      type: workloadscaler
      connectorRef:
        kind: K8SCluster
        name: app-cluster
      parameters:
        includedGroups:
          - Deployment
          - StatefulSet
        namespace:
          literals:
            - app-payment
            - app-shipping
            - app-inventory
        workloadSelector:
          matchLabels:
            app.kubernetes.io/part-of: "payments"
```

### 3. **Verify targets will be discovered**

Dry-run to preview which workloads will be downscaled:

```bash
kubectl apply -f hibernation-plan.yaml --dry-run=client

# Check what workloads match the selector:
kubectl get deployments,statefulsets \
  -l app.kubernetes.io/part-of=payments \
  -n app-payment,app-shipping,app-inventory

# Output:
# NAMESPACE       NAME                         READY   UP-TO-DATE   AVAILABLE   AGE
# app-payment     deployment.apps/api          3/3     3            3           30d
# app-payment     deployment.apps/worker       2/2     2            2           30d
# app-shipping    deployment.apps/scheduler    1/1     1            1           10d
# (These will be downscaled to 0)
```

### 4. **During hibernation: workloads scale to 0**

When hibernation starts (20:00):

```bash
# Before hibernation:
kubectl get deployments -n app-payment
# NAME       READY   UP-TO-DATE   AVAILABLE   AGE
# api        3/3     3            3           30d
# worker     2/2     2            2           30d

# At 20:00 (hibernation starts):
# Controller creates Job to run workloadscaler executor
# Executor reads current replica counts → Saves restore data
# Executor scales to 0

# After 1 minute:
kubectl get deployments -n app-payment
# NAME       READY   UP-TO-DATE   AVAILABLE   AGE
# api        0/0     0            0           30d  ← SCALED DOWN
# worker     0/0     0            0           30d  ← SCALED DOWN

# Pods terminate:
kubectl get pods -n app-payment
# No pods running (all scaled to 0)
```

### 5. **Monitor restore data**

Restore data captures original replica counts:

```bash
# View restore metadata
kubectl get cm restore-data-app-hibernation -o yaml

# Output:
# data:
#   workloadscaler_downscale-apps: |
#     {
#       "items": [
#         {
#           "group": "apps",
#           "kind": "Deployment",
#           "namespace": "app-payment",
#           "name": "api",
#           "replicas": 3
#         },
#         {
#           "group": "apps",
#           "kind": "Deployment",
#           "namespace": "app-payment",
#           "name": "worker",
#           "replicas": 2
#         },
#         {
#           "group": "apps",
#           "kind": "Deployment",
#           "namespace": "app-shipping",
#           "name": "scheduler",
#           "replicas": 1
#         }
#       ]
#     }
```

### 6. **During wakeup: workloads restore**

When hibernation ends (06:00):

```bash
# Runner loads restore data from ConfigMap
# Executor scales workloads back to original replica counts

# After 2 minutes:
kubectl get deployments -n app-payment
# NAME       READY   UP-TO-DATE   AVAILABLE   AGE
# api        3/3     3            3           30d  ← RESTORED TO 3
# worker     2/2     2            2           30d  ← RESTORED TO 2

# Pods reschedule:
kubectl get pods -n app-payment
# api-xyz        Running    (1/3 ready)
# api-abc        Running    (2/3 ready)
# api-def        Running    (3/3 ready)
# worker-123     Running    (1/2 ready)
# worker-456     Running    (2/2 ready)
```

---

## Advanced: Custom CRD Support

For custom CRDs (e.g., ArgoCD Rollouts):

```yaml
targets:
  - name: downscale-rollouts
    type: workloadscaler
    connectorRef:
      kind: K8SCluster
      name: app-cluster
    parameters:
      includedGroups:
        - argoproj.io/v1alpha1/rollouts  # Custom CRD format: group/version/resource
        - helm.fluxcd.io/v2beta1/helmreleases
      namespace:
        literals:
          - app-payment
      workloadSelector:
        matchLabels:
          hibernatable: "true"
```

---

## Decision Branches

| Decision | Option | Notes |
| --- | --- | --- |
| **Which resources?** | Deployments only | Simplest, covers 95% of workloads |
| | Deployments + StatefulSets | Include stateful applications |
| | Custom CRDs | Rollouts, HelmReleases, custom apps |
| **Namespace selection?** | Literal list (recommended) | Explicit; less surprise |
| | Label selector | Dynamic; namespaces tagged `env=staging` |
| **Workload filtering?** | Label selector | Select by app, tier, etc. |
| | None (all in namespace) | Everything in namespace |

---

## Outcome

✓ Workloads downscaled to 0 during hibernation. Original replica counts restored during wakeup.

---

## Related Journeys

- [EKS Managed Node Group Hibernation](eks-managed-nodegroup-hibernation.md) — Also scale EKS nodes
- [Monitor Hibernation Execution](monitor-hibernation-execution.md) — Track scaling progress

---

## Pain Points Solved

**RFC-0004:** Scale subresource executor provides unified downscaling for any workload type (Deployment, StatefulSet, custom CRDs) with accurate replica tracking and automatic restoration.

---

## RFC References

- **RFC-0004:** Scale Subresource Executor for Workload Downscaling (workloadscaler target, replica tracking, restore metadata)
