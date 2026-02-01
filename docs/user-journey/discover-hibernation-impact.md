# Discover Hibernation Impact

**Tier:** `[MVP]`

**Personas:** End User, Application Team, Developer

**When:** Deploying or operating applications during hibernation windows

**Why:** Understanding how hibernation affects your application helps with deployment planning and debugging.

---

## User Stories

**Story 1:** As an **Application Developer**, I want to **understand how hibernation affects my applications and plan deployments accordingly**, so that **I can avoid timeouts and resource unavailability**.

---

## When/Context

- **Deployment planning:** Know when it's safe to deploy (avoid hibernation windows)
- **Debugging:** Understand why pods are pending or services are unavailable
- **Communication:** Application teams can explain hibernation impact to stakeholders
- **Automation:** CI/CD pipelines can detect hibernation and adjust schedules

---

## Business Outcome

Understand hibernation schedule and impact on application availability so deployments and operations can be planned accordingly.

---

## Step-by-Step Flow

### 1. **Find the relevant HibernationPlan**

Identify which plan affects your environment:

```bash
kubectl get hibernateplans

# Output:
# NAME              PHASE           AGE
# prod-offhours     Active          7d
# staging-offhours  Active          3d
# dev-offhours      Active          2d

# Your app runs in staging → Check staging-offhours
```

### 2. **Understand the schedule**

View the hibernation schedule:

```bash
kubectl describe hibernateplan staging-offhours | grep -A 10 "schedule:"

# Output:
# Schedule:
#   Timezone:  America/New_York
#   Off Hours:
#     Start:        20:00
#     End:          06:00
#     Days Of Week: MON, TUE, WED, THU, FRI
```

**Interpretation:** Staging hibernates:
- Weekday nights: 20:00 (8 PM) to 06:00 (6 AM next day)
- Not hibernated: Weekends, 06:00-20:00

### 3. **Check current hibernation status**

Determine if hibernation is currently active:

```bash
kubectl describe hibernateplan staging-offhours | grep "^Status:"

# Output:
# Status:
#   Phase:                   Active
#   Last Schedule Evaluation: 2026-02-01T20:30:00Z
#   Next Hibernation Start:  2026-02-02T20:00:00Z
#   Next Wakeup Time:        2026-02-03T06:00:00Z
```

**Interpretation:**
- If `Phase: Hibernating` or `Phase: Hibernated` → Services are down
- If `Phase: Active` → Services are running
- `Next Hibernation Start` → When services will go down

### 4. **View targets to understand scope**

See which resources are hibernated:

```bash
kubectl describe hibernateplan staging-offhours | grep -A 20 "targets:"

# Output:
# Targets:
#   - Name: staging-db
#     Type: rds
#   - Name: staging-cluster
#     Type: eks
#   - Name: staging-workloads
#     Type: workloadscaler
```

**Interpretation:** During hibernation:
- RDS database: STOPPED (no queries possible)
- EKS cluster: Nodes scaled to 0 (pods pending)
- Workloads: Scaled to 0 replicas (no pods running)

### 5. **Plan deployments around hibernation**

If deployment currently times out:

```bash
# Example: Deploy fails with "pod pending 10 minutes"
kubectl get pods
# Output:
# my-app-xyz    Pending    0/1    10m

kubectl describe pod my-app-xyz
# Output:
# Events:
#   Type     Reason                Status    Message
#   ----     ------                ------    -------
#   Warning  FailedScheduling      Pending   0/5 nodes available (EKS nodes scaled to 0)
```

**Solution options:**

Option A: Wait for wakeup (06:00)
```bash
# Deployment will proceed after wakeup
# Pods will schedule automatically once nodes are available
```

Option B: Deploy outside hibernation window
```bash
# Edit CI/CD pipeline to deploy only 06:00-20:00
# Example: GitHub Actions workflow cron
# schedule:
#   - cron: '0 8 * * MON-FRI'  # 08:00 UTC (13:00 EST)
```

Option C: Create temporary exception (see "Create Emergency Exception" journey)

### 6. **Understand resource impact**

**During hibernation:**
- Database queries → FAIL (RDS stopped)
- Workload pods → PENDING (nodes scaled to 0)
- API requests → TIMEOUT (no pods to handle)
- Logs → Sparse (minimal activity)

**After wakeup (06:00):**
- Database → AVAILABLE (1-2 min startup)
- Nodes → LAUNCHING (pods reschedule)
- Workloads → RUNNING (app ready, 3-5 min total)

---

## Decision Branches

| Decision | Option | Notes |
| --- | --- | --- |
| **Can't deploy now?** | Wait until wakeup | Simpler; no exceptions needed |
| | Create emergency exception | If urgent; requires approvals |
| **When should CI/CD deploy?** | Off-hour safe window (06:00-20:00) | Avoid hibernation |
| | Always (with retry logic) | Handle transient failures gracefully |

---

## Outcome

✓ Understand hibernation schedule and impact; plan deployments accordingly.

---

## Related Journeys

- [Create Emergency Exception](create-emergency-exception.md) — Temporarily override hibernation if urgent
- [Hibernation Plan Initial Design](hibernation-plan-initial-design.md) — See how the plan is structured

---

## Pain Points Solved

**RFC-0002:** Human-readable schedule format (start/end/daysOfWeek) helps developers understand hibernation windows in business terms (e.g., "8 PM to 6 AM", not "0 20 * * 1-5").

---

## RFC References

- **RFC-0002:** User-Friendly Schedule Format (schedule validation, timezone support, readable format)
- **RFC-0001:** Control Plane + Runner Model (phase state, status ledger showing current state)
