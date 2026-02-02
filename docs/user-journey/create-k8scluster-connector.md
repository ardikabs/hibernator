# Create K8SCluster Connector

**Tier:** `[MVP]`

**Personas:** Cluster Operator, DevOps Engineer, Platform Engineer

**When:** Setting up access to Kubernetes clusters (EKS, GKE, or on-prem)

**Why:** Hibernator needs Kubernetes API access to manage workloads, scale node groups, and coordinate cluster-level hibernation.

---

## User Stories

**Story 1:** As a **Cluster Operator**, I want to **create a K8SCluster CR that provides Hibernator access to my Kubernetes cluster**, so that **it can manage workloads and node groups**.

---

## When/Context

- **Cluster discovery:** Hibernator knows how to connect to each target cluster
- **Flexibility:** Support EKS (with programmatic token generation), GKE, and generic Kubernetes
- **Separation of concerns:** Cluster config separate from workload targets
- **Multi-cluster:** Single Hibernator instance can manage many clusters

---

## Business Outcome

Create a `K8SCluster` CR that securely stores Kubernetes cluster configuration and authentication details for hibernation operations.

---

## Step-by-Step Flow

### 1. **Determine cluster type**

Identify which Kubernetes platform you're using:

```
EKS (AWS) → Use spec.eks (recommended)
GKE (GCP) → Use spec.eks alternative or spec.k8s
Generic (on-prem, local) → Use spec.k8s
```

### 2. **For EKS: Create K8SCluster with programmatic token generation**

Hibernator generates EKS tokens automatically from CloudProvider credentials.

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: K8SCluster
metadata:
  name: prod-eks-1
  namespace: hibernator-system
spec:
  providerRef:
    kind: CloudProvider
    name: aws-prod            # Reference to CloudProvider CR
  eks:
    name: prod-eks-1          # EKS cluster name
    region: us-east-1
```

**How it works:**
1. Hibernator loads CloudProvider with AWS credentials
2. Calls EKS DescribeCluster API → Gets endpoint + CA
3. Generates STS presigned token programmatically (no external binaries)
4. Builds kubeconfig on-the-fly
5. No kubeconfig Secret needed!

### 3. **For generic Kubernetes: Create kubeconfig Secret**

If not using EKS, store kubeconfig in a Secret:

```bash
# Get kubeconfig from your cluster admin
# Example: export from ~/.kube/config or cloud console

# Store in Secret
kubectl create secret generic kubeconfig-prod-gke \
  --from-file=kubeconfig=/path/to/kubeconfig.yaml \
  -n hibernator-system
```

Then create K8SCluster reference:

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: K8SCluster
metadata:
  name: prod-gke-1
  namespace: hibernator-system
spec:
  k8s:
    kubeconfigRef:
      name: kubeconfig-prod-gke
      namespace: hibernator-system
```

### 4. **For in-cluster access**

If runner runs in the same cluster as target, use in-cluster service account:

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: K8SCluster
metadata:
  name: self-cluster
  namespace: hibernator-system
spec:
  k8s:
    inCluster: true  # Use /var/run/secrets/kubernetes.io/serviceaccount
```

### 5. **Verify connector is ready**

```bash
kubectl get k8sclusters prod-eks-1

# Check status:
kubectl describe k8scluster prod-eks-1
# Should show: Status = Ready

# If not ready, check events:
kubectl describe k8scluster prod-eks-1 | grep Events -A 10
```

### 6. **Test connectivity**

Create a test plan to verify cluster access:

```bash
kubectl apply -f - <<EOF
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernationPlan
metadata:
  name: k8s-connectivity-test
  namespace: hibernator-system
spec:
  schedule:
    timezone: "UTC"
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["MON"]
  execution:
    strategy:
      type: Sequential
  behavior:
    mode: BestEffort
  targets:
    - name: test-workloads
      type: workloadscaler  # Generic workload scaler
      connectorRef:
        kind: K8SCluster
        name: prod-eks-1
      parameters:
        namespace:
          literals: ["test-namespace"]
        workloadSelector:
          matchLabels:
            test: "true"
EOF

# Monitor Job:
kubectl get jobs -l hibernator/plan=k8s-connectivity-test -w
kubectl logs job/$(kubectl get jobs -l hibernator/plan=k8s-connectivity-test -o name) -f
```

---

## Decision Branches

| Decision | Option | Notes |
| --- | --- | --- |
| **EKS or generic K8s?** | EKS (if AWS) | Automatic token generation; simplest |
| | Generic (kubeconfig) | Works anywhere; requires kubeconfig management |
| | In-cluster | Runner and target are same cluster; simplest auth |
| **EKS authentication?** | IRSA (CloudProvider) | Automatic credential rotation |
| | Static keys (CloudProvider) | Manual rotation |
| **Kubeconfig storage?** | Secret (K8sCluster) | Simple; vulnerable if Secret leaked |
| | External key management | Advanced; HSM or vault integration |

---

## Outcome

✓ K8SCluster connector created and tested; HibernationPlan can now use this cluster for workload hibernation and node group management.

---

## Related Journeys

- [Create CloudProvider Connector](create-cloudprovider-connector.md) — Set up cloud credentials (for EKS)
- [Setup IRSA Authentication](setup-irsa-authentication.md) — Secure AWS credential handling (for EKS)
- [Hibernation Plan Initial Design](hibernation-plan-initial-design.md) — Reference K8SCluster in targets

---

## Pain Points Solved

**RFC-0001:** EKS authentication simplified with programmatic token generation (no kubeconfig Secret needed). Supports multiple cluster platforms (EKS, GKE, on-prem).

---

## RFC References

- **RFC-0001:** Control Plane + Runner Model (K8SCluster CRD, EKS token generation, kubeconfig management)
