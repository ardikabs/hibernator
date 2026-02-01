# Scale Hibernation Across Organizations

**Tier:** `[Advanced]`

**Personas:** Enterprise Administrator, Platform Engineering Lead, Multi-Org Architect

**When:** Deploying shared Hibernator infrastructure serving multiple organizations with per-team isolation

**Why:** Shared infrastructure reduces operational overhead and cost; per-team isolation maintains security and autonomy.

---

## User Stories

**Story 1:** As an **Enterprise Administrator**, I want to **deploy shared Hibernator infrastructure**, so that **multiple teams benefit from economies of scale**.

**Story 2:** As a **Security Officer**, I want to **enforce per-team isolation via RBAC and namespace boundaries**, so that **teams cannot access or modify other teams' plans**.

**Story 3:** As a **Multi-Org Architect**, I want to **manage exceptions and approvals across teams**, so that **enterprise governance is enforced consistently**.

---

## When/Context

- **Multi-tenant environment:** Multiple independent teams/orgs
- **Shared control plane:** Single Hibernator Operator deployed centrally
- **Isolation requirement:** Teams cannot see or modify other teams' plans
- **Multi-cloud scale:** Managing 100s-1000s of resources across AWS/GCP/Azure

---

## Business Outcome

Deploy a single, multi-tenant Hibernator Operator that safely scales to enterprise scale with per-team isolation and governance.

---

## Step-by-Step Flow

### 1. **Architecture: shared control plane with isolated data planes**

```
┌─────────────────────────────────────────────────────┐
│  CENTRAL CLUSTER (hibernator-system namespace)     │
│  ┌───────────────────────────────────────────────┐  │
│  │ Hibernator Operator (shared control plane)   │  │
│  │  - Reconciler logic                          │  │
│  │  - Scheduler                                 │  │
│  │  - Status aggregation                        │  │
│  └───────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────┘
         │                    │                    │
         ├────────────────────┼────────────────────┤
         │                    │                    │
    ┌────┴────────┐    ┌─────┴──────┐    ┌───────┴────────┐
    │  TEAM-A     │    │  TEAM-B    │    │  TEAM-C        │
    │  Namespace  │    │  Namespace │    │  Namespace     │
    ├─────────────┤    ├────────────┤    ├────────────────┤
    │ Plans       │    │ Plans      │    │ Plans          │
    │ Connectors  │    │ Connectors │    │ Connectors     │
    │ Resources   │    │ Resources  │    │ Resources      │
    └─────────────┘    └────────────┘    └────────────────┘
```

### 2. **Create isolated namespaces per team**

```bash
# Namespace for team A
kubectl create namespace team-a-hibernator
kubectl label namespace team-a-hibernator \
  hibernator.io/team=team-a \
  hibernator.io/org=engineering

# Namespace for team B
kubectl create namespace team-b-hibernator
kubectl label namespace team-b-hibernator \
  hibernator.io/team=team-b \
  hibernator.io/org=data-science

# Namespace for team C
kubectl create namespace team-c-hibernator
kubectl label namespace team-c-hibernator \
  hibernator.io/team=team-c \
  hibernator.io/org=infrastructure
```

### 3. **Configure RBAC with per-team isolation**

```yaml
# team-a-role.yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: hibernator-team-lead
  namespace: team-a-hibernator
rules:
  # Can manage HibernationPlans in their namespace only
  - apiGroups: ["hibernator.ardikasaputro.io"]
    resources: ["hibernateplans", "cloudproviders", "k8sclusters"]
    verbs: ["create", "get", "list", "watch", "update", "patch", "delete"]
  # Can view status and events
  - apiGroups: [""]
    resources: ["events", "configmaps"]
    verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: hibernator-team-a-leads
  namespace: team-a-hibernator
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: hibernator-team-lead
subjects:
  - kind: Group
    name: "team-a-leads@company.com"
    apiGroup: rbac.authorization.k8s.io
```

### 4. **Tenant-aware operator configuration**

```yaml
# values.yaml for shared Hibernator
hibernator:
  multiTenant:
    enabled: true
    isolation: namespace  # or "label"

  # Operator watches only labeled namespaces
  watchNamespaceSelector:
    matchLabels:
      hibernator.io/team: "*"

  # Per-tenant limits
  tenantLimits:
    - team: team-a
      maxPlans: 20
      maxRunners: 10
      maxConcurrency: 5
    - team: team-b
      maxPlans: 50
      maxRunners: 20
      maxConcurrency: 10
```

### 5. **Multi-cloud connector per team**

```yaml
# team-a connectors in team-a-hibernator namespace
apiVersion: connector.hibernator.ardikasaputro.io/v1alpha1
kind: CloudProvider
metadata:
  name: aws-team-a
  namespace: team-a-hibernator
spec:
  type: aws
  aws:
    accountId: "111111111111"  # Team A AWS account
    region: us-east-1
    auth:
      serviceAccount: {}

---
apiVersion: connector.hibernator.ardikasaputro.io/v1alpha1
kind: CloudProvider
metadata:
  name: gcp-team-a
  namespace: team-a-hibernator
spec:
  type: gcp
  gcp:
    projectId: "team-a-project"
    region: us-central1
    auth:
      serviceAccount: {}
```

### 6. **Multi-account delegation via cross-account roles**

Each team's control account has IRSA to assume roles in target accounts:

```bash
# Team A: central account assumes dev/stg/prod roles
for ENV in dev stg prod; do
  aws iam put-role-policy \
    --role-name hibernator-operator \
    --policy-name assume-team-a-$ENV \
    --policy-document "{
      \"Statement\": [{
        \"Effect\": \"Allow\",
        \"Action\": \"sts:AssumeRole\",
        \"Resource\": \"arn:aws:iam::${TEAM_A_${ENV}_ACCOUNT}:role/hibernator-target\"
      }]
    }"
done
```

### 7. **Shared metrics and observability**

```yaml
# Prometheus job for multi-tenant scraping
scrape_configs:
  - job_name: hibernator-operator
    kubernetes_sd_configs:
      - role: pod
        namespaces:
          names:
            - hibernator-system
    relabel_configs:
      - source_labels: [__meta_kubernetes_pod_label_hibernator_io_team]
        target_label: team
      - source_labels: [__meta_kubernetes_namespace_name]
        target_label: namespace
```

Queries by team:

```promql
# Hibernation success rate by team
rate(hibernator_execution_success_total[5m]) by (team)

# Execution duration by team
histogram_quantile(0.95, hibernator_execution_duration_seconds_bucket) by (team)
```

### 8. **Central dashboard with per-team views**

```yaml
# Grafana dashboard showing all teams with filters
- title: Hibernation Overview (by Team)
  panels:
    - title: Active Plans by Team
      targets:
        - expr: count(hibernateplans) by (team)
    - title: Execution Success Rate by Team
      targets:
        - expr: rate(hibernator_execution_success_total[5m]) by (team)
    - title: Top Executors by Cost Savings
      targets:
        - expr: hibernator_cost_savings_total by (team)
```

### 9. **Chargeback model: track costs per team**

```bash
# Monitor resource usage per team
kubectl top nodes -l team=team-a
kubectl top pods -n team-a-hibernator

# Calculate hibernation ROI per team
hibernator_cost_before_hibernation_total{team="team-a"} -
hibernator_cost_after_hibernation_total{team="team-a"}
= cost savings per team
```

### 10. **Multi-environment scaling**

```yaml
# Separate HibernationPlans per environment per team
---
apiVersion: hibernator.ardikasaputro.io/v1alpha1
kind: HibernationPlan
metadata:
  name: team-a-dev-offhours
  namespace: team-a-hibernator
spec:
  schedule:
    timezone: "UTC"
    offHours:
      - start: "00:00"
        end: "23:59"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"]
  targets:
    - name: dev-cluster
      type: eks
      connectorRef:
        kind: CloudProvider
        name: aws-team-a
      parameters:
        clusterName: team-a-dev-eks
---
apiVersion: hibernator.ardikasaputro.io/v1alpha1
kind: HibernationPlan
metadata:
  name: team-a-prod-offhours
  namespace: team-a-hibernator
spec:
  schedule:
    timezone: "UTC"
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["SAT", "SUN"]
  targets:
    - name: prod-cluster
      type: eks
      connectorRef:
        kind: CloudProvider
        name: aws-team-a
      parameters:
        clusterName: team-a-prod-eks
```

### 11. **Cross-team admin dashboard**

```bash
# View all hibernation plans across organization
kubectl get hibernateplans -A

# Filter by team
kubectl get hibernateplans -l hibernator.io/team=team-a -A

# Check global execution status
kubectl get hibernateplans -A \
  -o jsonpath='{range .items[*]}{.metadata.namespace} {.metadata.name} {.status.phase}\n{end}'
```

### 12. **Compliance and audit per team**

```bash
# Audit all hibernation operations per team
kubectl get events -A \
  -l hibernator.io/team=team-a \
  --sort-by='.lastTimestamp'

# Check cost savings per team (from metrics)
kubectl get configmaps -A -l hibernator.io/team \
  -o jsonpath='{.items[*].data.cost_savings}'
```

---

## Multi-Tenant Scaling Limits

| Resource | Team-A | Team-B | Team-C |
| --- | --- | --- | --- |
| **Max Plans** | 20 | 50 | 30 |
| **Max Runners** | 10 | 20 | 15 |
| **Max Parallelism** | 5 | 10 | 8 |
| **Cost Budget** | $10k/mo | $50k/mo | $25k/mo |

---

## Decision Branches

| Decision | Option | Notes |
| --- | --- | --- |
| **Isolation?** | Namespace (recommended) | Strong; RBAC enforced by API server |
| | Label-based | Lighter; application-enforced |
| **Multi-cloud?** | Yes | All teams can use AWS/GCP/Azure |
| | Single cloud | Simpler; less flexibility |
| **Cost tracking?** | Per-team chargeback | Full accounting |
| | Shared pool | Simpler; less accountability |

---

## Outcome

✓ Shared Hibernator infrastructure deployed. 3 teams with isolated namespaces, RBAC, and multi-cloud access. Central metrics and chargeback enabled.

---

## Related Journeys

- [Configure RBAC for Hibernation](configure-rbac-for-hibernation.md) — RBAC setup per team
- [Setup Cross-Account Hibernation](setup-cross-account-hibernation.md) — Cross-account delegation
- [Setup IRSA Authentication](setup-irsa-authentication.md) — IRSA foundation

---

## Pain Points Solved

**RFC-0001:** Multi-tenant isolation via namespace-scoped CRDs and RBAC. Per-team connectors with cross-account AssumeRole enable secure delegation without credential sharing.

---

## RFC References

- **RFC-0001:** Control Plane + Runner Model (multi-tenant architecture, connector isolation, cross-account auth)
