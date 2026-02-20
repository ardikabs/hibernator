# Validate Hibernation Schedule Before Deployment

**Tier:** `MVP`

**Personas:** DevOps Engineer, Platform Engineer, SRE

**When:** Creating or modifying a HibernationPlan before deploying to production

**Why:** Catching schedule mistakes before deployment prevents accidental hibernation of critical resources and reduces operational incidents.

---

## User Stories

**Story 1:** As a **DevOps Engineer**, I want to **see exactly when my resources will be hibernated and restored**, so that **I can verify the schedule matches business hours guidelines before applying it**.

**Story 2:** As a **Platform Engineer**, I want to **validate schedule correctness without deploying to the cluster**, so that **I can catch mistakes in the dry-run phase instead of during actual hibernation**.

**Story 3:** As an **SRE**, I want to **understand how schedule exceptions will affect the resulting hibernation windows**, so that **I can explain to business stakeholders what the final schedule will look like**.

---

## When/Context

- **Development phase**: Building a new hibernation plan for a service
- **Modification phase**: Changing schedule parameters (timezone, times, days)
- **Review phase**: Team reviewing plan configuration before production deployment
- **Exception impact**: Understanding how active exceptions modify the schedule

---

## Business Outcome

Enable confident deployment of hibernation plans by validating the schedule completely before applying to the cluster. Reduce schedule-related incidents and improve team confidence in automation.

---

## Step-by-Step Flow

### Phase 1: Initial Schedule Design

**1.1 Create HibernationPlan YAML**

```yaml
# my-app-prod-hibernation.yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: my-app-prod-hibernation
  namespace: production
spec:
  schedule:
    timezone: "America/New_York"
    offHours:
      - start: "20:00"        # 8 PM
        end: "06:00"          # 6 AM next day
        daysOfWeek: ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday"]
  execution:
    strategy:
      type: Sequential
    behavior: BestEffort
  targets:
    - name: eks-prod
      type: eks
      connectorRef:
        kind: CloudProvider
        name: aws-prod
      parameters:
        clusterName: prod-cluster-1
```

**1.2 Validate schedule before applying:**

```bash
# Command: Validate schedule from YAML (before cluster interaction)
kubectl hibernator show schedule my-app-prod-hibernation.yaml

# Expected Output:
# HibernationPlan: my-app-prod-hibernation (from file)
# Configured Schedule:
#   Timezone: America/New_York
#   Off-Hours: 20:00 - 06:00 (Monday - Friday)
# 
# ✓ Schedule is valid and unambiguous
# ✓ No timezone conflicts detected
# 
# Next 10 Scheduled Events (starting from now):
#   1. Hibernating  → 2026-02-20 20:00 EST (Friday)
#   2. Hibernated   → 2026-02-21 06:00 EST (Friday)
#   3. Hibernating  → 2026-02-23 20:00 EST (Monday)
#   4. Hibernated   → 2026-02-24 06:00 EST (Tuesday)
#   5. Hibernating  → 2026-02-24 20:00 EST (Tuesday)
#   6. Hibernated   → 2026-02-25 06:00 EST (Wednesday)
#   7. Hibernating  → 2026-02-25 20:00 EST (Wednesday)
#   8. Hibernated   → 2026-02-26 06:00 EST (Thursday)
#   9. Hibernating  → 2026-02-26 20:00 EST (Thursday)
# 10. Hibernated   → 2026-02-27 06:00 EST (Friday)
#
# Business hour coverage:
#   Weekdays (Mon-Fri): Resources offline 8 PM - 6 AM EST (10 hours/day)
#   Weekends: Resources stay online 24/7
#   Annual savings: ~1,200 hours of resource consumption/year
```

**1.3 Team Review**

```bash
# Share schedule output with team:
# "Here's the schedule - every weekday 8 PM to 6 AM EST, offline on weekends"
# 
# Team confirms:
# ✓ Aligns with business hours (opens at 6 AM, closes at 8 PM)
# ✓ Weekends are covered (resources stay online for Monday launch)
# ✓ No holiday/exception conflicts today
```

---

### Phase 2: Deployment & Verification

**2.1 Apply the plan to the cluster:**

```bash
kubectl apply -f my-app-prod-hibernation.yaml
```

**2.2 Verify the cluster sees the same schedule:**

```bash
# Command: Validate the applied plan in the cluster
kubectl hibernator show schedule my-app-prod-hibernation -n production

# Output should match the YAML validation, confirming no parsing issues
```

---

### Phase 3: Monitoring & Exception Handling

**3.1 After plan is active, monitor upcoming events:**

```bash
# Watch the next events as we approach hibernation window
kubectl hibernator show schedule my-app-prod-hibernation -n production \
  --next 20 \
  --watch  # Updates every 10 seconds

# Output updates in real-time:
# Next Phase: Hibernating (in 2 hours 15 minutes at 2026-02-20 20:00 EST) ← Countdown updates
```

**3.2 If an exception is created (e.g., emergency maintenance), schedule updates:**

```bash
# Create an exception to extend hibernation
kubectl apply -f - <<EOF
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: emergency-maintenance-feb21
  namespace: production
spec:
  planRef:
    name: my-app-prod-hibernation
  type: extend
  until: 2026-02-21T14:00:00Z  # Extend wakeup by 8 hours
  reason: "Emergency maintenance on RDS cluster"
EOF

# Immediately check how the schedule changes:
kubectl hibernator show schedule my-app-prod-hibernation -n production

# Output now shows:
# Active Exceptions:
#   - Extend until 2026-02-21 14:00 EST (Lead time: 30 min remaining)
#
# Adjusted Next Events:
#   1. Hibernating  → 2026-02-20 20:00 EST (Friday) — same as before
#   2. Hibernated   → 2026-02-21 06:00 EST (Friday) — same as before
#   3. Extended     → 2026-02-21 14:00 EST (Friday) — EXTENDED by 8 hours due to exception
#   4. Hibernating  → 2026-02-23 20:00 EST (Monday) — resumes normal schedule
```

---

### Phase 4: Troubleshooting Schedule Issues

**4.1 Scenario: Timezone mismatch**

```bash
# User is in Los Angeles but plan is in New York time
# Validate from their local timezone perspective:
kubectl hibernator show schedule my-app-prod-hibernation -n production \
  --timezone "America/Los_Angeles"

# Output (adjusted to PST):
# Configured Schedule:
#   Timezone: America/New_York (displayed schedule)
#   Your timezone: America/Los_Angeles
#
# Next 10 Scheduled Events (converted to PST):
#   1. Hibernating  → 2026-02-20 17:00 PST (Friday)  [5 PM instead of 8 PM]
#   2. Hibernated   → 2026-02-21 03:00 PST (Friday)  [3 AM instead of 6 AM]
#
# ⚠️ Note: Schedule is evaluated in America/New_York time
#   These times are for your reference only
```

**4.2 Scenario: Detecting schedule conflicts**

```bash
# Plan with overlapping exception windows
# Plugin detects and warns:
kubectl hibernator show schedule my-app-prod-hibernation -n production

# Output includes warning:
# ⚠️  Schedule Warning:
#   Exception "emergency-maintenance" starts at 2026-02-21T06:00Z
#   but overlaps with exception "holiday-break" that ends at 2026-02-22T14:00Z
#
#   The newer exception (holiday-break) takes precedence.
#   This is expected if intentional; otherwise review exceptions.
```

---

## Decision Branches

### Branch A: Evaluating Schedule Timezone

**Decision Point:** "Is 8 PM to 6 AM the right window?"

- **If in different timezone than plan**:
  - Use `--timezone` flag in `show schedule` to see events in your local time
  - Convert the schedule offset yourself: EST is UTC-5, PST is UTC-8

- **If considering DST (Daylight Saving Time) changes**:
  - CLI evaluates schedule across DST boundaries correctly
  - Example: 8 PM EST becomes 8 PM EDT on second Sunday of March (no manual adjustment needed)
  - Verify by `show schedule --next 100` to see events around DST transition dates

---

### Branch B: Evaluating If Schedule Meets Business Requirements

**Decision Point:** "Will this hibernation pattern match our business hours?"

| Question | Validation Command | Expected Check |
|----------|---|---|
| Do we hibernate weekdays only? | `show schedule --next 10` → Check that Saturday/Sunday are not in results | No hibernation events on weekends ✓ |
| Do we cross the midnight boundary? | `show schedule --next 5` → Check Hibernated times | If start > end (e.g., 20:00-06:00), midnight is crossed ✓ |
| Do we cover holiday closures? | `show schedule` → Check for active exceptions | Exceptions extend/suspend as expected ✓ |
| Is the window long enough? | `show schedule --next 3` → Calculate hours between Hibernating and Hibernated | Hours = (EndTime - StartTime) or (EndTime + 24h - StartTime) if crossing midnight |

---

### Branch C: Handling Schedule Modifications

**Decision Point:** "How do I test a schedule change safely?"

**Option 1: Modify YAML and validate before applying**

```bash
# Edit YAML locally
nano my-app-prod-hibernation.yaml
# Change: start: "22:00"  # Now 10 PM instead of 8 PM

# Validate the change
kubectl hibernator show schedule my-app-prod-hibernation.yaml

# If good, apply it
kubectl apply -f my-app-prod-hibernation.yaml
```

**Option 2: Use a test plan first**

```bash
# Create a test plan with new schedule
kubectl hibernator show schedule my-app-test-hibernation.yaml

# After validating test plan in production for a few days,
# apply the same schedule to the real plan
```

---

## Related Journeys

- [Hibernation Plan Initial Design](./hibernation-plan-initial-design.md) — Creating the initial plan
- [Create Emergency Exception](./create-emergency-exception.md) — Adding exceptions and validating their impact on schedule
- [Manage Multi-Environment Schedules](./manage-multi-environment-schedules.md) — Validating schedules across multiple clusters
- [Manage Hibernation via CLI Plugin](./manage-hibernation-via-cli.md) — Full CLI usage including schedule validation

---

## Pain Points Solved

1. **Schedule Validation Blindness**: Previously, mistakes only appeared after deployment. Now: validate before applying.
2. **Timezone Confusion**: Previously, manual conversion was error-prone. Now: CLI shows events in any timezone.
3. **Exception Impact Uncertainty**: Users couldn't see how exceptions changed the final schedule. Now: `show schedule` includes active exceptions.
4. **Silent Parsing Failures**: YAML could parse successfully but schedule could be invalid. Now: validation catches issues upfront.
5. **Business Hour Misalignment**: Teams deployed plans that didn't match business hours. Now: dry-run confirms alignment.

---

## RFC References

- [RFC-0007](../enhancements/0007-kubectl-hibernator-cli-plugin.md) — CLI plugin design (contains `show schedule` command specification)
- [RFC-0002](../enhancements/0002-schedule-format-migration.md) — Schedule format specification (start/end/daysOfWeek)
- [RFC-0003](../enhancements/0003-schedule-exceptions.md) — Exception system (exceptions interact with schedule validation)

---

## Implementation Notes

### Schedule Evaluation Logic

The CLI reuses the same schedule evaluation as the controller:

1. Parse `spec.schedule` (timezone, offHours windows, daysOfWeek)
2. Generate cron expression for the schedule
3. Query next N events from cron engine
4. Merge active `ScheduleException` resources
5. Sort and deduplicate events
6. Display with human-readable formatting

### Display Format

```
[Phase] → [Timestamp] ([DayOfWeek])
```

**Example**: `Hibernating → 2026-02-20 20:00 EST (Friday)`

---

## Success Criteria

- **Time to validate**: < 5 seconds per plan
- **Confidence**: Users report > 95% confidence in schedule correctness after validation
- **Issue prevention**: < 2% of deployed plans have schedule issues (vs. ~15% before CLI)
- **Adoption**: > 70% of teams use `show schedule` before first deployment of a plan
