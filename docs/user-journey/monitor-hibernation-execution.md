# Monitor Hibernation Execution

**Tier:** `[MVP]`

**Personas:** SRE, Platform Engineer, On-Call Engineer

**When:** Hibernation window is active or wakeup is in progress

**Why:** Real-time observability into hibernation execution helps detect issues early and ensures resources are being managed as intended.

---

## User Stories

**Story 1:** As an **SRE**, I want to **track phase transitions and per-target execution status**, so that **I know exactly what's happening during hibernation/wakeup**.

**Story 2:** As a **Platform Engineer**, I want to **view execution logs and streaming progress**, so that **I can debug issues without SSH-ing into pods**.

**Story 3:** As an **On-Call Engineer**, I want to **receive alerts when targets fail**, so that **I can intervene before cascading failures occur**.

---

## When/Context

- **Transparency:** See exactly what's happening during hibernation/wakeup
- **Debugging:** Identify which resource failed and why
- **Alerting:** Know when to intervene vs. when to let automation continue
- **Audit trail:** Execution ledger provides compliance-friendly records

---

## Business Outcome

Track hibernation and wakeup progress in real-time, understand per-target execution status, and identify failures quickly.

---

## Step-by-Step Flow

### 1. **Check HibernationPlan status**

At any time, view the plan's current state:

```bash
kubectl get hibernateplans
# Output:
# NAME              PHASE           AGE
# prod-offhours     Hibernating     5m

kubectl describe hibernateplan prod-offhours
```

### 2. **Understand phase transitions**

Plan goes through phases during its lifecycle:

```
Active → Hibernating → Hibernated → WakingUp → Active
```

- **Active**: Normal operation, no hibernation currently
- **Hibernating**: Actively shutting down resources
- **Hibernated**: All resources stopped, waiting for wakeup window
- **WakingUp**: Actively restoring resources
- **Error**: Something failed; check `status.message`

### 3. **Watch execution ledger**

View per-target execution progress in `status.executions[]`:

```bash
kubectl describe hibernateplan prod-offhours

# Output (partial):
# Status:
#   Phase:       Hibernating
#   Executions:
#     - Target:      database
#       Executor:    rds
#       State:       Completed
#       StartedAt:   2026-02-01T20:05:00Z
#       FinishedAt:  2026-02-01T20:15:00Z
#       Attempts:    1
#       Message:     "Snapshot and stop completed"
#
#     - Target:      compute-cluster
#       Executor:    eks
#       State:       InProgress
#       StartedAt:   2026-02-01T20:15:30Z
#       Attempts:    1
#       Message:     "Scaling EKS node groups"
#
#     - Target:      worker-instances
#       Executor:    ec2
#       State:       Pending
#       Message:     "Waiting for EKS to complete"
```

**Key concepts highlighted:**
- **State**: `Pending | Running | Completed | Failed`
- **Attempts**: How many times executor has retried
- **StartedAt/FinishedAt**: Timestamps for duration calculation
- **Message**: Human-friendly status or error description

### 4. **View runner Job logs**

For detailed execution logs, check the Kubernetes Job:

```bash
# Find the runner Job
kubectl get jobs -l hibernator/plan=prod-offhours -l hibernator/target=database

# Get Job name
kubectl get jobs -l hibernator/plan=prod-offhours -l hibernator/target=database \
  -o jsonpath='{.items[0].metadata.name}'
# Output: hibernate-runner-abc123

# View logs
kubectl logs job/hibernate-runner-abc123

# Or stream live:
kubectl logs -f job/hibernate-runner-abc123
```

### 5. **Check restore metadata**

Verify restore data was captured (needed for wakeup):

```bash
# List restore ConfigMaps
kubectl get cm -l hibernator/plan=prod-offhours

# Output:
# NAME                        DATA   AGE
# restore-data-prod-offhours   3      5m

# View restore data for a specific target
kubectl get cm restore-data-prod-offhours -o yaml

# Output (partial):
# data:
#   rds_database: |
#     {
#       "instances": ["stg-postgres", "stg-mysql"],
#       "snapshotId": "rds:stg-postgres-2026-02-01-20-05"
#     }
#   eks_compute-cluster: |
#     {
#       "managedNodeGroups": {
#         "default-ng": {"desiredSize": 3}
#       }
#     }
```

### 6. **Monitor with metrics**

Check Prometheus metrics if you've enabled monitoring:

```bash
# Example metrics queries:
# Duration of hibernation execution
hibernator_execution_duration_seconds{plan="prod-offhours"}

# Execution success/failure rate
hibernator_execution_total{plan="prod-offhours",status="completed"}
hibernator_execution_total{plan="prod-offhours",status="failed"}

# Restore data size
hibernator_restore_data_size_bytes{plan="prod-offhours"}
```

---

## Decision Branches

| Decision | Option | Notes |
|----------|--------|-------|
| **Check status how often?** | Real-time (`-w` flag) | Use during first hibernation cycle to verify |
| | Every minute | Set up monitoring/alerting |
| | On-demand (when issues arise) | Less intrusive; sufficient for stable runs |
| **Alert on what?** | Plan phase = Error | Immediate action required |
| | Target state = Failed | Check logs, may be transient |
| | Execution duration > threshold | May indicate API throttling or stuck resources |

---

## Outcome

✓ Real-time visibility into hibernation execution; quick diagnosis of issues via status ledger and runner logs.

---

## Related Journeys

- [Troubleshoot Hibernation Failure](troubleshoot-hibernation-failure.md) — Deep dive when status shows failure
- [Hibernation Plan Initial Design](hibernation-plan-initial-design.md) — Understand plan structure

---

## Pain Points Solved

**RFC-0001:** CronJob-style scripts have no execution visibility; errors go unnoticed. Status ledger provides centralized observability.

---

## RFC References

- **RFC-0001:** Control Plane + Runner Model (status ledger, Job lifecycle, restore metadata, streaming logs)
