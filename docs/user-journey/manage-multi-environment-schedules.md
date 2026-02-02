# Manage Multi-Environment Schedules

**Tier:** `[Enhanced]`

**Personas:** Platform Engineer, DevOps Engineer

**When:** Managing hibernation across multiple environments (DEV, STG, PROD) with different policies

**Why:** Different environments have different SLAs and cost priorities; hibernation schedules should reflect each environment's requirements.

---

## User Stories

**Story 1:** As a **Platform Engineer**, I want to **design environment-specific hibernation policies**, so that **DEV saves aggressively while PROD remains conservative**.

**Story 2:** As a **DevOps Engineer**, I want to **deploy separate HibernationPlans per environment**, so that **each environment's schedule is independently managed and versioned**.

**Story 3:** As a **Cost Manager**, I want to **monitor per-environment hibernation cost savings**, so that **I can track ROI and adjust policies based on actual performance**.

---

## When/Context

- **DEV:** Aggressive hibernation (24/5, nights + weekends)
- **STG:** Moderate hibernation (nights + weekends)
- **PROD:** Conservative (nights only, restricted)
- **Auto-scaling:** Adjust capacity per environment during hibernation

---

## Business Outcome

Implement environment-specific hibernation schedules that balance cost savings with SLA requirements.

---

## Step-by-Step Flow

### 1. **Plan environment-specific policies**

```
Environment  │ Hibernation Windows        │ Target          │ Cost Savings
─────────────┼────────────────────────────┼─────────────────┼──────────────
DEV          │ 18:00-08:00 every day      │ Aggressive      │ 60%+
STG          │ 20:00-06:00 weekday nights │ Moderate        │ 40-50%
             │ Entire weekends            │                 │
PROD         │ 22:00-06:00 weekday nights │ Conservative    │ 20-30%
             │ No weekends                │ High availability│
```

### 2. **Create DEV hibernation plan**

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernationPlan
metadata:
  name: dev-aggressive-hibernation
  namespace: hibernator-system
  labels:
    environment: dev
spec:
  schedule:
    timezone: "America/New_York"
    offHours:
      - start: "18:00"
        end: "08:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"]
      # Every day: 18:00-08:00

  execution:
    strategy:
      type: Sequential

  behavior:
    mode: BestEffort  # DEV: Don't fail on errors

  targets:
    - name: dev-db
      type: rds
      connectorRef:
        kind: CloudProvider
        name: aws-dev
      parameters:
        snapshotBeforeStop: false  # DEV: Skip snapshots for speed

    - name: dev-cluster
      type: eks
      connectorRef:
        kind: CloudProvider
        name: aws-dev
      parameters:
        clusterName: eks-dev
        nodeGroups:
          - name: default-ng
```

### 3. **Create STG hibernation plan**

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernationPlan
metadata:
  name: stg-moderate-hibernation
  namespace: hibernator-system
  labels:
    environment: stg
spec:
  schedule:
    timezone: "America/New_York"
    offHours:
      # Weekday nights
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]

      # All day weekends
      - start: "00:00"
        end: "23:59"
        daysOfWeek: ["SAT", "SUN"]

  execution:
    strategy:
      type: DAG
      maxConcurrency: 2

  behavior:
    mode: BestEffort

  targets:
    - name: stg-db
      type: rds
      connectorRef:
        kind: CloudProvider
        name: aws-stg
      parameters:
        snapshotBeforeStop: true  # STG: Backup before stop

    - name: stg-cluster
      type: eks
      connectorRef:
        kind: CloudProvider
        name: aws-stg
      parameters:
        clusterName: eks-stg
        nodeGroups:
          - name: default-ng
          - name: compute-ng
```

### 4. **Create PROD hibernation plan**

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernationPlan
metadata:
  name: prod-conservative-hibernation
  namespace: hibernator-system
  labels:
    environment: prod
spec:
  schedule:
    timezone: "America/New_York"
    offHours:
      # Weekday nights only (late: 22:00-06:00)
      - start: "22:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]

  execution:
    strategy:
      type: Sequential

  behavior:
    mode: Strict  # PROD: Fail fast on errors

  targets:
    - name: prod-db
      type: rds
      connectorRef:
        kind: CloudProvider
        name: aws-prod
      parameters:
        snapshotBeforeStop: true  # PROD: Always snapshot

    - name: prod-cluster
      type: eks
      connectorRef:
        kind: CloudProvider
        name: aws-prod
      parameters:
        clusterName: eks-prod
        nodeGroups:
          - name: default-ng
          - name: compute-ng
        # PROD: EKS executor handles managed node groups only; Karpenter uses separate executor
```

### 5. **Deploy all plans to cluster**

```bash
kubectl apply -f dev-hibernation.yaml
kubectl apply -f stg-hibernation.yaml
kubectl apply -f prod-hibernation.yaml

# Verify all plans
kubectl get hibernateplans
# NAME                              PHASE           AGE
# dev-aggressive-hibernation        Active          1m
# stg-moderate-hibernation          Active          1m
# prod-conservative-hibernation     Active          1m
```

### 6. **Monitor per-environment execution**

```bash
# DEV execution (most aggressive)
kubectl describe hibernateplan dev-aggressive-hibernation | grep -A 5 "executions"

# STG execution (moderate)
kubectl describe hibernateplan stg-moderate-hibernation | grep -A 5 "executions"

# PROD execution (conservative)
kubectl describe hibernateplan prod-conservative-hibernation | grep -A 5 "executions"
```

### 7. **Query cost savings by environment**

```bash
# Prometheus query: hibernation cost savings per env
# (assuming metrics tagged with environment)
hibernator_hibernation_cost_saved{environment="dev"}   # ~60% savings
hibernator_hibernation_cost_saved{environment="stg"}   # ~40% savings
hibernator_hibernation_cost_saved{environment="prod"}  # ~20% savings
```

---

## Advanced: Exception override per environment

If STG needs extended hibernation for specific event:

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernationPlan
metadata:
  name: stg-moderate-hibernation
spec:
  schedule:
    exceptions:
      - name: "stg-q1-event"
        description: "Q1 summit - extend hibernation"
        type: "extend"
        validFrom: "2026-02-01T00:00:00Z"
        validUntil: "2026-02-28T23:59:59Z"
        windows:
          - start: "18:00"
            end: "08:00"
            daysOfWeek: ["SAT", "SUN"]  # Also hibernate weekends for the event
```

---

## Decision Branches

| Decision | Option | Notes |
| --- | --- | --- |
| **DEV hibernation?** | Aggressive (24/5) | Save costs; dev SLA is flexible |
| | Moderate | Some availability; testing needs |
| **STG hibernation?** | Moderate (nights+weekends) | Balance cost and testing availability |
| | Conservative | Preserve capacity for QA |
| **PROD hibernation?** | Conservative (nights only) | Protect against customer impact |
| | None (disabled) | If cost savings < SLA risk |

---

## Outcome

✓ DEV, STG, and PROD each have environment-specific hibernation schedules. Cost savings balanced with SLA per environment.

---

## Related Journeys

- [Deploy Operator to Cluster](deploy-operator-to-cluster.md) — Setup base operator
- [Integrate with GitOps](integrate-with-gitops.md) — Manage all plans in Git
- [Discover Hibernation Impact](discover-hibernation-impact.md) — Understand schedule impact

---

## Pain Points Solved

**RFC-0002:** Human-readable schedule format makes per-environment policies easy to maintain and reason about. Each environment has clear start/end times.

---

## RFC References

- **RFC-0002:** User-Friendly Schedule Format (start/end/daysOfWeek, timezone support)
- **RFC-0001:** Control Plane + Runner Model (multiple plans per operator, independent scheduling)
