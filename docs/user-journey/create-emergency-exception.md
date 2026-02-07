# Create Emergency Exception

**Tier:** `[Enhanced]`

**Personas:** On-Call Engineer, SRE, Incident Commander

**When:** Unexpected incidents occur during hibernation windows that require services to remain online

**Why:** Emergency exceptions allow temporary overrides to hibernation schedules without modifying the base HibernatePlan.

---

> **👉 RFC-0003 Implementation Status**
> This journey covers features from **RFC-0003 Phase 1-3** (✅ Implemented):
> - Independent ScheduleException CRD
> - Exception types: extend, suspend, replace
> - Lead time for suspend type
> - Automatic expiration
>
> **NOT covered:** Approval workflow (Phase 4, future work)

---

## User Stories

**Story 1:** As an **On-Call Engineer**, I want to **quickly create an emergency exception during an incident**, so that **services remain available without delay**.

**Story 2:** As an **Incident Commander**, I want to **scope the exception to a specific time window**, so that **services automatically resume hibernation after the incident is resolved**.

**Story 3:** As a **Compliance Officer**, I want to **track all exceptions created and their reasons**, so that **I maintain an audit trail of schedule overrides**.

---

## When/Context

- **Emergency response:** Incident detected → Need immediate wakeup or prevent upcoming hibernation
- **Time-bound:** Exception automatically expires after incident resolved
- **Minimal friction:** Create exception quickly without approvals (or with fast-track approval)
- **Lead time buffer:** Prevent NEW hibernation starts just before a suspension window
- **Audit trail:** All exceptions logged as separate CRs for compliance
- **GitOps-friendly:** Exceptions are independent resources; base plan unchanged

---

## Business Outcome

Quickly create a temporary exception to keep services awake during an incident, then let it automatically expire when no longer needed. The base HibernatePlan remains unchanged.

---

## Step-by-Step Flow

### 1. **Recognize incident requires exception**

During hibernation window, incident occurs:

```
22:00 (hibernation active) → Alert received
      → Incident commander: "Need to wake up services"
      → Create ScheduleException to suspend hibernation 22:00-23:30
```

### 2. **Create emergency exception (Independent CRD)**

#### 📋 **Semantic Alert: Suspend Type Uses INVERTED Semantics**

**Critical difference from base schedule:**

| Context | `start` Semantic | `end` Semantic | Mindset |
|---------|-----------------|----------------|----------|
| **Base schedule** | ⬇️ Begin hibernation | ⬆️ Begin wakeup | Off hours (when to sleep) |
| **`suspend` exception** | ✋ Begin stay-awake | ✅ End stay-awake | **On hours (when to stay awake)** ⚠️ |

**In suspend exceptions, you define WHEN to STAY AWAKE (not when to sleep).**

Create a `ScheduleException` resource that references the HibernatePlan:

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: incident-2026-02-01
  namespace: hibernator-system
spec:
  # Reference to the HibernatePlan
  planRef:
    name: prod-offhours

  # Exception period
  validFrom: "2026-02-01T21:30:00Z"   # Lead time starts 1h before window
  validUntil: "2026-02-02T00:30:00Z"

  # Exception type: suspend (keep services awake)
  type: suspend

  # Lead time: don't start hibernation 1h before suspension
  leadTime: "1h"

  # Windows where hibernation is suspended
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

The `leadTime` parameter prevents race conditions where a hibernation cycle might start just as you're responding to an incident.

```text
Timeline Example:
20:00 → Hibernation begins (base schedule)
21:30 → Lead time window starts (22:30 - 1h)
       → Controller prevents ANY new hibernation from starting
       → If currently hibernating: remains hibernated
22:30 → Suspension window starts (explicit carve-out)
       → Resources are kept awake or restored if hibernated
00:30 → Suspension ends
06:00 → Normal wakeup (base schedule)
```

### 4. **Verify exception is active**

```bash
# Check ScheduleException status
kubectl get scheduleexception incident-2026-02-01 -n hibernator-system

# Detailed view
kubectl describe scheduleexception incident-2026-02-01 -n hibernator-system
```

Expected status during lead time:
```yaml
status:
  state: Active
  message: "Exception active (in lead time window), suspension starts in 59 minutes"
```

Expected status during suspension:
```yaml
status:
  state: Active
  appliedAt: "2026-02-01T22:01:15Z"
  message: "Exception active, expires in 89 minutes"
```

### 5. **Incident resolved → Exception expires automatically**

When incident window ends:

```
00:30 (exception expires) → Controller transitions state to Expired
      → Schedule reverts to normal (base hibernation resumes)
      → ScheduleException CR preserved for audit (state: Expired)
```

---

## Advanced: Multiple Suspensions

For extended incident recovery, create sequential ScheduleExceptions. Note that the system prevents temporal overlaps, so you must define them sequentially:

```yaml
# Incident phase 1: Investigation (22:30-00:30)
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: incident-phase1
spec:
  planRef: { name: prod-offhours }
  type: suspend
  validFrom: "2026-02-01T21:30:00Z"
  validUntil: "2026-02-02T00:30:00Z"
  leadTime: "1h"
  windows: [{ start: "22:30", end: "00:30", daysOfWeek: ["Monday"] }]
---
# Incident phase 2: Remediation (00:30-04:00)
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: incident-phase2
spec:
  planRef: { name: prod-offhours }
  type: suspend
  validFrom: "2026-02-02T00:30:00Z"
  validUntil: "2026-02-02T04:00:00Z"
  windows: [{ start: "00:30", end: "04:00", daysOfWeek: ["Tuesday"] }]
```

---

## Decision Branches

| Decision | Option | Notes |
| --- | --- | --- |
| **Lead time?** | 1 hour (recommended) | Prevents race conditions |
| | None (no buffer) | Only if manual hibernation control in place |
| **Duration?** | 1-2 hours | Typical incident resolution time |
| | Longer | Rare; for complex multi-system incidents |
| **Require approval?** | No (instant) | On-call can create immediately |
| | Yes (fast-track) | Manager approval (Future Work) |

---

## Outcome

✓ Exception created and active; services remain awake during incident. Exception automatically expires when incident resolved.

---

## Related Journeys

- [Monitor Hibernation Execution](monitor-hibernation-execution.md) — Monitor status during incident
- [Extend Hibernation for Event](extend-hibernation-for-event.md) — Add hibernation windows (opposite of suspend)

---

## Pain Points Solved

**RFC-0003:** Time-bound exceptions eliminate need to recreate entire HibernationPlan; lead time prevents race conditions where hibernation starts during response.

---

## RFC References

- **RFC-0003:** Temporary Schedule Exceptions and Overrides (suspend type, lead time, auto-expiration)