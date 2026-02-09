# Troubleshoot Hibernation Failure

**Tier:** `[MVP]`

**Personas:** SRE, Platform Engineer, On-Call Engineer

**When:** Hibernation or wakeup fails, or takes longer than expected

**Why:** Understanding what went wrong and how to recover is critical for operational confidence.

---

## User Stories

**Story 1:** As an **On-Call Engineer**, I want to **quickly identify which target failed and why**, so that **I can classify the error and decide on recovery action**.

**Story 2:** As a **Platform Engineer**, I want to **distinguish transient errors from permanent ones**, so that **I can decide whether to retry or escalate to manual intervention**.

**Story 3:** As an **SRE**, I want to **access executor logs and restore data**, so that **I can root-cause issues and prevent recurrence**.

---

## When/Context

- **Rapid recovery:** Quick diagnosis minimizes impact
- **Automation trust:** Understanding failures builds confidence in the system
- **Cost protection:** Prevent resource leaks (e.g., RDS stuck in "stopping" state)
- **Root cause learning:** Identify patterns to prevent recurrence

---

## Business Outcome

Diagnose hibernation failures, distinguish transient from permanent errors, and execute appropriate recovery actions.

---

## Step-by-Step Flow

### 1. **Detect failure**

Check HibernationPlan status:

```bash
kubectl describe hibernateplan prod-offhours

# Look for:
# - status.phase: Error
# - status.message: "Details about failure"
# - status.executions[].state: Failed
```

### 2. **Identify which target failed**

Look at `status.executions[]` ledger:

```bash
# Example: RDS target failed
# status.executions:
#   - target: database
#     state: Failed
#     message: "Failed to stop instance: timeout"
#     retryCount: 3
#     lastRetryTime: 2026-02-01T20:25:00Z
```

### 3. **Classify the error**

Determine if transient or permanent:

**Transient errors** (may self-recover on retry):
- Network timeout
- Cloud API throttling (RequestLimitExceeded)
- Resource in transition state

**Permanent errors** (won't recover without intervention):
- Resource not found (already deleted)
- Insufficient permissions (IAM policy issue)
- Invalid configuration (bad selector, wrong target name)
- Resource incompatible with hibernation (e.g., RDS in wrong state)

### 4. **Check error classification in status**

Controller marks errors:

```bash
kubectl get hibernateplan prod-offhours -o yaml | grep -A 5 "error"

# Example output:
# errorClassification: Transient
# errorMessage: "AWS API rate limited (RequestLimitExceeded)"
# suggestedAction: "Will retry automatically with exponential backoff"
```

### 5. **View runner logs for details**

Get the Job name from status:

```bash
kubectl describe hibernateplan prod-offhours | grep -A 2 "database"
# Find jobRef:
# jobRef:
#   name: hibernate-runner-xyz789
#   namespace: hibernator-system

# View logs:
kubectl logs job/hibernate-runner-xyz789 -n hibernator-system

# Example log output:
# [20:05:30] Starting RDS executor for target: database
# [20:05:31] Connecting to AWS RDS API
# [20:05:35] Creating snapshot: rds:database-2026-02-01-20-05
# [20:15:00] Snapshot completed
# [20:15:01] Stopping instances: [stg-postgres, stg-mysql]
# [20:15:02] stg-postgres: stopping
# [20:25:00] ERROR: stg-postgres: stop timeout after 600s
# [20:25:01] Marking execution as Failed
```

### 6. **Check resource state in cloud console**

Verify actual state vs. expected state:

```bash
# Example: RDS status check
aws rds describe-db-instances --query 'DBInstances[0].DBInstanceStatus'
# Output: stopping (stuck!)

# Example: EKS node group check
aws eks describe-nodegroup --cluster-name prod-eks-1 --nodegroup-name default-ng \
  --query 'nodegroup.scalingConfig'
# Output: {desiredSize: 0, minSize: 1, maxSize: 5}  ← Already scaled, but Job shows failure
```

### 7. **Take recovery action**

Depends on error type:

**For transient errors:**
```bash
# Controller retries automatically with exponential backoff
# (default: up to 3 attempts, max 30 minutes between retries)

# Or manually force retry (delete failed Job; controller relaunches):
kubectl delete job hibernate-runner-xyz789 -n hibernator-system
# Controller detects Job missing, relaunches
```

**For permanent errors:**
```bash
# Option 1: Fix the issue and retry
# Example: Fix IAM policy permissions
aws iam attach-role-policy --role-name hibernator-operator \
  --policy-arn arn:aws:iam::aws:policy/AmazonEC2FullAccess

# Then delete the Job to retry:
kubectl delete job hibernate-runner-xyz789 -n hibernator-system

# Option 2: Skip this target and continue (if non-critical)
# Edit HibernationPlan to remove failed target, apply:
kubectl patch hibernateplan prod-offhours --type='json' \
  -p='[{"op": "remove", "path": "/spec/targets/0"}]'  # Remove first target

# Option 3: Suspend hibernation until fixed
# Set annotation to pause reconciliation:
kubectl annotate hibernateplan prod-offhours hibernator/suspend=true --overwrite
```

### 8. **Verify recovery**

```bash
# Check status after recovery attempt:
kubectl describe hibernateplan prod-offhours

# Should show:
# status.phase: Active (or Hibernating if recovery in progress)
# status.executions[].state: Completed (or Running)
```

---

## Common Failure Patterns & Solutions

| Error | Cause | Solution |
|-------|-------|----------|
| `RDS stop timeout` | RDS taking long to drain connections | Wait and retry; or revoke DB connections forcefully |
| `EKS scale API error` | ASG API throttled | Reduce maxConcurrency; retry |
| `EC2 not found` | Instance already deleted | Remove from targets |
| `Permission denied` | IAM policy missing | Add required permissions; retry |
| `Target selector matches 0` | No resources matched selector | Fix tag selector; list actual resources |

---

## Decision Branches

| Decision | Option | Notes |
|----------|--------|-------|
| **Error transient?** | Yes | Wait for auto-retry or delete Job to force retry |
| | No | Fix root cause, then retry |
| **Should we pause hibernation?** | Yes | Set suspend annotation; fix issues; resume |
| | No | Continue with reduced targets (skip failed one) |
| **Manual intervention or auto?** | Auto (preferred) | Let controller retry; minimal disruption |
| | Manual | Run imperative recovery commands |

---

## Outcome

✓ Systematically diagnosed and recovered from hibernation failure; understood root cause for prevention.

---

## Related Journeys

- [Monitor Hibernation Execution](monitor-hibernation-execution.md) — Detect failure early
- [Hibernation Plan Initial Design](hibernation-plan-initial-design.md) — Understand plan structure

---

## Pain Points Solved

**RFC-0001:** Error classification (transient vs permanent) and retry logic enable automated recovery without manual intervention. Status ledger and logs provide debugging context.

---

## RFC References

- **RFC-0001:** Control Plane + Runner Model (error classification, retry logic, status ledger, Job logs)
