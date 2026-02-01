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

### 2. **Create extension exception**

```yaml
apiVersion: hibernator.ardikasaputro.io/v1alpha1
kind: HibernationPlan
metadata:
  name: event-support
  namespace: hibernator-system
spec:
  schedule:
    timezone: "America/New_York"
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]

    # Extend hibernation during on-site event
    exceptions:
      - name: "on-site-event-q1"
        description: "Q1 On-site event support (Jan 29 - Feb 28, 2026)"
        type: extend                    # ADD hibernation windows (union with base)
        validFrom: "2026-01-29T00:00:00Z"
        validUntil: "2026-02-28T23:59:59Z"
        windows:
          - start: "06:00"
            end: "11:00"
            daysOfWeek: ["SAT", "SUN"]  # Weekend extended hibernation
          - start: "01:00"
            end: "06:00"
            daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]  # Early morning extension

  targets: [...]
```

### 3. **Verify exception is active**

```bash
kubectl describe hibernateplan event-support | grep -A 10 "activeExceptions:"

# Output:
# Active Exceptions:
#   Name: on-site-event-q1
#   Type: extend
#   Reason: Active; 28 days remaining
```

### 4. **Schedule during extension**

During the extension period (Jan 29 - Feb 28):

**Before extension (baseline):**
```
MON-FRI 20:00-06:00 → Services hibernated
SAT-SUN 06:00-11:00 → Services running
```

**During extension:**
```
MON-FRI 01:00-06:00  → ADDITIONALLY hibernated (extended window)
MON-FRI 20:00-06:00  → Services hibernated (base schedule)
SAT-SUN 06:00-11:00  → ADDITIONALLY hibernated (extended window)
```

**Effective hibernation:**
```
MON-FRI: 20:00 → 06:00 (base) + 01:00 → 06:00 (extended) = 20:00 → 06:00 (union)
SAT-SUN: 06:00 → 11:00 (extended only)
```

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
