# Wakeup and Restore Resources

**Tier:** `[MVP]`

**Personas:** SRE, Platform Engineer, On-Call Engineer

**When:** End of hibernation window (e.g., 06:00 when hibernation ends)

**Why:** Restore resources to their pre-hibernation state so services are available during business hours.

---

## User Stories

**Story 1:** As an **SRE**, I want to **resources automatically restore to their pre-hibernation state at the end of hibernation windows**, so that **services are available during business hours without manual intervention**.

---

## When/Context

- **Automation:** No manual wake-up clicks needed
- **Reliability:** Restore metadata ensures resources return to correct state
- **Safety:** Phased restoration respects dependencies (restore databases before clusters)
- **Audit trail:** Wakeup execution tracked same as hibernation

---

## Business Outcome

Automatically restore all hibernated resources to their pre-hibernation capacity and state, making services available during business hours.

---

## Step-by-Step Flow

### 1. **Understand restore timing**

Controller checks schedule and determines when wakeup should occur:

```
Hibernation window: 20:00 - 06:00
Wakeup trigger: 06:00
Plan phase: Active → Hibernated → WakingUp → Active
```

Controller continuously evaluates schedule; when `06:00` arrives, it begins wakeup.

### 2. **Retrieve restore metadata**

Wakeup process loads saved restore data from ConfigMap:

```bash
# View restore metadata
kubectl get cm restore-data-prod-offhours -o yaml

# Output example:
# data:
#   rds_database: |
#     {
#       "instances": ["stg-postgres", "stg-mysql"],
#       "snapshotId": "rds:stg-postgres-2026-02-01-20-05",
#       "desiredState": "available"
#     }
#   eks_compute-cluster: |
#     {
#       "managedNodeGroups": {
#         "default-ng": {"desiredSize": 3, "minSize": 1, "maxSize": 5}
#       }
#     }
#   ec2_worker-instances: |
#     {
#       "instances": ["i-0123456789abcdef0", "i-fedcba9876543210"]
#     }
```

### 3. **Executor restores each target**

Each executor uses restore metadata to restore its target:

**RDS example:**

```
Restore metadata → ["stg-postgres", "stg-mysql"]
Executor action:  → Start-DBInstance stg-postgres
                 → Start-DBInstance stg-mysql
State: starting → available (2-3 minutes)
```

**EKS example:**

```
Restore metadata → {default-ng: desiredSize=3}
Executor action:  → SetDesiredCapacity default-ng 3
Nodes come up:   → Pending, Running (5-10 minutes)
Pods reschedule: → From pending to running
```

**EC2 example:**

```
Restore metadata → ["i-0123456789abcdef0", "i-fedcba9876543210"]
Executor action:  → Start-Instances
State: stopping → running (30 seconds - 2 minutes)
```

### 4. **Monitor wakeup progress**

```bash
kubectl describe hibernateplan prod-offhours

# During wakeup:
# status.phase: WakingUp
# status.executions:
#   - target: rds/database
#     state: Running
#     message: "Starting RDS instances"
#   - target: eks/compute-cluster
#     state: Pending
#     message: "Waiting for RDS to complete"
#
# After wakeup:
# status.phase: Active
# status.executions:
#   - target: rds/database
#     state: Completed
#     message: "Instances restored to available state"
```

### 5. **Verify resources are restored**

```bash
# Check EKS nodes
kubectl get nodes
# Output: 3 nodes (restored from metadata)

# Check RDS
aws rds describe-db-instances --query 'DBInstances[0].DBInstanceStatus'
# Output: available

# Check EC2
aws ec2 describe-instances --instance-ids i-0123456789abcdef0 \
  --query 'Reservations[0].Instances[0].State.Name'
# Output: running

# Verify workloads are running
kubectl get pods
# Output: All pods should be Running (previously Pending during hibernation)
```

### 6. **Handle restore failures**

If a target fails to restore:

```bash
kubectl describe hibernateplan prod-offhours

# Example failure:
# status.executions:
#   - target: rds/database
#     state: Failed
#     message: "Start-DBInstance failed: InvalidDBInstanceState"
#     retryCount: 2
#     attempts: 3
```

Depending on error:

**Transient errors (will auto-retry):**
- Cloud API throttle → Controller retries with exponential backoff
- Resource in transition → Wait for automatic retry

**Permanent errors (manual intervention):**
- Invalid configuration → Fix and manually restore resource
- Resource deleted → Remove from targets

```bash
# Manual restore (if needed):
aws rds start-db-instances --db-instance-identifier stg-postgres

# Or force controller retry:
kubectl delete job hibernate-runner-xyz789 -n hibernator-system
```

---

## Decision Branches

| Decision | Option | Notes |
| --- | --- | --- |
| **Automatic retry on failure?** | Yes (default) | Exponential backoff; up to 3 attempts |
| | Manual | Check status and retry manually if needed |
| **All resources or selective?** | All (default) | Restore all targets in HibernationPlan |
| | Selective | Edit plan to skip certain targets temporarily |

---

## Outcome

✓ All resources successfully restored to pre-hibernation state; services available for business hours.

---

## Related Journeys

- [Monitor Hibernation Execution](monitor-hibernation-execution.md) — Track wakeup progress
- [Troubleshoot Hibernation Failure](troubleshoot-hibernation-failure.md) — Debug restore issues

---

## Pain Points Solved

**RFC-0001:** Restore metadata ensures resources return to correct state (no guessing about replica counts, capacity, configuration).

---

## RFC References

- **RFC-0001:** Control Plane + Runner Model (restore metadata persistence, wakeup execution, phase transitions)
