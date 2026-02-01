# RDS Database Hibernation

**Tier:** `[MVP]`

**Personas:** Platform Engineer, SRE, Database Administrator

**When:** Running RDS databases (instances or clusters) that don't need to run 24/7 (dev, staging, lab environments)

**Why:** RDS charges by instance-hour; stopping during off-hours can reduce costs by 50%+ for non-production environments.

---

## User Stories

**Story 1:** As a **Platform Engineer**, I want to **automatically stop RDS databases during off-hours with optional snapshots**, so that **I can reduce database costs for non-production environments**.

---

## When/Context

- **Cost reduction:** Stop paying for unused database capacity
- **Safety first:** Optional automatic snapshots protect against data loss
- **Automation:** No manual stop/start clicks
- **Non-disruptive:** Applications have time to drain connections before stop

---

## Business Outcome

Automatically stop RDS databases during off-hours, with optional snapshots before stopping, then restart during business hours.

---

## Step-by-Step Flow

### 1. **Identify RDS resources to hibernate**

List your RDS instances or clusters:

```bash
# List RDS instances
aws rds describe-db-instances --region us-east-1 \
  --query 'DBInstances[*].[DBInstanceIdentifier,DBInstanceStatus]'

# Output:
# - stg-postgres: available
# - stg-mysql: available
```

### 2. **Configure in HibernationPlan**

Add an RDS target with optional snapshot configuration:

```yaml
targets:
  - name: stg-db
    type: rds
    connectorRef:
      kind: CloudProvider
      name: aws-prod
    parameters:
      snapshotBeforeStop: true    # Optional: create snapshot before stopping
      instances:
        - stg-postgres            # Specific instances to target
        - stg-mysql
      # OR use selector (all instances matching pattern):
      # selector:
      #   resourceType: "instance"  # "instance" or "cluster"
```

**Key concepts highlighted:**
- **`snapshotBeforeStop: true`**: Creates automated snapshot with timestamp before stop (safety net)
- **`snapshotBeforeStop: false`**: Skip snapshot (faster stop, less storage cost)
- **`instances`**: List specific instances, OR
- **`selector`**: Tag-based selection (matches all instances with tag)

### 3. **Understand what happens at hibernation**

When schedule triggers hibernation:

1. **Snapshot created** (if enabled) → Takes 5-15 minutes depending on size
2. **Database stopped** → Connections closed, instance paused
3. **Restore metadata saved** → Instance identifiers and region captured
4. **Billing paused** → No compute charges while stopped (storage still charges)

Example state transitions:

```
Before hibernation:
  stg-postgres: Status=available, MultiAZ=true

During hibernation:
  stg-postgres: Status=stopping (1-2 min)
  stg-postgres: Status=stopped (billing paused)

After wakeup:
  stg-postgres: Status=starting (1-2 min)
  stg-postgres: Status=available
```

### 4. **Configure CloudProvider credentials**

Ensure CloudProvider has permissions for RDS operations:

```yaml
# CloudProvider must allow:
# - rds:DescribeDBInstances
# - rds:DescribeDBClusters
# - rds:StopDBInstance / StartDBInstance
# - rds:CreateDBSnapshot (if snapshotBeforeStop: true)
```

### 5. **Monitor hibernation**

**Check status before hibernation:**

```bash
kubectl describe hibernateplan prod-offhours
# status.executions:
#   - target: rds/stg-db
#     state: Pending
#     message: "Waiting for hibernation window"
```

**During hibernation (20:00):**

```bash
kubectl describe hibernateplan prod-offhours
# status.executions:
#   - target: rds/stg-db
#     state: Running
#     message: "Creating snapshot + stopping instances"

# In AWS console:
aws rds describe-db-instances --query 'DBInstances[0].DBInstanceStatus'
# Output: stopping (then stopped)
```

**After wakeup (06:00):**

```bash
kubectl describe hibernateplan prod-offhours
# status.executions:
#   - target: rds/stg-db
#     state: Completed
#     message: "Instances restarted successfully"

# Verify:
aws rds describe-db-instances --query 'DBInstances[0].DBInstanceStatus'
# Output: available
```

---

## Decision Branches

| Decision | Option | Notes |
|----------|--------|-------|
| **Snapshot before stop?** | Yes (safe) | Creates backup; takes 5-15 min, adds storage cost |
| | No (fast) | Stops immediately; no backup |
| **Stop all or specific instances?** | Specific (list by name) | Less risky; control scope |
| | All matching tag | More automated; tag your dev/staging instances |
| **Handle stop failures?** | Strict mode | Fail entire plan if any instance fails to stop |
| | BestEffort | Stop what you can, continue others |

---

## Outcome

✓ RDS databases automatically stop during off-hours (with optional snapshots) and restart during business hours.

---

## Related Journeys

- [Hibernation Plan Initial Design](hibernation-plan-initial-design.md) — Overview of plan structure
- [Monitor Hibernation Execution](monitor-hibernation-execution.md) — Track execution status
- [Troubleshoot Hibernation Failure](troubleshoot-hibernation-failure.md) — Debug RDS issues

---

## Pain Points Solved

**RFC-0001:** Manual RDS stop/start is tedious and often forgotten, leaving dev DBs running unnecessarily. HibernationPlan automates with audit trail.

---

## RFC References

- **RFC-0001:** Control Plane + Runner Model (RDS executor, restore metadata)
