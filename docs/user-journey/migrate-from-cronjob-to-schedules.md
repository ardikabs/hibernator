# Migrate from CronJob to Declarative Schedules

**Tier:** `[Advanced]`

**Personas:** Platform Engineer, DevOps Engineer, SRE

**When:** Replacing ad-hoc CronJob scripts with declarative HibernationPlans

**Why:** Declarative infrastructure provides built-in recovery, observability, and audit trails; manual scripts require constant maintenance.

---

## User Stories

**Story 1:** As a **Platform Engineer**, I want to **migrate from ad-hoc CronJob scripts to declarative HibernationPlans**, so that **I gain built-in recovery, observability, and audit trails**.

---

## When/Context

- **Existing CronJobs:** Ad-hoc hibernation/wakeup scripts scattered across repos
- **Pain points:** No dependency management, limited error recovery, poor visibility
- **Target state:** Single source of truth via Hibernator CRDs, automatic retry, full audit trail
- **Gradual migration:** Migrate one team/environment at a time

---

## Business Outcome

Consolidate scattered hibernation scripts into a unified, maintainable declarative system.

---

## Step-by-Step Flow

### 1. **Audit existing CronJob landscape**

Identify all hibernation-related CronJobs:

```bash
# Find all CronJobs in all namespaces
kubectl get cronjobs -A -o wide | grep -i hibernate

# Output:
# NAMESPACE        NAME                  SCHEDULE      SUSPEND  ACTIVE  LAST SCHEDULE
# operations       eks-scale-down        0 20 * * 1-5  false    0       2d
# operations       eks-scale-up          0 6 * * 1-5   false    0       2d
# operations       rds-snapshot-stop     0 20 * * *    false    0       1d
# operations       rds-start             0 6 * * *     false    0       1d
# infrastructure   ec2-stop              0 20 * * 1-5  false    0       2d
# infrastructure   ec2-start             0 6 * * 1-5   false    0       2d

# Get details of each CronJob
for ns in operations infrastructure; do
  kubectl get cronjobs -n $ns -o yaml > cronjobs-$ns.yaml
done
```

### 2. **Analyze CronJob specs**

Extract hibernation logic:

```bash
#!/bin/bash
# analyze-cronjobs.sh

echo "=== CronJob Analysis ==="
echo ""

for ns in operations infrastructure; do
  echo "Namespace: $ns"
  kubectl get cronjobs -n $ns -o jsonpath='{range .items[*]}{.metadata.name} | {.spec.schedule} | {.spec.jobTemplate.spec.template.spec.containers[0].command[*]}\n{end}'
  echo ""
done
```

Sample output:

```
eks-scale-down | 0 20 * * 1-5 | /bin/bash -c aws eks update-nodegroup-with-tags --nodegroup-name default --tags Environment=hibernated
eks-scale-up | 0 6 * * 1-5 | /bin/bash -c aws eks update-nodegroup-with-tags --nodegroup-name default --tags Environment=active
rds-snapshot-stop | 0 20 * * * | /bin/bash -c aws rds create-db-snapshot ... && aws rds stop-db-instance
rds-start | 0 6 * * * | /bin/bash -c aws rds start-db-instance
ec2-stop | 0 20 * * 1-5 | /bin/bash -c aws ec2 stop-instances --filters Name=tag:Hibernate,Values=true
ec2-start | 0 6 * * 1-5 | /bin/bash -c aws ec2 start-instances --filters Name=tag:Hibernate,Values=true
```

### 3. **Map CronJobs to HibernationPlan targets**

```yaml
# Mapping table
CronJob: eks-scale-down + eks-scale-up
  → HibernationPlan target: type: eks
    └─ connectorRef: aws-prod
    └─ schedule: 20:00-06:00 Mon-Fri

CronJob: rds-snapshot-stop + rds-start
  → HibernationPlan target: type: rds
    └─ connectorRef: aws-prod
    └─ schedule: 20:00-06:00 (all days)
    └─ snapshotBeforeStop: true

CronJob: ec2-stop + ec2-start
  → HibernationPlan target: type: ec2
    └─ connectorRef: aws-prod
    └─ selector: tags { Hibernate: true }
    └─ schedule: 20:00-06:00 Mon-Fri
```

### 4. **Create CloudProvider connector**

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: CloudProvider
metadata:
  name: aws-prod
  namespace: hibernator-system
spec:
  type: aws
  aws:
    accountId: "123456789012"
    region: us-east-1
    auth:
      serviceAccount: {}  # Uses IRSA
```

### 5. **Create initial HibernationPlan from CronJobs**

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernationPlan
metadata:
  name: prod-offhours-migration
  namespace: hibernator-system
  annotations:
    migration-source: "eks-scale-down, eks-scale-up, rds-snapshot-stop, rds-start, ec2-stop, ec2-start"
spec:
  schedule:
    timezone: "UTC"
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"]

  execution:
    strategy:
      type: Sequential  # Start simple; can optimize later

  behavior:
    mode: BestEffort  # Don't fail entire plan if one resource fails

  targets:
    # EKS (replaces eks-scale-down + eks-scale-up)
    - name: prod-eks-nodegroups
      type: eks
      connectorRef:
        kind: CloudProvider
        name: aws-prod
      parameters:
        clusterName: prod-eks-1
        nodeGroups:
          - name: default-ng
          - name: gpu-ng

    # RDS (replaces rds-snapshot-stop + rds-start)
    - name: prod-rds-instances
      type: rds
      connectorRef:
        kind: CloudProvider
        name: aws-prod
      parameters:
        snapshotBeforeStop: true

    # EC2 (replaces ec2-stop + ec2-start)
    - name: prod-ec2-instances
      type: ec2
      connectorRef:
        kind: CloudProvider
        name: aws-prod
      parameters:
        selector:
          tags:
            Hibernate: "true"
```

### 6. **Run dry-run test**

```bash
# Apply HibernationPlan to staging cluster first
kubectl apply -f hibernationplan-migration.yaml --namespace=staging

# Verify it created successfully
kubectl describe hibernateplan prod-offhours-migration -n staging

# Wait for first scheduled execution (or wait for next scheduled time)
# Monitor execution
kubectl logs -n hibernator-system -f \
  deployment/hibernator-controller
```

### 7. **Verify hibernation and wakeup work**

Check execution ledger:

```bash
# After scheduled hibernation time
kubectl get hibernateplan prod-offhours-migration -o jsonpath='{.status.executions[*]}' | jq .

# Output:
# [
#   {
#     "target": "eks/prod-eks-nodegroups",
#     "state": "Completed",
#     "startedAt": "2026-02-01T20:00:00Z",
#     "finishedAt": "2026-02-01T20:05:30Z",
#     "message": "Successfully scaled EKS managed node groups to 0"
#   },
#   {
#     "target": "rds/prod-rds-instances",
#     "state": "Completed",
#     "startedAt": "2026-02-01T20:05:30Z",
#     "finishedAt": "2026-02-01T20:08:00Z",
#     "message": "Snapshot created; database stopped"
#   },
#   {
#     "target": "ec2/prod-ec2-instances",
#     "state": "Completed",
#     "startedAt": "2026-02-01T20:08:00Z",
#     "finishedAt": "2026-02-01T20:10:00Z",
#     "message": "3 instances stopped"
#   }
# ]

# After wakeup time (06:00)
kubectl get hibernateplan prod-offhours-migration -o jsonpath='{.status.phase}' # Should be "Active"
```

### 8. **Validate cost savings parity**

Compare CronJob era vs. HibernationPlan era:

```bash
# Cost before migration (from cost tracking):
echo "Cost during off-hours (CronJob era): $5000/month"

# Cost after migration (from metrics):
kubectl exec -n hibernator-system deployment/hibernator-controller -- \
  curl localhost:8080/metrics | grep hibernator_cost_savings_total

# Output:
# hibernator_cost_savings_total{plan="prod-offhours-migration"} 4950
# (Nearly identical; confirms plans are working equivalently)
```

### 9. **Disable old CronJobs (keep for rollback)**

```bash
# Suspend old CronJobs instead of deleting (for 1 week rollback window)
kubectl patch cronjob eks-scale-down -n operations -p '{"spec":{"suspend":true}}'
kubectl patch cronjob eks-scale-up -n operations -p '{"spec":{"suspend":true}}'
kubectl patch cronjob rds-snapshot-stop -n operations -p '{"spec":{"suspend":true}}'
kubectl patch cronjob rds-start -n operations -p '{"spec":{"suspend":true}}'
kubectl patch cronjob ec2-stop -n infrastructure -p '{"spec":{"suspend":true}}'
kubectl patch cronjob ec2-start -n infrastructure -p '{"spec":{"suspend":true}}'
```

### 10. **Migrate additional environments gradually**

Move from prod → staging → dev:

```bash
# 1. Staging (lower risk)
kubectl apply -f hibernationplan-stg.yaml

# 2. Wait 1 week; verify no issues
# 3. Dev
kubectl apply -f hibernationplan-dev.yaml

# 4. Finally promote to prod if not already done
kubectl apply -f hibernationplan-prod.yaml
```

### 11. **Clean up old CronJobs**

After 4-week rollback window with zero issues:

```bash
# Delete old CronJobs
kubectl delete cronjob eks-scale-down -n operations
kubectl delete cronjob eks-scale-up -n operations
kubectl delete cronjob rds-snapshot-stop -n operations
kubectl delete cronjob rds-start -n operations
kubectl delete cronjob ec2-stop -n infrastructure
kubectl delete cronjob ec2-start -n infrastructure

# Document in runbooks
echo "Migrated to HibernationPlan; old CronJobs deleted"
```

### 12. **Capture knowledge and runbooks**

```markdown
# Hibernation Runbooks (Post-Migration)

## Normal Operation

```bash
# Monitor hibernation
kubectl describe hibernateplan prod-offhours-migration

# Check cost savings
kubectl logs -n hibernator-system -f deployment/hibernator-controller | grep cost_savings
```

## Troubleshooting

If hibernation fails:

```bash
# Check runner Job logs
kubectl logs -n hibernator-system job/hibernator-runner-xyz

# Check plan status
kubectl get hibernateplan prod-offhours-migration -o yaml | grep -A 20 status.executions
```

## Emergency Override

To immediately stop/start resources:

```bash
# Create suspend exception for incidents
kubectl apply -f - <<EOF
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernationPlan
metadata:
  name: prod-offhours-migration
spec:
  schedule:
    exceptions:
      - name: incident-override
        type: suspend
        validFrom: "2026-02-01T21:00:00Z"
        validUntil: "2026-02-02T06:00:00Z"
        leadTime: "1h"
EOF
```
```

---

## Migration Checklist

- [ ] Audited all existing CronJobs
- [ ] Mapped CronJobs to HibernationPlan targets
- [ ] Created CloudProvider connector
- [ ] Created initial HibernationPlan
- [ ] Tested in staging environment
- [ ] Verified hibernation/wakeup cycle works
- [ ] Validated cost savings parity
- [ ] Suspended old CronJobs (1-week rollback)
- [ ] Monitored for issues (1 week)
- [ ] Deleted old CronJobs
- [ ] Updated runbooks and documentation

---

## Decision Branches

| Decision | Option | Notes |
| --- | --- | --- |
| **Execution order?** | Sequential | Simple; safe; slower |
| | Parallel | Faster; requires testing |
| **Behavior on partial failure?** | Strict (fail all) | Safe; prevents partial state |
| | BestEffort (continue) | Lenient; continues on errors |
| **Rollback plan?** | Keep CronJobs suspended (1-4 weeks) | Safe; easy rollback |
| | Delete immediately | Risky; need very confident |

---

## Outcome

✓ Migrated from 6 CronJobs to 1 HibernationPlan. Hibernation/wakeup cycles working identically. Cost savings parity verified. Automated recovery enabled.

---

## Related Journeys

- [Deploy Operator to Cluster](deploy-operator-to-cluster.md) — Initial setup
- [Create CloudProvider Connector](create-cloudprovider-connector.md) — Connector setup
- [Monitor Hibernation Execution](monitor-hibernation-execution.md) — Monitoring

---

## Pain Points Solved

**RFC-0001:** HibernationPlan's execution ledger, DAG support, and automatic retry eliminate ad-hoc script maintenance burden. Status tracking provides single source of truth.

---

## RFC References

- **RFC-0001:** Control Plane + Runner Model (status ledger, execution strategy, automatic recovery)
