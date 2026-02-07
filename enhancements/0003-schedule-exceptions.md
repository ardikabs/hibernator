---
rfc: RFC-0003
title: Temporary Schedule Exceptions via Independent CRD
status: Implemented (Phase 1-3)
date: 2026-01-29
updated: 2026-02-02
---

# RFC 0003 — Temporary Schedule Exceptions via Independent CRD

**Keywords:** Schedule-Exceptions, Maintenance-Windows, Lead-Time, Time-Bound, Extend, Suspend, Replace, Emergency-Events, Validation, Status-Tracking, Independent-CRD, GitOps

**Status:** Implemented ✅ (Phase 1-3 Complete, Phase 4 Pending)

## Summary

Introduce `ScheduleException` as an independent CRD that references HibernatePlan to enable temporary schedule deviations. This design separates exception lifecycle from plan lifecycle, enabling GitOps-friendly temporary schedule modifications without modifying the base HibernatePlan. A strict temporal overlap prevention constraint ensures predictable behavior and simplifies merge semantics.

## Motivation

In real-world scenarios, infrastructure needs fluctuate:

- **Emergency events**: Maintenance windows, incidents requiring all services online
- **Temporary workload changes**: Special projects, team events, customer engagements
- **Seasonal adjustments**: Holiday periods, sprint cycles
- **Regional team support**: Supporting offshore teams across different time zones

**Example use case**:

A team normally hibernates services 20:00-06:00 on weekdays. However, for the next month, they're supporting an on-site event:

- Saturday 06:00-11:00: Services must remain active (normally hibernated)
- Sunday 06:00-11:00: Services must remain active (normally hibernated)
- Weekdays 01:00-06:00: Additional early-morning support window
- After 1 month: Revert to normal schedule automatically

**Current limitations**:

- No way to override schedule without recreating HibernatePlan
- Manual intervention required to pause/resume hibernation
- No time-bound exception mechanism
- Embedded exceptions in HibernatePlan complicate GitOps workflow (every temporary change modifies plan spec)

## Goals

- Enable temporary schedule exceptions via independent CRD (not embedded in HibernatePlan)
- Support three exception types: "extend" (add windows), "suspend" (carve-out with lead time), "replace" (full override)
- Enforce temporal overlap prevention for predictable behavior
- Provide lead time configuration for suspensions to prevent mid-process hibernation interruption
- Automatically expire exceptions to prevent stale overrides
- Provide clear visibility into exception history via status tracking
- Enable GitOps-friendly workflow where exceptions are separate commits from plan changes

## Non-Goals

- Support multiple simultaneous active exceptions per plan (use single active exception for simplicity)
- Support infinite exceptions (time-bound only, max 90 days)
- Implement approval workflow in initial version (designed for future extension)

## Proposal

This RFC proposes introducing `ScheduleException` as a new independent CRD that references `HibernatePlan` via `planRef`. This design offers several advantages over embedding exceptions directly in the HibernatePlan spec:

**Design Rationale:**

1. **GitOps-Friendly**: Temporary exceptions don't modify the base plan. Teams can commit exceptions separately and they auto-expire without plan changes.
2. **Clear Ownership**: Exceptions have independent lifecycle. Creation, expiration, and deletion don't trigger plan spec changes.
3. **Audit Trail**: Old exceptions remain as CRs with `state: Expired` for compliance and cost tracking.
4. **RBAC Flexibility**: Teams can grant exception-creation permissions without allowing plan modification.
5. **Simple Semantics**: Temporal overlap prevention eliminates complex merge logic and ordering concerns.

### CRD Design

#### ScheduleException Spec

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: on-site-event-override
  namespace: hibernator-system  # Must match HibernatePlan namespace
  labels:
    hibernator.ardikabs.com/plan: event-support  # Auto-set by controller
spec:
  # Reference to the HibernatePlan this exception applies to
  planRef:
    name: event-support
    namespace: hibernator-system  # Optional, defaults to exception namespace

  # Exception period
  validFrom: "2026-01-29T00:00:00Z"
  validUntil: "2026-02-28T23:59:59Z"

  # Exception type: extend, suspend, or replace
  type: extend

  # Lead time (only for suspend type)
  # Prevents NEW hibernation starts within this buffer before suspension window
  leadTime: "1h"  # Format: duration string (e.g., "30m", "1h", "3600s")

  # Schedule windows (meaning depends on type)
  windows:
    - start: "06:00"
      end: "11:00"
      daysOfWeek: ["Saturday", "Sunday"]
    - start: "01:00"
      end: "06:00"
      daysOfWeek: ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday"]
```

#### HibernatePlan Status Extension

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: event-support
spec:
  schedule:
    timezone: "America/New_York"
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday"]
  targets: [...]

status:
  phase: "Active"

  # NEW: Exception history (max 10 entries, expired pruned first)
  activeExceptions:
    - name: "on-site-event-override"
      type: "extend"
      validFrom: "2026-01-29T00:00:00Z"
      validUntil: "2026-02-28T23:59:59Z"
      state: "Active"
      appliedAt: "2026-01-29T00:05:23Z"
    - name: "holiday-week-2025"
      type: "replace"
      validFrom: "2025-12-24T00:00:00Z"
      validUntil: "2025-12-31T23:59:59Z"
      state: "Expired"
      appliedAt: "2025-12-24T00:02:11Z"
      expiredAt: "2025-12-31T23:59:59Z"
```

### Exception Types

#### 1. Extend (`type: extend`)

**Meaning**: Apply exception windows IN ADDITION to the base `offHours`

**Use case**: "Hibernate during these additional times (e.g., weekend support, early morning)"

**Behavior**: Union of base `offHours` + exception windows

**Example**:

```yaml
type: extend
windows:
  - start: "06:00"
    end: "11:00"
    daysOfWeek: ["Saturday", "Sunday"]
```

**Effect**: If base hibernates Mon-Fri 20:00-06:00, exception adds Sat-Sun 06:00-11:00 hibernation.

#### 2. Suspend (`type: suspend`)

**Meaning**: Prevent hibernation during this window (carve-out from hibernation)

**Use case**: "Keep services awake during this window (e.g., maintenance, incident response, deployment)"

**Behavior**: Subtract exception windows from the combined hibernation schedule

**Lead Time**: Specifies buffer period before suspension begins where hibernation should NOT start

- Default: "" (no buffer)
- Format: Duration string (e.g., "30m", "1h", "3600s")
- Example: `leadTime: "1h"` → Don't start hibernation within 1 hour before suspension window

**Critical Edge Case**: Lead time only prevents **NEW hibernation starts**. If hibernation already began before the lead time window, it continues normally.

**Example**:

```yaml
type: suspend
leadTime: "1h"
windows:
  - start: "21:00"
    end: "02:00"
    daysOfWeek: ["Saturday"]
```

**Timeline**:

```
19:00: Normal operations (not in hibernation window)
20:00: Base schedule says hibernate, but lead time active (20:00-21:00)
       → DON'T start hibernation (reschedule check for 02:00)
21:00-02:00: Suspension window active → Stay awake
02:00: Suspension ended, lead time passed → Hibernation can start

Alternative scenario (hibernation already started):
18:00: Hibernation started (before lead time window)
20:00-21:00: Lead time window → No effect (hibernation already running)
21:00: Suspension starts → Wake up resources
```

#### 3. Replace (`type: replace`)

**Meaning**: Completely replace base schedule during exception period

**Use case**: "Temporary schedule change (e.g., holiday mode, different timezone support)"

**Behavior**: Use ONLY exception windows during valid period, ignore base `offHours`

**Example**:

```yaml
type: replace
validFrom: "2026-12-24T00:00:00Z"
validUntil: "2026-12-31T23:59:59Z"
windows:
  - start: "00:00"
    end: "23:59"
    daysOfWeek: ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"]
```

**Effect**: During Dec 24-31, ignore normal weekday schedule and hibernate 24/7.

### Reference-Based Association

ScheduleException and HibernatePlan are linked via:

1. **planRef** in exception spec (explicitly names the plan)
2. **Label** `hibernator.ardikabs.com/plan: <plan-name>` (auto-set by controller for querying)
3. **Same namespace constraint** (enforced by webhook)
4. **Status tracking** in HibernatePlan (maintains history of active/expired exceptions)

**No owner reference**: Exceptions are independent resources. Manual deletion removes CR immediately; automatic expiration keeps CR with `state: Expired` for audit.

### Schedule Evaluation Semantics

**Controller evaluates schedule with newest active exception:**

1. Load HibernatePlan base schedule (`offHours`)
2. Query for active ScheduleExceptions (label selector `hibernator.ardikabs.com/plan=<name>` + `state=Active`)
3. Filter for those within valid period (`validFrom` < `now` < `validUntil`)
4. If multiple active exceptions exist (e.g. webhook bypassed), **deterministic selection** picks the newest one by `CreationTimestamp` (Latest Intent Wins).
5. Apply selected exception:
   - **Extend**: Merge exception windows with base windows (union)
   - **Suspend**: Check if current time is within lead time window OR suspension window
     - If in lead time window (suspension start - leadTime) → Prevent NEW hibernation starts
     - If in suspension window → Remove from hibernation schedule (keep awake)
     - **Note**: Ongoing hibernation at lead time window start continues normally
   - **Replace**: Use ONLY exception windows, ignore base schedule
6. Evaluate effective schedule against current time
7. Update `status.activeExceptions[]` history (max 10, prune expired first)

### Temporal Overlap Prevention

**Rule**: Only ONE exception (Active or Pending) allowed to cover any specific point in time per HibernatePlan.

**Rationale**:

- Simplifies merge semantics (no complex ordering or precedence rules)
- Clear intent (explicit override, not layered modifications)
- Predictable behavior (users know exactly what schedule is active)

**Enforcement**:

- Webhook validation rejects new exception creation if its time range `[validFrom, validUntil]` overlaps with ANY existing non-expired exception for the same plan.
- Overlap detection logic: `start1 < end2 AND start2 < end1`.
- User must:
  - Wait for current exception to expire (controller transitions to Expired)
  - OR adjust time ranges to be sequential
  - OR manually delete the conflicting exception

### Validation Rules

**Webhook validation enforces**:

1. `planRef.name` must reference existing HibernatePlan
2. `planRef.namespace` must equal exception namespace (permanent same-namespace constraint)
3. No temporal overlap between non-expired exceptions for the same plan
4. `validFrom <= validUntil`
5. `validUntil - validFrom <= 90 days` (maximum exception duration)
6. `type` must be one of: `extend`, `suspend`, `replace`
7. For `suspend` type: `leadTime` must be valid duration format (or empty)
8. `windows[]` must follow OffHourWindow format (HH:MM time, valid day names)
9. Exception name must be unique within namespace

### Controller Implementation

**ScheduleException Controller**:

1. **On Create/Update**:
   - Determine state (`Pending`, `Active`, `Expired`) declaratively based on `now` vs `validFrom/validUntil`
   - Add label `hibernator.ardikabs.com/plan: <planRef.name>`
   - Trigger HibernatePlan reconciliation on state transition

2. **On Reconcile** (periodic):
   - Re-evaluate desired state based on current time
   - Schedule requeue at `validFrom` (to activate) or `validUntil` (to expire)
   - Update `message` field with diagnostic info (e.g. "activates in 2 days")

3. **On Delete** (finalizer):
   - Update HibernatePlan status to remove exception from `activeExceptions[]`
   - Clean up label references
   - Allow deletion to proceed

## Future Considerations: Exception Approval Workflow

Temporary schedule exceptions can significantly impact infrastructure availability and cost. Adding an approval workflow is a potential requirement for enterprise-scale deployments.

### Concept Overview

- **State Machine Extension**: `Pending Approval → Approved → Active`.
- **Stakeholders**: On-Call creates, Manager/Head-of-Engineering approves.
- **Integration Ideas**:
    - **Slack Integration**: Interactive DMs with [Approve/Reject] buttons. Approvers looked up via email in LDAP/Slack directory.
    - **SSO/URL-based**: Controller generates a unique authenticated OIDC link for approvers.
    - **kubectl plugin**: `kubectl hibernator exception approve <name>`.
- **Requirements**:
    - Complete audit trail in `status` (who, when, what channel).
    - Multi-tenant isolation (managers only see exceptions for their teams).

*This feature is currently not implemented and is being considered for Phase 4+ based on user feedback.*

## Migration

ScheduleException is a new CRD. No breaking changes to existing HibernatePlans.
