# Suspend Hibernation During Incident

**Tier:** `Enhanced`

**Personas:** On-Call Engineer, Incident Commander, SRE

**When:** Services are currently hibernated (or about to hibernate) and an unexpected incident requires them to stay online

**Why:** A `suspend` exception carves out a time-bound window from the effective hibernation schedule, keeping services awake until the incident is resolved — without touching the base HibernatePlan.

---

> **👉 RFC-0003 Implementation Status**
> This journey covers features from **RFC-0003 Phase 1-3 and Phase 5** (✅ Implemented):
> - Independent ScheduleException CRD with `type: suspend`
> - Lead time to prevent new hibernation cycles from starting before the suspension window
> - Automatic expiration — base schedule resumes when exception expires
> - Multiple active exceptions: a `suspend` can coexist with an active `extend` or `replace` exception (Phase 5 composability)
>
> **NOT covered:** Approval workflow (Phase 4+, future work)

---

## User Stories

**Story 1:** As an **On-Call Engineer**, I want to **immediately suspend hibernation for a named plan during an active incident**, so that **services remain available while the team investigates without manually restarting them after each hibernation cycle**.

**Story 2:** As an **Incident Commander**, I want to **set a lead-time buffer before the suspension window**, so that **a new hibernation cycle cannot start during my active response window**.

**Story 3:** As an **SRE**, I want the **suspension to expire automatically when the incident resolves**, so that **cost savings resume without manual cleanup**.

---

## When/Context

- **Services already hibernated** (or in hibernation window) when incident is declared
- **Suspension must activate immediately** — minimal delay between declaration and wakeup
- **Lead-time protection required** — prevent new hibernation from starting just before or during response
- **Auto-expiry mandatory** — base schedule must resume without manual intervention
- **Composable with extend exceptions** — an on-site event extension and a simultaneous incident suspension can coexist (Phase 5 allows `extend + suspend` overlap)

---

## Business Outcome

Hibernation is suppressed for the duration of the incident window. Services wake up (or remain awake) automatically. When the incident window ends the `ScheduleException` transitions to `Expired` and the base schedule resumes — no plan modification required.

---

## Step-by-Step Flow

### 1. **Declare incident and identify affected plan**

```bash
# Identify the HibernatePlan governing the affected resources
kubectl get hibernateplan -n hibernator-system

# Check current phase (may already be Hibernating or Hibernated)
kubectl get hibernateplan prod-offhours -n hibernator-system \
  -o jsonpath='{.status.phase}'
```

### 2. **Create the suspend exception**

#### ⚠️ Semantic Alert: Suspend Windows Define "Stay-Awake" Time

In a `suspend` exception, `start`/`end` define **when the system must stay awake**, not when to hibernate.

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: incident-$(date +%Y%m%d-%H%M)
  namespace: hibernator-system
spec:
  planRef:
    name: prod-offhours

  # Exception validity covers the entire incident window + lead time buffer.
  # Set validFrom BEFORE the suspension start to allow lead time to kick in.
  validFrom: "2026-03-25T21:00:00Z"    # lead time starts: suspend start - leadTime
  validUntil: "2026-03-26T03:00:00Z"

  type: suspend

  # Lead time: prevent new hibernation within 1h before suspension window opens.
  # Applies only to NEW hibernation cycles; ongoing hibernation continues.
  leadTime: "1h"

  windows:
    - start: "22:00"
      end: "03:00"
      daysOfWeek: ["Wednesday"]
```

```bash
kubectl apply -f incident-exception.yaml
```

### 3. **Verify exception becomes Active**

```bash
kubectl get scheduleexception incident-20260325-2100 -n hibernator-system
```

Expected output:
```
NAME                      STATE    APPLIED-AT             EXPIRES-AT
incident-20260325-2100    Active   2026-03-25T21:00:12Z   2026-03-26T03:00:00Z
```

Check on the plan — if it was `Hibernated`, the controller will initiate wakeup:

```bash
kubectl get hibernateplan prod-offhours -n hibernator-system \
  -o jsonpath='{.status.phase}'
# → WakingUp, then Active
```

### 4. **Understand lead-time behavior**

```text
Timeline:
21:00 → validFrom reached → exception enters Active state
        Lead time window: 22:00 − 1h = 21:00 → 22:00
        → Controller will NOT start a new hibernation cycle during 21:00-22:00
22:00 → Suspension window opens
        → Plan must be Active; if Hibernated, wakeup job is triggered
03:00 → Suspension window closes → exception expires
        → Base 20:00-06:00 schedule resumes at next reconcile
```

> **Key distinction**: Lead time only blocks *new* hibernation starts. If the plan was already
> `Hibernated` before 21:00 (hibernation started before the lead time window), the controller
> will still wake it up when the suspension window opens at 22:00.

### 5. **Monitor during incident**

```bash
# Watch plan phase transitions in real time
kubectl get hibernateplan prod-offhours -n hibernator-system -w

# Check exception status and message
kubectl describe scheduleexception incident-20260325-2100 -n hibernator-system
```

During lead time:
```yaml
status:
  state: Active
  message: "Lead time active: 1 hibernation window suppressed, suspension starts in 42 minutes"
```

During suspension window:
```yaml
status:
  state: Active
  appliedAt: "2026-03-25T21:00:12Z"
  message: "Exception active, expires in 4h 47m"
```

### 6. **Incident resolved — exception expires automatically**

```
03:00 UTC → validUntil reached → controller transitions exception to Expired
          → Base 20:00-06:00 schedule re-evaluated at next reconcile
          → ScheduleException CR preserved with state: Expired (audit trail)
```

No manual deletion required. The expired CR serves as a permanent audit record.

---

## Composability with Other Exceptions (Phase 5)

If the plan already has an active `extend` exception (e.g., an on-site event added 09:00–18:00 windows), a `suspend` exception can coexist with it provided their windows do not collide — or they share the *allowed cross-type pair* rule:

| Pair | Allowed | Reason |
|------|---------|--------|
| `suspend` + `extend` | ✅ | Suspend carves out safety window from extended hibernation |
| `suspend` + `replace` | ✅ | Replace takes full ownership; suspend carves out sub-windows |
| `suspend` + `suspend` | ❌ | Two overlapping suspensions are redundant; webhook rejects |

**Example**: extend adds 09:00–18:00 hibernation AND suspend blocks 11:00–14:00 within it:

```yaml
# Both coexist — webhook allows it, evaluator applies suspend carve-out over extend
# Effective schedule: hibernate 09:00-11:00 and 14:00-18:00 (11:00-14:00 stays awake)
```

---

## Decision Branches

| Decision | Option | Notes |
|----------|--------|-------|
| **Lead time?** | `"1h"` (recommended) | Prevents race conditions near window boundary |
| | `""` (none) | Only if plan is already confirmed Active at incident time |
| **Window duration?** | Incident window + 15 min buffer | Always over-provision; unused time has no cost |
| | Rolling extension | Delete + recreate exception to extend; back-to-back validFrom/validUntil |
| **Multiple incidents?** | Sequential exceptions | Each `validFrom` must start ≥ previous `validUntil` (same-type collision prevention) |
| | Overlapping windows? | Use `extend + suspend` composition if additional hibernation is needed alongside |

---

## Related Journeys

- [Create Emergency Exception](create-emergency-exception.md) — Broad overview of all emergency exception types (suspend, extend, replace)
- [Extend Hibernation for Event](extend-hibernation-for-event.md) — Add planned hibernation windows (opposite intent to suspend)
- [Monitor Hibernation Execution](monitor-hibernation-execution.md) — Track wakeup job progress during incident response

---

## Pain Points Solved

- **No plan modification required**: Teams respond in seconds without coordinator approval to change the base plan
- **Lead time guard**: Eliminates race conditions where a hibernation job starts mid-response
- **Auto-expiry**: Base cost-saving schedule automatically resumes; no cleanup toil
- **Audit trail**: Expired `ScheduleException` CRs provide a timestamped incident record

---

## RFC References

- [RFC-0003](../enhancements/0003-schedule-exceptions.md) — Temporary Schedule Exceptions; suspend type, lead time, auto-expiration, Phase 5 composability (extend+suspend allowed pair)
