# Suspend Hibernation During Incident

**Tier:** `[Enhanced]`

**Personas:** SRE, On-Call Engineer, Incident Commander

**When:** An incident requires services to remain awake during a hibernation window, with lead time to prevent mid-hibernation interruption

**Why:** Suspension exceptions prevent hibernation from starting, giving extra time to complete incident fixes.

---

## User Stories

**Story 1:** As an **SRE**, I want to **create a carve-out from hibernation to keep services awake during incidents**, so that **critical fixes aren't interrupted**.

---

## When/Context

- **Lead time:** Buffer period before suspension to prevent immediate hibernation restart
- **Safety:** Don't interrupt running hibernation; only prevent new hibernation from starting
- **Time-bound:** Automatically expires; no lingering impact
- **Clear communication:** Team knows exactly when suspension ends
- **GitOps-friendly:** Exception is independent CRD; base plan unchanged

---

## Business Outcome

Prevent hibernation from starting during incident windows, ensuring services remain available for debugging and fixes. Lead time prevents race conditions.

---

## Step-by-Step Flow

### 1. **Detect incident during hibernation**

```text
20:00 (hibernation starts) → Initial hibernation in progress
22:30 → Alert fired: "Database critical"
     → Team: "Need 2 hours to fix; keep services awake"
     → Create suspension: 22:30-00:30 with 1 hour lead time
```

### 2. **Create suspension exception with lead time**

Create a `ScheduleException` resource (independent CRD):

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: incident-db-corruption
  namespace: hibernator-system
spec:
  # Reference to the HibernatePlan
  planRef:
    name: prod-offhours

  # Exception period
  validFrom: "2026-02-01T21:30:00Z"   # Lead time starts here (1h before suspension)
  validUntil: "2026-02-02T00:30:00Z"

  # Exception type: suspend (prevent hibernation)
  type: suspend

  # Lead time: 1 hour buffer before suspension window
  leadTime: "1h"

  # Suspension window (when hibernation is actively prevented)
  windows:
    - start: "22:30"
      end: "00:30"
      daysOfWeek: ["Monday"]
```

Apply the exception:

```bash
kubectl apply -f scheduleexception-incident.yaml
```

### 3. **Lead time protection explained**

```text
Timeline:
20:00 → Hibernation begins (base schedule)
21:30 → Lead time window starts (22:30 - 1h)
       → Controller prevents ANY new hibernation from starting
       → If currently hibernating: remains hibernated
22:30 → Suspension window starts (explicit carve-out)
       → If paused during lead time: can't resume
00:30 → Suspension ends
       → If still needing time: create another exception
06:00 → Normal wakeup (base schedule)
```

### 4. **Verify lead time is active**

```bash
# Check ScheduleException status
kubectl get scheduleexception incident-db-corruption -n hibernator-system

# Detailed status
kubectl describe scheduleexception incident-db-corruption -n hibernator-system
```

Expected output:

```yaml
status:
  state: Active
  appliedAt: "2026-02-01T21:31:00Z"
  message: "Exception active (in lead time window), suspension starts in 59 minutes"
```

### 5. **Team debugs incident during suspension**

During suspension window (22:30-00:30):

- Services stay awake (no hibernation)
- Team has uninterrupted time to fix database
- Pod resources remain allocated
- No pod rescaling happening

### 6. **Suspension expires, services resume**

At 00:30:

```text
- Suspension ends
- Lead time ends
- Normal hibernation can resume (20:00-06:00)
- If incident not resolved: create another suspension
```

### 7. **Optional: Automatic wakeup if hibernating**

If services were already hibernated when suspension starts:

```text
Lead time period (21:30-22:30):
- Hibernation paused (no further scale-down)
- Resources remain in frozen state

Suspension period (22:30-00:30):
- Services can be manually restored (optional)
- OR left in hibernated state (consumes no resources)
- Team focuses on incident investigation

At 00:30:
- If still hibernating: remains hibernated
- Wakeup happens at 06:00 (base schedule)
```

---

## Advanced: Multiple Suspensions

For extended incident recovery, create sequential ScheduleExceptions:

```yaml
# Note: Single active exception per plan enforced
# Delete first exception before creating second

# Incident phase 1: Investigation (22:30-00:30)
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: incident-phase1
  namespace: hibernator-system
spec:
  planRef:
    name: prod-offhours
  type: suspend
  validFrom: "2026-02-01T21:30:00Z"
  validUntil: "2026-02-02T00:30:00Z"
  leadTime: "1h"
  windows:
    - start: "22:30"
      end: "00:30"
      daysOfWeek: ["Monday"]
---
# When phase 1 expires, create phase 2:
# Incident phase 2: Remediation (00:30-04:00)
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: incident-phase2
  namespace: hibernator-system
spec:
  planRef:
    name: prod-offhours
  type: suspend
  validFrom: "2026-02-02T00:00:00Z"
  validUntil: "2026-02-02T04:00:00Z"
  leadTime: "30m"  # Less lead time; team already responding
  windows:
    - start: "00:30"
      end: "04:00"
      daysOfWeek: ["Tuesday"]
```

Total incident window: 22:30 - 04:00 (5.5 hours). Services awake for full duration.

---

## Decision Branches

| Decision | Option | Notes |
| --- | --- | --- |
| **Lead time?** | 1 hour (recommended) | Prevents race conditions |
| | 30 minutes | Faster but higher risk |
| | None (no buffer) | Only if manual hibernation control in place |
| **Suspension duration?** | 1-2 hours | Typical incident |
| | 4+ hours | Major incident; consider dayshift handover |
| **After suspension?** | Wakeup at 06:00 (base schedule) | Normal behavior |
| | Create another suspension | Incident ongoing |

---

## Comparison: Suspend vs Extend

| Feature | Suspend | Extend |
| --- | --- | --- |
| **Purpose** | Prevent hibernation | Add hibernation windows |
| **Lead time** | Yes (prevents mid-hibernation) | No (add windows only) |
| **When to use** | Incident/emergency | Planned event |
| **Services state** | Stay awake | Hibernated |
| **Approval** | Often auto (emergency) | Often requires approval |

---

## Outcome

✓ Suspension active. Lead time prevents hibernation restart. Services remain available for incident resolution. Suspension auto-expires at 00:30.

---

## Related Journeys

- [Create Emergency Exception](create-emergency-exception.md) — Similar time-bound exception
- [Extend Hibernation for Event](extend-hibernation-for-event.md) — Opposite use case (add hibernation)

---

## Pain Points Solved

**RFC-0003:** Lead time parameter prevents race conditions where hibernation restarts mid-incident. Services guaranteed awake for full suspension period.

---

## RFC References

- **RFC-0003:** Temporary Schedule Exceptions and Overrides (suspend type, lead time, auto-expiration)
