# Deploy Operator to Cluster

**Tier:** `[MVP]`

**Personas:** DevOps Engineer, Platform Engineer, Cluster Operator

**When:** Setting up Hibernator Operator for the first time on a Kubernetes cluster

**Why:** Establish the control plane that orchestrates hibernation and restoration of resources.

---

## User Stories

**Story 1:** As a **DevOps Engineer**, I want to **install Hibernator Operator using Helm**, so that **deployment is repeatable and production-ready with sane defaults**.

**Story 2:** As a **Platform Engineer**, I want to **verify the operator is healthy and ready**, so that **I can confidently begin creating HibernatePlans**.

**Story 3:** As a **Cluster Operator**, I want to **configure RBAC and webhooks**, so that **I can enforce validation and prevent misconfigured plans from being applied**.

---

## When/Context

- **One-time setup:** Foundation for all hibernation functionality
- **Production-ready:** Helm chart, RBAC, and certificates properly configured
- **Operational confidence:** Leader election, health checks, monitoring ready

---

## Business Outcome

Install and configure Hibernator Operator so it can begin managing hibernation of cloud resources according to schedules.

---

## Step-by-Step Flow

### 1. **Add Hibernator Helm repository**

```bash
helm repo add hibernator https://charts.hibernator.ardikabs.com
helm repo update
```

### 2. **Create namespace**

```bash
kubectl create namespace hibernator-system
```

### 3. **Configure values**

Create a `values-override.yaml` with your settings:

```yaml
# values-override.yaml
controller:
  replicas: 2              # HA setup
  image:
    repository: ghcr.io/ardikasaputro/hibernator/controller
    tag: v0.1.0
  resources:
    requests:
      cpu: 100m
      memory: 256Mi
    limits:
      cpu: 500m
      memory: 512Mi

webhook:
  enabled: true
  certManager:
    enabled: true          # Use cert-manager for TLS

serviceAccount:
  create: true
  name: hibernator-controller

# IRSA (IAM Roles for Service Accounts) for AWS
irsa:
  enabled: true
  roleArn: arn:aws:iam::123456789012:role/hibernator-operator

runner:
  image:
    repository: ghcr.io/ardikasaputro/hibernator/runner
    tag: v0.1.0
  serviceAccount:
    name: hibernator-runner

metrics:
  enabled: true
  port: 8080
```

### 4. **Install Hibernator via Helm**

```bash
helm install hibernator hibernator/hibernator \
  --namespace hibernator-system \
  --values values-override.yaml
```

### 5. **Verify installation**

```bash
# Check pods are running
kubectl get pods -n hibernator-system
# Output:
# NAME                                      READY   STATUS
# hibernator-controller-xyz...              1/1     Running
# hibernator-webhook-abc...                 1/1     Running

# Check CRDs installed
kubectl get crds | grep hibernator
# Output:
# hibernateplans.hibernator.ardikabs.com
# cloudproviders.hibernator.ardikabs.com
# k8sclusters.hibernator.ardikabs.com

# Check webhook is ready
kubectl get validatingwebhookconfigurations | grep hibernator
```

### 6. **Verify controller is healthy**

```bash
# Check logs
kubectl logs -n hibernator-system deployment/hibernator-controller -f

# Expected output:
# [controller] Starting manager
# [controller] Leader election enabled
# [controller] Webhooks configured
# [controller] Ready to serve
```

### 7. **Test with sample HibernationPlan**

Create a simple test plan:

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernationPlan
metadata:
  name: test-hibernation
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
  behavior:
    mode: BestEffort
  targets: []  # Empty targets to test basic functionality
```

```bash
kubectl apply -f test-hibernation.yaml

# Check if accepted (webhook validation)
kubectl describe hibernateplan test-hibernation
# Should show: Status = Active, no validation errors
```

---

## Decision Branches

| Decision | Option | Notes |
| --- | --- | --- |
| **HA setup?** | Yes (2+ replicas) | Recommended for production; leader election handles coordination |
| | No (1 replica) | Sufficient for dev/test; simpler debugging |
| **Use IRSA or static creds?** | IRSA (recommended) | Automatic credential rotation; no secret distribution |
| | Static AWS keys | Simpler setup; requires Secret management |
| **Enable cert-manager?** | Yes (recommended) | Automatic TLS cert renewal |
| | Manual certificates | You manage renewal; higher operational burden |
| **Enable monitoring?** | Yes (recommended) | Prometheus metrics for observability |
| | No | Minimal; acceptable if using external monitoring |

---

## Outcome

✓ Hibernator Operator successfully deployed and healthy; ready to execute HibernatePlans.

---

## Related Journeys

- [Create CloudProvider Connector](create-cloudprovider-connector.md) — Set up AWS/GCP/Azure credentials
- [Create K8SCluster Connector](create-k8scluster-connector.md) — Configure target cluster access
- [Setup IRSA Authentication](setup-irsa-authentication.md) — Secure AWS credential handling
- [Configure RBAC for Hibernation](configure-rbac-for-hibernation.md) — Control access to hibernation

---

## Pain Points Solved

**RFC-0001:** Operator deployment simplified with Helm chart; RBAC and webhooks pre-configured.

---

## RFC References

- **RFC-0001:** Control Plane + Runner Model (operator deployment, RBAC, webhooks)
