# Extend Hibernation for Event

**Tier:** `[Enhanced]`

**Personas:** Team Lead, Product Manager, Project Manager

**When:** Special events or projects require extended hibernation beyond normal schedule (e.g., on-site event, sprint push)

**Why:** Temporary schedule extensions allow cost savings to continue during unusual scheduling periods without modifying the base plan.

---

## User Stories

**Story 1:** As a **Team Lead**, I want to **extend hibernation for on-site events or sprint push**, so that **cost savings continue during the event period**.

**Story 2:** As a **Product Manager**, I want to **specify exactly when the extension applies**, so that **hibernation reverts automatically when the event ends**.

**Story 3:** As a **Finance Manager**, I want to **track cost impact of extensions**, so that **I can understand extended hibernation's ROI**.

---

## When/Context

- **Temporary scope:** Extension is time-bound and expires automatically
- **Business-driven:** Non-technical reason (event, sprint, holiday)
- **Preserve base schedule:** Don't modify the underlying HibernationPlan
- **Automatic cleanup:** No manual removal needed

---

## Business Outcome

Automatically extend hibernation windows during special events or project periods, then revert to normal schedule when event ends.

---

## Step-by-Step Flow

### 1. **Plan the extended hibernation**

Identify when extra hibernation is needed:

```
Scenario: On-site event (Jan 29 - Feb 28)
- Normal hibernation: weekday nights (20:00-06:00)
- Extended hibernation: weekends 06:00-11:00 (support different time zone team)
- Extended hibernation: weekday mornings 01:00-06:00 (early support)
- Duration: 1 month (auto-expires Feb 28)
```

### 2. **Create ScheduleException resource**

Create independent `ScheduleException` that references the HibernatePlan:

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: on-site-event-q1
  namespace: hibernator-system
spec:
  # Reference to the HibernatePlan
  planRef:
    name: event-support
    namespace: hibernator-system  # Must be same namespace

  # Exception period
  validFrom: "2026-01-29T00:00:00Z"
  validUntil: "2026-02-28T23:59:59Z"

  # Exception type: extend (add hibernation windows)
  type: extend

  # Windows to add to base schedule
  windows:
    # Weekend support (normally awake, now hibernated)
    - start: "06:00"
      end: "11:00"
      daysOfWeek: ["Saturday", "Sunday"]

    # Early-morning support (additional hibernation)
    - start: "01:00"
      end: "06:00"
      daysOfWeek: ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday"]
```

**GitOps-Friendly Workflow:**

- Base HibernatePlan remains unchanged
- Exception is separate commit/resource
- Auto-expires without modifying plan
- Audit trail preserved as CR with `state: Expired`

### 3. **Apply the exception**

Commit ScheduleException as separate resource (GitOps-friendly):

```bash
# Apply ScheduleException (separate from plan)
kubectl apply -f scheduleexception-on-site-event-q1.yaml

# Verify exception is Active
kubectl get scheduleexception on-site-event-q1 -n hibernator-system

# Check exception status
kubectl get scheduleexception on-site-event-q1 -n hibernator-system -o jsonpath='{.status.state}'
# Expected: Active
```

### 4. **Verify exception activation**

Controller processes exception and updates both resources:

```bash
# Check ScheduleException status
kubectl describe scheduleexception on-site-event-q1 -n hibernator-system
```

Expected ScheduleException status:

```yaml
status:
  state: Active
  appliedAt: "2026-01-29T00:05:23Z"
  message: "Exception active, expires in 29 days"
```

Check HibernatePlan status (history tracking):

```bash
kubectl get hibernateplan event-support -n hibernator-system -o jsonpath='{.status.activeExceptions}' | jq
```

Expected plan status:

```json
[
  {
    "name": "on-site-event-q1",
    "type": "extend",
    "validFrom": "2026-01-29T00:00:00Z",
    "validUntil": "2026-02-28T23:59:59Z",
    "state": "Active",
    "appliedAt": "2026-01-29T00:05:23Z"
  }
]
```

### 5. **Schedule during extension**

During the extension period (Jan 29 - Feb 28):

**Before extension (baseline):**

```text
MON-FRI 20:00-06:00 → Services hibernated
SAT-SUN 06:00-11:00 → Services running
```

**During extension:**

```text
MON-FRI 01:00-06:00  → ADDITIONALLY hibernated (extended window)
MON-FRI 20:00-06:00  → Services hibernated (base schedule)
SAT-SUN 06:00-11:00  → ADDITIONALLY hibernated (extended window)
```

**Effective hibernation:**

```text
MON-FRI: 20:00 → 06:00 (base) + 01:00 → 06:00 (extended) = 20:00 → 06:00 (union)
SAT-SUN: 06:00 → 11:00 (extended only)
```

### 6. **Automatic expiration**

On Feb 28, 23:59:59 UTC, the exception automatically expires:

```bash
# After expiration, check state
kubectl get scheduleexception on-site-event-q1 -n hibernator-system -o jsonpath='{.status.state}'
# Expected: Expired

# ScheduleException CR remains for audit
kubectl get scheduleexception on-site-event-q1 -n hibernator-system -o yaml
```

Expected status after expiration:

```yaml
status:
  state: Expired
  appliedAt: "2026-01-29T00:05:23Z"
  expiredAt: "2026-02-28T23:59:59Z"
  message: "Exception expired"
```

HibernatePlan status updates (moves to history):

```bash
kubectl get hibernateplan event-support -n hibernator-system -o jsonpath='{.status.activeExceptions}' | jq
```

```json
[
  {
    "name": "on-site-event-q1",
    "type": "extend",
    "validFrom": "2026-01-29T00:00:00Z",
    "validUntil": "2026-02-28T23:59:59Z",
    "state": "Expired",
    "appliedAt": "2026-01-29T00:05:23Z",
    "expiredAt": "2026-02-28T23:59:59Z"
  }
]
```

**Audit Trail**: Exception CR preserved with `state: Expired` for cost tracking and compliance.

### 5. **Verify extended hibernation is working**

Monitor status:

```bash
kubectl describe hibernateplan event-support | grep -A 30 "status.executions"

# Should show extended hibernation windows being executed
# Example:
# status.phase: Hibernated
# executions:
#   - target: staging-cluster
#     state: Completed
#     message: "Hibernated during extended window (06:00-11:00 SAT)"
```

### 6. **Extension expires automatically**

On Feb 28, 23:59:59:

```bash
# Feb 28 23:59:59 → Exception expires
# Feb 29 00:00:00 → Schedule reverts to base

# After expiration:
kubectl describe hibernateplan event-support | grep "activeExceptions"
# Should show: (no active exceptions) or exception moved to expiredExceptions

# Next hibernation window (Mar 1 20:00):
# - Uses BASE schedule only (no more extended windows)
# - SAT-SUN hibernation ends
# - Early morning 01:00-06:00 extension ends
```

---

## Multi-Event Example

Multiple overlapping extensions:

```yaml
exceptions:
  # Event 1: On-site conference (Feb 1-28)
  - name: "conference-2026"
    type: "extend"
    validFrom: "2026-02-01T00:00:00Z"
    validUntil: "2026-02-28T23:59:59Z"
    windows:
      - start: "06:00"
        end: "11:00"
        daysOfWeek: ["SAT", "SUN"]

  # Event 2: Quarterly sprint push (Feb 15-28)
  - name: "q1-sprint-push"
    type: "extend"
    validFrom: "2026-02-15T00:00:00Z"
    validUntil: "2026-02-28T23:59:59Z"
    windows:
      - start: "06:00"
        end: "18:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]

# During Feb 1-14: Conference extended windows active
# During Feb 15-28: Both conference AND sprint windows active (union)
# After Feb 28: Revert to base schedule
```

---

## Decision Branches

| Decision | Option | Notes |
| --- | --- | --- |
| **Duration?** | 1-2 weeks | Short event |
| | 1 month | Typical on-site event |
| | Longer | Rare; unusual circumstances |
| **Extend all resources?** | Yes (all targets) | Simpler; entire plan in same scope |
| | Selective (some targets) | Complex; requires multiple exceptions |

---

## Outcome

✓ Hibernation extended during event. Services hibernated per extended schedule. Exception auto-expires; schedule reverts to normal on Feb 28.

---

## Related Journeys

- [Create Emergency Exception](create-emergency-exception.md) — Similar time-bound exception pattern
- [Suspend Hibernation During Incident](suspend-hibernation-during-incident.md) — Opposite use case (carve-out)

---

## Pain Points Solved

**RFC-0003:** Time-bound extensions eliminate need to modify base HibernationPlan. Automatic expiration prevents stale extensions consuming budget long after event ends.

---

## RFC References

- **RFC-0003:** Temporary Schedule Exceptions and Overrides (extend type, auto-expiration, multi-window support)
