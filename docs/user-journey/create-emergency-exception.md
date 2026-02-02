# Create Emergency Exception

**Tier:** `[Enhanced]`

**Personas:** On-Call Engineer, SRE, Incident Commander

**When:** Unexpected incidents occur during hibernation windows that require services to remain online

**Why:** Emergency exceptions allow temporary overrides to hibernation schedules without modifying the base HibernatePlan.

---

## User Stories

**Story 1:** As an **On-Call Engineer**, I want to **quickly create an emergency exception during an incident**, so that **services remain available without delay**.

**Story 2:** As an **Incident Commander**, I want to **scope the exception to a specific time window**, so that **services automatically resume hibernation after the incident is resolved**.

**Story 3:** As a **Compliance Officer**, I want to **track all exceptions created and their reasons**, so that **I maintain an audit trail of schedule overrides**.

---

## When/Context

- **Emergency response:** Incident detected → Need immediate wakeup
- **Time-bound:** Exception automatically expires after incident resolved
- **Minimal friction:** Create exception quickly without approvals (or with fast-track approval)
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
  validFrom: "2026-02-01T22:00:00Z"
  validUntil: "2026-02-01T23:30:00Z"

  # Exception type: suspend (keep services awake)
  type: suspend

  # Lead time: don't start hibernation 5 min before suspension
  leadTime: "5m"

  # Windows where hibernation is suspended
  windows:
    - start: "22:00"
      end: "23:30"
      daysOfWeek: ["Monday"]
```

Apply the exception:

```bash
kubectl apply -f scheduleexception-incident.yaml
```

**Key Design:** Base HibernatePlan is NOT modified. Exception is a separate resource with its own lifecycle.

### 3. **Verify exception is active**

```bash
# Check ScheduleException status
kubectl get scheduleexception incident-2026-02-01 -n hibernator-system

# Detailed view
kubectl describe scheduleexception incident-2026-02-01 -n hibernator-system
```

Expected output:

```yaml
status:
  state: Active
  appliedAt: "2026-02-01T22:01:15Z"
  message: "Exception active, expires in 89 minutes"
```

Check HibernatePlan status (exception history):

```bash
kubectl get hibernateplan prod-offhours -n hibernator-system -o jsonpath='{.status.activeExceptions}' | jq
```

```json
[
  {
    "name": "incident-2026-02-01",
    "type": "suspend",
    "state": "Active",
    "validFrom": "2026-02-01T22:00:00Z",
    "validUntil": "2026-02-01T23:30:00Z",
    "appliedAt": "2026-02-01T22:01:15Z"
  }
]
```

### 4. **Services remain awake during exception**

With `type: suspend`:
- Hibernation **will not** start if within lead time window (5 min before suspension)
- Currently hibernated services **remain** hibernated (exception only prevents new hibernation)
- Any new hibernation requests **are delayed** until exception expires

### 5. **Incident resolved → Exception expires automatically**

When incident window ends:

```
23:30 (exception expires) → Controller transitions state to Expired
      → Schedule reverts to normal (base hibernation resumes)
      → ScheduleException CR preserved for audit (state: Expired)
```

Exception expires automatically; no manual cleanup needed. The CR remains for compliance.

### 6. **If manual removal needed**

For emergency early removal:

```bash
# Delete the exception CR
kubectl delete scheduleexception incident-2026-02-01 -n hibernator-system

# Verify removed from plan status
kubectl get hibernateplan prod-offhours -n hibernator-system -o jsonpath='{.status.activeExceptions}'
# Should be empty or no longer mention incident exception
```

---

## Advanced: Fast-Track Approval (Future)

Approval workflow is planned for future implementation (RFC-0003 Phase 4):

```bash
# Future: On-call creates exception with approval required
# Engineering Head receives Slack DM
# Engineering Head approves via Slack button
# Exception transitions to "Active"
```

---

## Decision Branches

| Decision | Option | Notes |
| --- | --- | --- |
| **Exception type?** | suspend | Keep services awake (no hibernation start) |
| | extend | Add hibernation windows (unusual for incidents) |
| **Duration?** | 1-2 hours | Typical incident resolution time |
| | Longer | Rare; for complex multi-system incidents |
| **Require approval?** | No (instant) | On-call can create immediately |
| | Yes (fast-track) | Manager approval in Slack (1-2 min) |

---

## Outcome

✓ Exception created and active; services remain awake during incident. Exception automatically expires when incident resolved.

---

## Related Journeys

- [Approve Exception via Slack](approve-exception-via-slack.md) — Manager approval workflow
- [Monitor Hibernation Execution](monitor-hibernation-execution.md) — Monitor status during incident
- [Suspend Hibernation During Incident](suspend-hibernation-during-incident.md) — Similar use case with lead time

---

## Pain Points Solved

**RFC-0003:** Time-bound exceptions eliminate need to recreate entire HibernationPlan; automatic expiration prevents forgotten manual cleanups.

---

## RFC References

- **RFC-0003:** Temporary Schedule Exceptions and Overrides (suspend type, time-bound, auto-expiration)
