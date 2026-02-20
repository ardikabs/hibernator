# Manage Hibernation via CLI Plugin

**Tier:** `MVP`

**Personas:** SRE, DevOps Engineer, On-Call Engineer, Platform Engineer

**When:** Day-to-day operational management of HibernatePlan resources

**Why:** Reduce operational friction by providing quick, discoverable CLI commands for common tasks instead of manual manifest editing and kubectl annotations.

---

## User Stories

**Story 1:** As an **SRE**, I want to **validate the schedule of a HibernationPlan before applying it**, so that **I can catch scheduling mistakes early and avoid unintended hibernation windows**.

**Story 2:** As a **DevOps Engineer**, I want to **suspend hibernation during maintenance or incidents**, so that **I can prevent automatic shutdowns while we investigate issues**.

**Story 3:** As an **On-Call Engineer**, I want to **retry a failed target without waiting for automatic backoff**, so that **I can unblock stuck executions quickly when I know the issue is resolved**.

**Story 4:** As a **Platform Engineer**, I want to **stream executor logs to debug issues**, so that **I understand why a shutdown or wakeup failed without manually finding runner pods**.

**Story 5:** As an **SRE**, I want to **check the current operational status of a HibernationPlan**, so that **I know the phase, progress, and any pending errors at a glance**.

---

## When/Context

- **Before deployment**: Validate new HibernationPlans with dry-run schedule checks
- **During incidents**: Quickly suspend hibernation or manually retry to unblock
- **Troubleshooting**: Stream logs to debug executor failures
- **Monitoring**: Check operational status without kubectl describe
- **Day-to-day**: Manage hibernation workflows without direct manifest editing

---

## Business Outcome

Reduce operational overhead by providing streamlined CLI commands for schedule validation, operational control, and debugging. Users gain faster, more intuitive workflows compared to manual manifest editing and annotation management.

---

## Step-by-Step Flow

### Scenario 1: Validate Schedule Before Deployment

**Goal:** Ensure the schedule is correct before submitting the plan.

```bash
# 1. View the HibernationPlan YAML (as preparation)
cat <<EOF > my-hibernation-plan.yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: app-prod-hibernation
  namespace: production
spec:
  schedule:
    timezone: "America/New_York"
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday"]
  targets:
    - name: eks-prod
      type: eks
      connectorRef:
        kind: CloudProvider
        name: aws-prod
      parameters:
        clusterName: prod-cluster-1
EOF

# 2. Validate the schedule using the CLI plugin (before kubectl apply)
kubectl hibernator show schedule my-hibernation-plan.yaml --next 14

# Output shows:
# HibernationPlan: app-prod-hibernation (Namespace: production)
# Configured Schedule:
#   Timezone: America/New_York
#   Off-Hours: 20:00 - 06:00 (Monday - Friday)
# 
# Current State: Active (Running normally)
# Next Phase: Hibernating (in 3 hours 42 minutes at 2026-02-20 20:00 EST)
# 
# Next 14 Scheduled Events:
#   1. Hibernating  → 2026-02-20 20:00 EST (Fri)
#   2. Hibernated   → 2026-02-21 06:00 EST (Fri)
#   3. Hibernating  → 2026-02-23 20:00 EST (Mon)
#   4. Hibernated   → 2026-02-24 06:00 EST (Tue)
#   ... (10 more events)

# 3. If schedule looks correct, apply the plan
kubectl apply -f my-hibernation-plan.yaml
```

**Expected outcome:** User confidently applies the plan knowing the schedule is correct.

---

### Scenario 2: Suspend Hibernation During an Incident

**Goal:** Prevent automatic hibernation while investigating an issue.

```bash
# 1. Detect issue (e.g., via monitoring alert)
# Alert: "Database replication lag detected in prod"

# 2. Quickly suspend hibernation
kubectl hibernator suspend app-prod-hibernation -n production \
  --hours 4 \
  --reason "Database replication lag under investigation"

# Output:
# ✓ Hibernation suspended for app-prod-hibernation (production)
#   Until: 2026-02-20 23:00 EST (3 hours 59 minutes)
#   Reason: Database replication lag under investigation

# 3. Check status to confirm suspension is active
kubectl hibernator show status app-prod-hibernation -n production

# Output confirms:
# Status: Active (Hibernation Suspended)
# Suspended Until: 2026-02-20 23:00 EST
# Suspension Reason: Database replication lag under investigation

# 4. After issue is resolved, resume normal hibernation
kubectl hibernator resume app-prod-hibernation -n production

# Output:
# ✓ Hibernation resumed for app-prod-hibernation (production)
#   Schedule will resume per next planned event (in 1 hour 15 minutes)
```

**Expected outcome:** Hibernation is paused; team has 4 hours to investigate without automatic shutdowns.

---

### Scenario 3: Retry a Failed Target

**Goal:** Manually retry a stuck target without waiting for automatic backoff.

```bash
# 1. Monitor status and notice a failure
kubectl hibernator show status app-prod-hibernation -n production --watch

# Output shows:
# Status: Hibernating
#   Target Results:
#     ✓ eks-cluster-prod-1 (completed)
#     ✗ rds-database-prod (failed: connection timeout, will retry)
#     ⏳ ec2-bastion (pending: waiting for dependency)

# 2. After fixing the underlying issue (e.g., database network policy)
# Trigger immediate retry instead of waiting
kubectl hibernator retry app-prod-hibernation -n production --force

# Output:
# ✓ Retry triggered for app-prod-hibernation (production)
#   Previous attempts: 2/3 retries
#   Status: Queued for immediate execution

# 3. Watch status update as retry completes
kubectl hibernator show status app-prod-hibernation -n production --watch

# Output progresses to:
#     ✓ rds-database-prod (completed on retry #3)
```

**Expected outcome:** Failed target successfully retried and completes.

---

### Scenario 4: Debug Executor Failure with Logs

**Goal:** Stream logs to understand why an executor failed.

```bash
# 1. Notice a failed execution
kubectl hibernator show status app-prod-hibernation -n production

# Output shows:
# Status: Hibernating
#   Target Results:
#     ✗ rds-database-prod (failed: error - see logs)

# 2. Stream executor logs to debug the issue
kubectl hibernator logs app-prod-hibernation -n production \
  --follow \
  --severity error \
  --executor rds

# Output (streaming):
# [2026-02-20 20:05:12 UTC] INFO  executor=rds-executor target=rds-database-prod: Starting hibernation
# [2026-02-20 20:05:18 UTC] DEBUG executor=rds-executor target=rds-database-prod: Authenticated to AWS
# [2026-02-20 20:05:35 UTC] ERROR executor=rds-executor target=rds-database-prod: Failed to create DB snapshot
#   Details: InvalidDBInstanceStateFault: Cannot create snapshot because DB is in 'backing-up' state
# [2026-02-20 20:05:36 UTC] INFO  executor=rds-executor target=rds-database-prod: Retrying in 2 seconds...
# [2026-02-20 20:05:38 UTC] DEBUG executor=rds-executor target=rds-database-prod: Authenticated to AWS
# [2026-02-20 20:05:52 UTC] INFO  executor=rds-executor target=rds-database-prod: DB snapshot created successfully

# 3. Understand the issue:
# Root cause: DB was in backup state; retry after backup completed worked
# Action: None needed (auto-retry handled it)
```

**Expected outcome:** User understands the failure reason and can make informed decisions about next steps (optional manual intervention, or just let retry proceed).

---

### Scenario 5: Check Operational Status at a Glance

**Goal:** Get current status without kubectl describe verbosity.

```bash
# 1. Quick status check during standup or monitoring routine
kubectl hibernator show status app-prod-hibernation -n production

# Output:
# HibernationPlan: app-prod-hibernation (Namespace: production)
# Status: Hibernating
#   Began: 2026-02-20 20:05 EST
#   Progress: 18/20 targets completed
#   Error Count: 1 (1 failed, 1 pending)
#
# Last Execution (Hibernation Cycle #45):
#   Duration: 4 minutes 32 seconds
#   Target Results:
#     ✓ eks-cluster-prod-1 (completed)
#     ✗ rds-database-prod (failed: timeout, will retry)
#     ⏳ ec2-bastion (pending: waiting for dependency)
#     ✓ karpenter-nodepool-prod (completed)
#
# Retry Policy:
#   Max Retries: 3
#   Current Retry: 2/3 (Next retry in 2 minutes 18 seconds)

# 2. If needed, drill into logs or take action (suspend, retry, etc.)
```

**Expected outcome:** User gets concise, actionable status overview.

---

## Decision Branches

### Branch A: "Should I suspend or let the plan continue?"

**Decision Point:** When encountering a failure during hibernation.

- **If issue is temporary** (e.g., network blip, database backup finishing):
  - Action: Do nothing; let automatic retry proceed (2-3 retries are automatic)
  - Command: `kubectl hibernator show status --watch` (monitor passively)

- **If issue requires investigation** (e.g., misconfiguration, security policy):
  - Action: Suspend hibernation to prevent further shutdown attempts
  - Command: `kubectl hibernator suspend <plan> --hours 2 --reason "Investigating XYZ"`

- **If issue is resolved and you want to immediately retry**:
  - Action: Enforce retry instead of waiting for backoff timer
  - Command: `kubectl hibernator retry <plan> --force`

---

### Branch B: "How do I validate a new schedule?"

**Decision Point:** Before deploying a new HibernationPlan.

- **Option 1: Validate from YAML file**
  - Command: `kubectl hibernator show schedule <file.yaml>`
  - Use case: Before `kubectl apply`

- **Option 2: Validate existing plan in cluster**
  - Command: `kubectl hibernator show schedule <plan-name> -n <namespace>`
  - Use case: After applying, verify the plan's interpreted schedule

- **Option 3: Validate with different timezone**
  - Command: `kubectl hibernator show schedule <plan> --timezone "America/Los_Angeles"`
  - Use case: Validate schedule from user's local timezone perspective

---

## Related Journeys

- [Hibernation Plan Initial Design](./hibernation-plan-initial-design.md) — Create the plan the CLI will manage
- [Monitor Hibernation Execution](./monitor-hibernation-execution.md) — Status checks via kubectl
- [Troubleshoot Hibernation Failure](./troubleshoot-hibernation-failure.md) — Debug complex failures (CLI logs are one tool)
- [Create Emergency Exception](./create-emergency-exception.md) — Exception-based control alternative to CLI suspend
- [Manage Multi-Environment Schedules](./manage-multi-environment-schedules.md) — Multi-plan workflows using CLI validation

---

## Pain Points Solved

1. **Schedule Validation Friction**: Previously required applying, then fixing mistakes. Now: dry-run before apply.
2. **Manual Annotation Management**: Previously required `kubectl annotate` or manual YAML editing. Now: single CLI command.
3. **Log Fishing**: Previously required finding runner pods, reading job logs. Now: single `kubectl hibernator logs` command.
4. **Status Visibility**: Previously required `kubectl describe hibernateplan` (verbose). Now: concise `show status` summary.
5. **Operational Barriers**: Reduced need for kubectl expertise; lower operational overhead for SREs.

---

## RFC References

- [RFC-0007](../enhancements/0007-kubectl-hibernator-cli-plugin.md) — CLI plugin design and implementation
- [RFC-0001](../enhancements/0001-hibernate-operator.md) — Core operator architecture (context for operational tasks)
- [RFC-0003](../enhancements/0003-schedule-exceptions.md) — Exception system (suspend alternative to CLI)

---

## Success Metrics

- **Time to validate schedule**: < 3 seconds vs. 5+ minutes with manual processes
- **Time to suspend hibernation**: < 1 command vs. 2-3 with manual annotation
- **Time to debug failure**: < 1 minute to see logs vs. 5+ minutes to find runner pod
- **User adoption**: > 80% of hibernation operations use CLI within first quarter
- **Support tickets reduction**: Fewer questions about status, schedule, and failure debugging
