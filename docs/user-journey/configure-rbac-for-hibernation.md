# Configure RBAC for Hibernation

**Tier:** `[Enhanced]`

**Personas:** DevOps Engineer, Platform Engineer, Security Officer

**When:** Setting up multi-team Kubernetes environment where different teams manage different hibernation plans

**Why:** RBAC controls who can create, view, and modify hibernation configurations, preventing unauthorized schedule changes.

---

## User Stories

**Story 1:** As a **DevOps Engineer**, I want to **create separate namespaces and RBAC roles per team**, so that **each team manages only their own hibernation plans**.

**Story 2:** As a **Security Officer**, I want to **enforce least-privilege access to CloudProvider and K8SCluster connectors**, so that **teams cannot access credentials for other environments**.

**Story 3:** As a **Platform Engineer**, I want to **grant exception approval permissions to managers**, so that **oncall engineers can request overrides but managers control acceptance**.

---

## When/Context

- **Multi-team separation:** Platform, App-A, App-B teams have separate hibernation plans
- **Least privilege:** Teams can only modify their own plans
- **Audit trail:** All RBAC-protected actions logged via Kubernetes audit
- **Service account isolation:** Runner pods use dedicated SA per plan

---

## Business Outcome

Enforce RBAC policies so different teams can safely manage hibernation without interfering with each other.

---

## Step-by-Step Flow

### 1. **Understand Hibernator RBAC resources**

Hibernator exposes these CRDs:
- `hibernateplans.hibernator.ardikabs.com` — Main hibernation intent
- `cloudproviders.hibernator.ardikabs.com` — Cloud credentials
- `k8sclusters.hibernator.ardikabs.com` — Cluster access

RBAC can control:
- `get`, `list`, `watch` — Read-only
- `create`, `update`, `patch` — Modify plans
- `delete` — Remove plans
- `approve` — Accept exceptions (custom action)

### 2. **Create namespace per team**

```bash
# Each team gets a namespace
kubectl create namespace hibernator-platform
kubectl create namespace hibernator-team-a
kubectl create namespace hibernator-team-b

# Each namespace has its own plans, connectors, etc.
```

### 3. **Create team-specific Role**

Team can read/write their own plans:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: hibernator-admin
  namespace: hibernator-team-a
rules:
  # HibernationPlans: full access
  - apiGroups: ["hibernator.ardikabs.com"]
    resources: ["hibernateplans"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]

  # CloudProviders: view only (managed by platform team)
  - apiGroups: ["hibernator.ardikabs.com"]
    resources: ["cloudproviders"]
    verbs: ["get", "list", "watch"]

  # K8SClusters: view only (managed by platform team)
  - apiGroups: ["hibernator.ardikabs.com"]
    resources: ["k8sclusters"]
    verbs: ["get", "list", "watch"]

  # ConfigMaps: access restore data (readonly)
  - apiGroups: [""]
    resources: ["configmaps"]
    verbs: ["get", "list", "watch"]
    resourceNames: ["restore-data-*"]  # Only restore data CMs
```

### 4. **Create platform team admin Role**

Platform team manages all connectors and shared resources:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: hibernator-platform-admin
rules:
  # All access to all Hibernator resources
  - apiGroups: ["hibernator.ardikabs.com"]
    resources: ["hibernateplans", "hibernateplans/status"]
    verbs: ["*"]

  - apiGroups: ["hibernator.ardikabs.com"]
    resources: ["cloudproviders", "k8sclusters"]
    verbs: ["*"]

  # ConfigMaps for restore data
  - apiGroups: [""]
    resources: ["configmaps"]
    verbs: ["*"]
    resourceNames: ["restore-data-*"]
```

### 5. **Bind roles to service accounts or groups**

**Team developers:**

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: hibernator-admin
  namespace: hibernator-team-a
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: hibernator-admin
subjects:
  # GitHub org team
  - kind: Group
    name: "hibernator:team-a"
    apiGroup: rbac.authorization.k8s.io

  # Service account for CI/CD
  - kind: ServiceAccount
    name: team-a-ci
    namespace: hibernator-team-a
```

**Platform team:**

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: hibernator-platform-admin
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: hibernator-platform-admin
subjects:
  - kind: Group
    name: "hibernator:platform-team"
    apiGroup: rbac.authorization.k8s.io
```

### 6. **Test RBAC policies**

```bash
# As team-a developer, create a plan
kubectl create -f hibernateplan-team-a.yaml -n hibernator-team-a
# ✓ Success

# Try to access team-b's plan (should fail)
kubectl get hibernateplans -n hibernator-team-b
# Error: hibernateplans.hibernator.ardikabs.com is forbidden: User "alice" cannot list resource "hibernateplans" in API group "hibernator.ardikabs.com" in the namespace "hibernator-team-b"

# As platform team, manage connectors
kubectl get cloudproviders
# ✓ Success
```

### 7. **Enable audit logging (optional)**

Track all RBAC decisions:

```bash
# View audit logs
kubectl logs -n kube-system pod/etcd-master | grep hibernator | grep forbidden

# Example:
# {"verb":"get","objectRef":{"resource":"hibernateplans"...},"user":{"username":"alice"},"sourceIPs":["10.0.0.1"],"requestReceivedTimestamp":"...","outcome":"Failure"}
```

---

## Advanced: Exception Approvals

Separate RBAC for exception approvals:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: hibernator-approver
  namespace: hibernator-team-a
rules:
  # Approve exceptions (custom subresource action)
  - apiGroups: ["hibernator.ardikabs.com"]
    resources: ["hibernateplans/exceptions/approve"]
    verbs: ["create"]

  # View exceptions (not edit base plan)
  - apiGroups: ["hibernator.ardikabs.com"]
    resources: ["hibernateplans"]
    verbs: ["get", "list", "watch"]
```

---

## Decision Branches

| Decision | Option | Notes |
| --- | --- | --- |
| **Namespace scope?** | Per-team namespace | Best isolation; each team owns their namespace |
| | Shared namespace (labels) | Simpler; RBAC uses resourceNames |
| **Connector access?** | Platform team only | Centralized; teams reference shared connectors |
| | Per-team connectors | Flexibility; higher credential management burden |

---

## Outcome

✓ RBAC configured. Teams can manage their own hibernation plans. Cross-team interference prevented. Audit trail available.

---

## Related Journeys

- [Integrate with GitOps](integrate-with-gitops.md) — GitOps + RBAC combined
- [Deploy Operator to Cluster](deploy-operator-to-cluster.md) — Initial operator setup

---

## Pain Points Solved

**RFC-0001:** RBAC controls prevent accidental schedule changes and provide audit trail for compliance.

---

## RFC References

- **RFC-0001:** Control Plane + Runner Model (RBAC, runner ServiceAccount isolation, audit logging)
