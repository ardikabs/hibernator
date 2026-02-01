# Integrate with GitOps

**Tier:** `[Enhanced]`

**Personas:** DevOps Engineer, Platform Engineer, SRE

**When:** Adding HibernationPlans to version-controlled infrastructure repositories (ArgoCD, Flux, Helm)

**Why:** GitOps enables auditable, repeatable hibernation deployments with version control, approvals, and automated rollback.

---

## User Stories

**Story 1:** As a **DevOps Engineer**, I want to **organize HibernationPlans in Git repositories**, so that **all configurations have version history and code review**.

**Story 2:** As a **Platform Engineer**, I want to **automate deployment of hibernation changes via ArgoCD or Flux**, so that **manifests are consistently applied across clusters**.

**Story 3:** As a **Release Manager**, I want to **control HibernationPlan rollouts with promotion gates**, so that **prod hibernation changes are tested in staging first**.

---

## When/Context

- **Version control:** All hibernation plans in Git with full change history
- **Code review:** Hibernation changes require PR approvals before deployment
- **Consistency:** Same plans deployed across environments (dev, staging, prod)
- **Rollback:** Revert hibernation changes if needed
- **Automation:** No manual kubectl apply commands

---

## Business Outcome

Manage hibernation configurations as infrastructure-as-code with version control, audit trail, and deployment automation.

---

## Step-by-Step Flow

### 1. **Organize hibernation manifests in Git**

```
my-infrastructure-repo/
├── hibernator/
│   ├── namespaces/
│   │   └── hibernator-system.yaml
│   ├── cloudproviders/
│   │   ├── aws-prod.yaml
│   │   └── aws-staging.yaml
│   ├── k8sclusters/
│   │   ├── eks-prod.yaml
│   │   └── eks-staging.yaml
│   └── hibernateplans/
│       ├── prod-offhours.yaml
│       ├── staging-offhours.yaml
│       └── dev-offhours.yaml
└── kustomization.yaml
```

### 2. **Define CloudProvider with Git management**

```yaml
# hibernator/cloudproviders/aws-prod.yaml
apiVersion: connector.hibernator.ardikasaputro.io/v1alpha1
kind: CloudProvider
metadata:
  name: aws-prod
  namespace: hibernator-system
  labels:
    app.kubernetes.io/name: hibernator
    app.kubernetes.io/version: v0.1.0
    managed-by: gitops
spec:
  type: aws
  aws:
    accountId: "123456789012"
    region: us-east-1
    auth:
      serviceAccount: {}  # IRSA, not hardcoded credentials
```

### 3. **Define K8SCluster with Git management**

```yaml
# hibernator/k8sclusters/eks-prod.yaml
apiVersion: connector.hibernator.ardikasaputro.io/v1alpha1
kind: K8SCluster
metadata:
  name: eks-prod
  namespace: hibernator-system
  labels:
    app.kubernetes.io/name: hibernator
    environment: production
spec:
  providerRef:
    kind: CloudProvider
    name: aws-prod
  eks:
    name: prod-eks-1
    region: us-east-1
```

### 4. **Define HibernationPlan with Git management**

```yaml
# hibernator/hibernateplans/prod-offhours.yaml
apiVersion: hibernator.ardikasaputro.io/v1alpha1
kind: HibernationPlan
metadata:
  name: prod-offhours
  namespace: hibernator-system
  labels:
    app.kubernetes.io/name: hibernator
    environment: production
    version: "1.2"
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
        - from: prod-db
          to: prod-cluster

  targets:
    - name: prod-db
      type: rds
      connectorRef:
        kind: CloudProvider
        name: aws-prod
      parameters:
        snapshotBeforeStop: true

    - name: prod-cluster
      type: eks
      connectorRef:
        kind: CloudProvider
        name: aws-prod
      parameters:
        clusterName: eks-prod
        nodeGroups:
          - name: default-ng
          - name: gpu-ng
```

### 5. **Setup ArgoCD Application (recommended for GitOps)**

```yaml
# argocd-hibernator-app.yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: hibernator
  namespace: argocd
spec:
  project: default
  source:
    repoURL: https://github.com/myorg/my-infrastructure-repo.git
    targetRevision: main
    path: hibernator/
  destination:
    server: https://kubernetes.default.svc
    namespace: hibernator-system
  syncPolicy:
    automated:
      prune: true      # Remove resources if deleted from Git
      selfHeal: true   # Reconcile if cluster drifts
    syncOptions:
      - CreateNamespace=true
```

Deploy ArgoCD Application:

```bash
kubectl apply -f argocd-hibernator-app.yaml

# ArgoCD will now:
# 1. Watch for changes to hibernator/ directory in Git
# 2. Automatically deploy changes to cluster
# 3. Detect and alert if cluster diverges from Git
```

### 6. **Alternatively: Setup Flux for GitOps**

```yaml
# hibernator-source.yaml
apiVersion: source.toolkit.fluxcd.io/v1beta2
kind: GitRepository
metadata:
  name: hibernator-repo
  namespace: flux-system
spec:
  interval: 1m
  url: https://github.com/myorg/my-infrastructure-repo.git
  ref:
    branch: main
  secretRef:
    name: git-credentials

---
# hibernator-kustomization.yaml
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: hibernator
  namespace: flux-system
spec:
  interval: 10m
  sourceRef:
    kind: GitRepository
    name: hibernator-repo
  path: ./hibernator
  prune: true        # Remove resources if deleted from Git
  wait: true         # Wait for resources to become ready
```

Apply Flux resources:

```bash
kubectl apply -f hibernator-source.yaml
kubectl apply -f hibernator-kustomization.yaml
```

### 7. **Deploy with workflow**

```bash
# 1. Edit hibernation plan locally
vim hibernator/hibernateplans/prod-offhours.yaml
# Change: start: "20:00" → start: "19:00" (extend hibernation)

# 2. Commit to Git
git add hibernator/hibernateplans/prod-offhours.yaml
git commit -m "Extend prod hibernation to 19:00 for Q1 campaign"

# 3. Create PR
git push origin feature/extend-hibernation
# PR created; team reviews

# 4. PR approved and merged
# → ArgoCD/Flux automatically detects change
# → Applies new plan to cluster
# → Hibernation now starts at 19:00 instead of 20:00

# 5. Monitor sync
kubectl get application hibernator -n argocd
# NAME         SYNC STATUS   HEALTH STATUS
# hibernator   Synced        Healthy
```

### 8. **Rollback if needed**

```bash
# If new schedule causes issues:
git revert <commit-hash>
git push origin main

# ArgoCD/Flux detects revert
# Rolls back to previous hibernation schedule automatically
```

---

## Environment-specific Plans (Kustomize)

Use Kustomize overlays for per-environment variations:

```
hibernator/
├── base/
│   ├── kustomization.yaml
│   ├── namespace.yaml
│   ├── cloudproviders.yaml
│   └── hibernateplans.yaml
└── overlays/
    ├── dev/
    │   └── kustomization.yaml  # Different timezone, shorter window
    ├── staging/
    │   └── kustomization.yaml
    └── prod/
        └── kustomization.yaml  # 24/5 hibernation
```

---

## Decision Branches

| Decision | Option | Notes |
| --- | --- | --- |
| **GitOps tool?** | ArgoCD (recommended) | Most mature; easier learning curve |
| | Flux | Lighter; better for resource-constrained |
| **Git workflow?** | Monorepo | Single repo for all infrastructure |
| | Multi-repo | Separate repos per environment or team |
| **Approval process?** | Require PR reviews | Higher governance; slower changes |
| | Auto-merge | Faster; lower risk if good CI tests |

---

## Outcome

✓ Hibernation plans in Git with version control. Changes require PR approvals. Deployment automated via ArgoCD/Flux. Rollback available.

---

## Related Journeys

- [Configure RBAC for Hibernation](configure-rbac-for-hibernation.md) — Add Git-based RBAC
- [Deploy Operator to Cluster](deploy-operator-to-cluster.md) — Initial Hibernator setup

---

## Pain Points Solved

**RFC-0001:** Version control + GitOps enable auditable, repeatable hibernation deployments. All changes tracked in Git. No manual kubectl apply.

---

## RFC References

- **RFC-0001:** Control Plane + Runner Model (CRDs, declarative management)
