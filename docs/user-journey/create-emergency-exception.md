# Create Emergency Exception

**Tier:** `[Enhanced]`

**Personas:** On-Call Engineer, SRE, Incident Commander

**When:** Unexpected incidents occur during hibernation windows that require services to remain online

**Why:** Emergency exceptions allow temporary overrides to hibernation schedules without recreating the entire plan.

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
- **Audit trail:** All exceptions logged for compliance

---

## Business Outcome

Quickly create a temporary exception to keep services awake during an incident, then let it automatically expire when no longer needed.

---

## Step-by-Step Flow

### 1. **Recognize incident requires exception**

During hibernation window, incident occurs:

```
22:00 (hibernation active) → Alert received
      → Incident commander: "Need to wake up services"
      → Create exception to suspend hibernation 22:00-23:30
```

### 2. **Create emergency exception**

```yaml
apiVersion: hibernator.ardikasaputro.io/v1alpha1
kind: HibernationPlan
metadata:
  name: prod-offhours
  namespace: hibernator-system
spec:
  schedule:
    timezone: "America/New_York"
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]

    # NEW: Add exception for incident
    exceptions:
      - name: "incident-2026-02-01"
        description: "Production incident - database corruption detected"
        type: suspend                    # Keep services awake during this window
        validFrom: "2026-02-01T22:00:00Z"
        validUntil: "2026-02-01T23:30:00Z"
        leadTime: "5m"                   # Don't start hibernation in last 5 min
        windows:
          - start: "22:00"
            end: "23:30"
            daysOfWeek: ["MON"]
```

### 3. **Verify exception is active**

```bash
kubectl describe hibernateplan prod-offhours | grep -A 10 "activeExceptions:"

# Output:
# Active Exceptions:
#   Name: incident-2026-02-01
#   Type: suspend
#   Reason: Active; 89 minutes remaining
```

### 4. **Services remain awake during exception**

With `type: suspend`:
- Hibernation **will not** start if within 5 minutes of suspension
- Currently hibernated services **remain** hibernated (exception only prevents new hibernation)
- Any new hibernation requests **are delayed** until exception expires

### 5. **Incident resolved → Exception expires**

When incident window ends:

```
23:30 (exception expires) → Controller removes exception
      → Schedule reverts to normal (hibernation continues)
      → Services continue in their current state
```

Exception automatically removed; no manual cleanup.

### 6. **If manual removal needed**

For emergency early removal:

```bash
# Edit plan
kubectl edit hibernateplan prod-offhours

# Find exception and delete it from spec.schedule.exceptions[]
# Save and exit

# Verify removed:
kubectl describe hibernateplan prod-offhours | grep activeExceptions
# Should be empty or no longer mention incident exception
```

---

## Advanced: Fast-Track Approval

If using exception approvals (RFC-0003):

```bash
# On-call creates exception with approval=required flag:
kubectl edit hibernateplan prod-offhours
# Set: exceptions[].approvalRequired: true

# Exception enters "Pending" state
# Engineering Head receives Slack DM

# Engineering Head approves:
hibernator exception approve incident-2026-02-01

# Exception transitions to "Active" → Services awake
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
