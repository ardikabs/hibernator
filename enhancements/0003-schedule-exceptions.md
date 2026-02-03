<!--
RFC: 0003
Title: Temporary Schedule Exceptions via Independent CRD
Author: Hibernator Team
Status: Implemented (Phase 1-3)
Date: 2026-01-29
Updated: 2026-02-02
-->

# RFC 0003 — Temporary Schedule Exceptions via Independent CRD

**Keywords:** Schedule-Exceptions, Maintenance-Windows, Lead-Time, Time-Bound, Extend, Suspend, Replace, Emergency-Events, Validation, Status-Tracking, Independent-CRD, GitOps

**Status:** Implemented ✅ (Phase 1-3 Complete, Phase 4 Pending)

## Summary

Introduce `ScheduleException` as an independent CRD that references HibernatePlan to enable temporary schedule deviations. This design separates exception lifecycle from plan lifecycle, enabling GitOps-friendly temporary schedule modifications without modifying the base HibernatePlan. Single active exception per plan constraint simplifies merge semantics and ensures clear intent.

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
- Enforce single active exception per plan for predictable behavior
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
5. **Simple Semantics**: Single active exception per plan eliminates complex merge logic and ordering concerns.

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

  # Future: Approval workflow fields (commented for MVP)
  # approvalRequired: true
  # approverEmails:
  #   - "manager@company.com"
  #   - "cto@company.com"

status:
  # Current exception state
  state: Active  # Active | Expired

  # Timestamps
  appliedAt: "2026-01-29T00:05:23Z"
  expiredAt: null  # Set when state transitions to Expired

  # Diagnostic message
  message: "Exception active, expires in 29 days"

  # Future: Approval tracking
  # approvalState: Pending | Approved | Rejected
  # approvals: []
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

**Controller evaluates schedule with single active exception:**

1. Load HibernatePlan base schedule (`offHours`)
2. Query for active ScheduleException (label selector `hibernator.ardikabs.com/plan=<name>` + `state=Active`)
3. If no active exception found → Use base schedule only
4. If active exception found:
   - **Extend**: Merge exception windows with base windows (union)
   - **Suspend**: Check if current time is within lead time window OR suspension window
     - If in lead time window (suspension start - leadTime) → Prevent NEW hibernation starts
     - If in suspension window → Remove from hibernation schedule (keep awake)
     - **Note**: Ongoing hibernation at lead time window start continues normally
   - **Replace**: Use ONLY exception windows, ignore base schedule
5. Evaluate effective schedule against current time
6. Update `status.activeExceptions[]` history (max 10, prune expired first)

### Single Active Exception Constraint

**Rule**: Only ONE exception with `state=Active` allowed per HibernatePlan at any time.

**Rationale**:

- Simplifies merge semantics (no complex ordering or precedence rules)
- Clear intent (explicit override, not layered modifications)
- Predictable behavior (users know exactly what schedule is active)

**Enforcement**:

- Webhook validation rejects new exception creation if any existing exception has `state=Active`
- Error message: "Plan <name> already has active exception <exception-name> (expires at <timestamp>)"
- User must:
  - Wait for current exception to expire (controller transitions to Expired)
  - OR manually delete current exception (`kubectl delete scheduleexception <name>`)

**Transition period**: New exception can be created immediately after old exception expires (controller-driven transition only)

### Validation Rules

**Webhook validation enforces**:

1. `planRef.name` must reference existing HibernatePlan
2. `planRef.namespace` must equal exception namespace (permanent same-namespace constraint)
3. Only one active exception per plan (query existing exceptions via label)
4. `validFrom <= validUntil`
5. `validUntil - validFrom <= 90 days` (maximum exception duration)
6. `type` must be one of: `extend`, `suspend`, `replace`
7. For `suspend` type: `leadTime` must be valid duration format (or empty)
8. `windows[]` must follow OffHourWindow format (HH:MM time, valid day names)
9. Exception name must be unique within namespace

**Runtime validation in controller**:

- Transition `state: Active` → `state: Expired` when `currentTime > validUntil`
- Trigger HibernatePlan reconciliation when exception state changes
- Update HibernatePlan status with current active exception

### Controller Implementation

**ScheduleException Controller**:

1. **On Create/Update**:
   - Set `state: Active` immediately (no approval workflow in MVP)
   - Add label `hibernator.ardikabs.com/plan: <planRef.name>`
   - Set `appliedAt` timestamp
   - Trigger HibernatePlan reconciliation

2. **On Reconcile** (periodic):
   - Check if `currentTime > validUntil` → Transition to `state: Expired`
   - Set `expiredAt` timestamp when transitioning
   - Schedule requeue at `validUntil` time for automatic expiration
   - Update `message` field with diagnostic info

3. **On Delete** (finalizer):
   - Update HibernatePlan status to remove exception from `activeExceptions[]`
   - Clean up label references
   - Allow deletion to proceed

**HibernatePlan Controller**:

1. **Watch ScheduleException resources** (secondary watch)
   - Enqueue reconciliation when exception referencing this plan changes state

2. **On Reconcile**:
   - Query single active exception (0 or 1 expected)
   - Pass exception to schedule evaluator
   - Update `status.activeExceptions[]` history array:
     - Max 10 entries
     - Prune expired exceptions first, then oldest by `expiredAt`
     - Include: name, type, validFrom, validUntil, state, appliedAt, expiredAt

3. **Schedule Evaluation**:
   - Call `EvaluateWithException(baseSchedule, exception, currentTime)`
   - Return effective schedule for hibernation decision

### Example Scenarios

#### Scenario 1: Extend Hibernation During Weekend Event

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: conference-weekend
  namespace: hibernator-system
spec:
  planRef:
    name: dev-plan
  type: extend
  validFrom: "2026-02-10T00:00:00Z"
  validUntil: "2026-02-15T23:59:59Z"
  windows:
    - start: "00:00"
      end: "23:59"  # Hibernate entire day
      daysOfWeek: ["Saturday", "Sunday"]
```

#### Scenario 2: Extend Hibernation for Additional Work Windows

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: early-bird-support
  namespace: hibernator-system
spec:
  planRef:
    name: dev-plan
  type: extend
  validFrom: "2026-01-29T00:00:00Z"
  validUntil: "2026-02-28T23:59:59Z"
  windows:
    - start: "01:00"
      end: "06:00"
      daysOfWeek: ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday"]
```

#### Scenario 3: Suspend with Lead Time (Maintenance Window)

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: maintenance-window
  namespace: hibernator-system
spec:
  planRef:
    name: prod-plan
  type: suspend
  validFrom: "2026-02-01T20:00:00Z"  # Lead time starts here
  validUntil: "2026-02-02T02:00:00Z"
  leadTime: "1h"  # 1 hour buffer before suspension
  windows:
    - start: "21:00"
      end: "02:00"
      daysOfWeek: ["Saturday"]
```

**Effect**:

- 20:00-21:00: Lead time active → Don't start hibernation (reschedule to 02:00)
- 21:00-02:00: Suspension active → Stay awake
- After 02:00: Can resume normal hibernation schedule

## Implementation Plan

### Phase 1: CRD and Validation ✅ COMPLETE

1. ✅ Design `ScheduleException` CRD types (spec, status, enums) — `api/v1alpha1/scheduleexception_types.go`
2. ✅ Implement validation webhook (planRef existence, same-namespace, single active exception) — `api/v1alpha1/scheduleexception_webhook.go`
3. ✅ Generate CRD manifests and RBAC — `config/crd/bases/`, `config/rbac/role.yaml`
4. ✅ Create sample configurations — `config/samples/scheduleexception_samples.yaml`

### Phase 2: Controller Implementation ✅ COMPLETE

1. ✅ Implement ScheduleException controller (state transitions, finalizer, reconciliation) — `internal/controller/scheduleexception_controller.go`
2. ✅ Update HibernatePlan controller (watch exceptions, query active, status history) — `internal/controller/hibernateplan_controller.go`
3. ✅ Add label-based association (`hibernator.ardikabs.com/plan`) — `LabelPlan` constant
4. ✅ Implement cross-resource event triggering — Annotation-based trigger `AnnotationExceptionTrigger`

### Phase 3: Schedule Evaluation ✅ COMPLETE

1. ✅ Extend schedule evaluator with exception support — `internal/scheduler/schedule.go` (`EvaluateWithException()`)
2. ✅ Implement extend/suspend/replace semantics — `evaluateExtend()`, `evaluateSuspend()`, replace via `evaluateWindows()`
3. ✅ Add lead time prevention logic — `isInLeadTimeWindow()`, `findSuspensionEnd()`
4. ✅ Write comprehensive tests — `internal/scheduler/schedule_test.go` (17 new tests)

### Phase 4: Documentation and Samples 🔄 PENDING

1. ✅ Update RFC-0003 with implementation details
2. 🔄 Create user journey documentation — `docs/user-journey/create-emergency-exception.md` (needs update)
3. ⏳ Add troubleshooting guide
4. ⏳ Document upgrade path from embedded exceptions (future)

## Alternatives Considered

### 1. Embedded Exceptions in HibernatePlan

**Approach**: Add `exceptions` field directly under `spec.schedule` in HibernatePlan

**Pros:**

- Simpler to implement (one CRD)
- Co-located with base schedule
- Easier to query (single resource)

**Cons:**

- Every temporary change modifies plan spec (not GitOps-friendly)
- Requires plan update permissions for exceptions
- Complicates plan versioning and rollback
- Difficult to maintain audit trail for expired exceptions
- No independent lifecycle management

**Decision**: Rejected - Independent CRD provides better GitOps workflow and clearer lifecycle management

### 2. Manual Pause/Resume Annotation

**Approach**: Use annotations like `hibernator.ardikabs.com/pause: "until-2026-02-28"`

**Pros:**

- Extremely simple
- No new CRD
- Fast to implement

**Cons:**

- No structured validation
- No exception types (extend/suspend/replace)
- No lead time support
- Requires manual intervention to resume
- No audit trail
- Limited expressiveness (single pause, no complex windows)

**Decision**: Rejected - Insufficient for real-world use cases requiring different exception semantics

### 3. ConfigMap-Based Configuration

**Approach**: Store exceptions in ConfigMap, controller watches and applies

**Pros:**

- Flexible data structure
- Easy to update externally

**Cons:**

- No schema validation
- Poor discoverability (kubectl get)
- No admission webhooks
- Difficult RBAC enforcement
- Not idiomatic Kubernetes pattern

**Decision**: Rejected - CRD provides proper validation, RBAC, and Kubernetes integration

### 4. Multiple Simultaneous Exceptions

**Approach**: Allow multiple active exceptions per plan with complex merge logic

**Pros:**

- Maximum flexibility
- Could handle overlapping scenarios

**Cons:**

- Complex precedence rules (which exception wins?)
- Difficult to reason about merged schedule
- Unpredictable behavior with conflicts
- Complicated implementation and testing
- Poor user experience (hard to debug)

**Decision**: Rejected - Single active exception simplifies semantics significantly. Future enhancement if strong use case emerges.

## Migration

ScheduleException is a new CRD. No breaking changes to existing HibernatePlans.

## Examples

### Use Case 1: On-Site Event (Original Problem)

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: event-support
  namespace: hibernator-system
spec:
  schedule:
    timezone: "America/New_York"
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday"]
  targets: [...]
---
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: on-site-event
  namespace: hibernator-system
spec:
  planRef:
    name: event-support
  type: extend
  validFrom: "2026-01-29T00:00:00Z"
  validUntil: "2026-02-28T23:59:59Z"
  windows:
    - start: "06:00"
      end: "11:00"
      daysOfWeek: ["Saturday", "Sunday"]
    - start: "01:00"
      end: "06:00"
      daysOfWeek: ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday"]
```

**Effect**: During Jan 29 - Feb 28, services will hibernate during:

- Mon-Fri 20:00-06:00 (base offHours) + 01:00-06:00 (extended)
- Sat-Sun 06:00-11:00 (extended, normally awake)

After Feb 28: Exception expires, plan reverts to base schedule automatically.

### Use Case 2: Quarterly Sprint

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: q1-sprint-push
  namespace: hibernator-system
spec:
  planRef:
    name: dev-plan
  type: extend
  validFrom: "2026-02-15T00:00:00Z"
  validUntil: "2026-03-15T23:59:59Z"
  windows:
    - start: "06:00"  # Extend hibernation throughout day
      end: "18:00"
      daysOfWeek: ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday"]
```

### Use Case 3: Holiday Schedule (Replace)

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: holiday-week
  namespace: hibernator-system
spec:
  planRef:
    name: prod-plan
  type: replace
  validFrom: "2026-12-24T00:00:00Z"
  validUntil: "2026-12-31T23:59:59Z"
  windows:
    - start: "00:00"
      end: "23:59"  # Always hibernated (skeleton crew)
      daysOfWeek: ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"]
```

**Effect**: During Dec 24-31, use only the replacement schedule (ignore base offHours). Services are fully hibernated 24/7.

---

## Future Work: Exception Approval Workflow

Temporary schedule exceptions can significantly impact infrastructure availability and cost. Adding an approval workflow ensures:
- **Safety**: Prevents accidental or malicious schedule changes
- **Compliance**: Creates audit trail of who changed what and when
- **Scalability**: Higher roles (managers, CTOs) can approve without kubectl knowledge

### Proposed Architecture

**Exception States**:
```
Draft → Pending Approval → Approved → Active → Expired
   ↓                          ↓
   └─→ Rejected ─────────────→ Cancelled
```

**Approval Workflow**:

**On-Call Engineer Workflow (Real-world scenario):**

```yaml
# Step 1: On-call engineer creates ScheduleException with approval requirement
# Directly specifies approver emails (no role mapping needed)
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: on-site-event
  namespace: hibernator-system
spec:
  planRef:
    name: event-support  # References existing HibernatePlan

  approvalRequired: true
  approverEmails:  # On-call specifies approvers by email
    - "bob@company.com"      # Engineering Head email
    - "carol@company.com"    # Manager email

  type: extend
  validFrom: "2026-01-29T00:00:00Z"
  validUntil: "2026-02-28T23:59:59Z"
  windows:
    - start: "06:00"
      end: "11:00"
      daysOfWeek: ["SAT", "SUN"]
```

**Workflow:**

1. **On-call engineer applies ScheduleException**
   - Sets `approverEmails` with specific approver email addresses
   - No role lookup needed (direct email specification)

2. **Controller detects pending exception**
   - Creates pending approval entries for each email
   - State: "Pending"

3. **Controller notifies approvers**
   - For each email in `approverEmails`:
     - Calls Slack `users.lookupByEmail(email)` → Gets Slack user ID
     - Opens DM conversation with approver
     - Sends interactive message with exception details + [APPROVE] [REJECT] buttons

4. **Approver receives Slack DM**
   - "New infrastructure exception pending your approval"
   - Exception details (period, type, impact)
   - Quick action buttons in DM

5. **Approver clicks [APPROVE] or [REJECT]**
   - Slack bot calls controller endpoint
   - Controller verifies approver's Slack user matches email
   - Updates ScheduleException status with approval/rejection

6. **When all approvals received**
   - Exception state transitions to "Active"
   - Controller sends confirmation to all approvers
   - HibernatePlan controller detects active exception and applies it

**Benefits of this approach:**
- On-call engineer has full control (specifies exact approvers)
- No pre-configured role mappings needed
- Works for ad-hoc approvals (one-time exceptions)
- Email + Slack DM = private, direct communication
- Audit trail: exactly who approved what and when
```

**Approval Workflow (Core Orchestration):**

1. **User creates ScheduleException** via kubectl or manifest file
   ```yaml
   apiVersion: hibernator.ardikabs.com/v1alpha1
   kind: ScheduleException
   metadata:
     name: on-site-event
     namespace: hibernator-system
   spec:
     planRef:
       name: event-support
     approvalRequired: true
     approverEmails:  # On-call specifies approver emails
       - "bob@company.com"
       - "carol@company.com"
     type: extend
     validFrom: "2026-01-29T00:00:00Z"
     validUntil: "2026-02-28T23:59:59Z"
     windows:
       - start: "06:00"
         end: "11:00"
         daysOfWeek: ["SAT", "SUN"]
   ```

2. **Exception created in "Pending" state**
   ```yaml
   # ScheduleException status field
   status:
     state: Pending
     approvalState: Pending
     requestedBy: "alice"
     requestedAt: "2026-01-29T10:00:00Z"
     requiredApprovals:
       - email: "bob@company.com"
         status: "pending"
       - email: "carol@company.com"
         status: "pending"
     approvals: []  # Empty until approved
   ```

3. **Notification sent to approvers via Slack DM**
   - Controller looks up Slack user by email
   - Sends direct DM to each approver
   - Message includes exception details + approval buttons

4. **Approvers review and act**
   - Option A (Primary): Slack DM → Click [APPROVE] button
   - Option B: kubectl plugin → `hibernator exception approve on-site-event`
   - Option C: Dashboard UI → Visual approval interface

5. **Exception transitions to "Active" when all approvals received**
   ```yaml
   approvals:
     - email: "bob@company.com"
       approvedBy: "bob"
       approvedAt: "2026-01-29T10:15:00Z"
     - email: "carol@company.com"
       approvedBy: "carol"
       approvedAt: "2026-01-29T10:30:00Z"
   state: "Approved"
   ```

6. **Controller only evaluates "Active" exceptions**
   - Pending/Rejected exceptions are ignored
   - Audit trail preserved in status

### Integration Options

**Option A: Slack Integration (Recommended for non-technical approvers)**

Slack push notifications directly to approver DMs with interactive approval buttons:

```
kubectl apply (HibernatePlan with exception)
  ↓
Controller detects pending exception
  ↓
For each required approver role:
  - Look up user in approver group (LDAP/Slack directory)
  - Send Slack DM to user with interactive message:

    📋 New Exception Pending Approval (DM to user)

    Plan: event-support
    Exception: on-site-event
    Requested by: alice (alice@company.com)
    Period: Jan 29 - Feb 28, 2026
    Type: extend

    Required approvals: Manager (pending), Engineering Head (pending)

    [APPROVE] [REJECT] [VIEW DETAILS LINK]
  ↓
Approver clicks [APPROVE] in DM
  ↓
Slack bot calls controller endpoint with user identity
  ↓
Controller verifies user has required role → Updates HibernatePlan status
  ↓
Controller sends confirmation DM to approver
  ↓
When all approvals received → Exception becomes "Active"
  ↓
Controller sends notification to all approvers: "Exception approved and active"
```

**Implementation details for Slack DM approach:**
- Use Slack Bot with `chat:write` and `users.lookupByEmail` scopes for DM sending
- **User lookup by email** (primary method):
  - Store approver emails in approver groups (from LDAP/directory)
  - Use Slack `users.lookupByEmail(email)` API to get Slack user ID
  - More portable than Slack IDs (emails are universal across systems)
  - Works even if approver hasn't been seen by bot before
- Alternative: Pre-cache Slack user IDs for known approvers
- Use `conversations.open(userId)` to initiate DM conversation
- Send interactive message with action buttons (block kit)
- Button callbacks include user identity for audit trail
- Timeout DMs after 30 days (regenerate if needed)
- Store email → Slack user ID mappings with TTL

**Option B: kubectl Plugin (for technical users)**

```
hibernator exception list --pending        # See pending approvals
hibernator exception approve on-site-event # Approve as current user
hibernator exception reject on-site-event "reason..." # Reject
hibernator exception view on-site-event   # See full details + approvals
```

**Option C: Dashboard UI (for visibility)**

- Web UI showing all exceptions with state
- Approval cards for pending items
- Real-time updates via WebSocket
- Audit trail visualization

**Option D: SSO/URL-based Approval (Recommended for platform teams with SSO infrastructure)**

Platform team adds approval link directly in the manifest, controller generates an authenticated SSO link for each approver role:

```
Manifest update (by platform team):
  exceptions:
    - name: "on-site-event"
      approvalRequired: true
      approvalRequiredRoles: ["engineeringHead", "manager"]
  ↓
Controller detects pending exception
  ↓
Controller generates unique SSO approval links:
  - Link for engineeringHead: https://hibernator.company.com/approve/{approvalId}?role=engineeringHead&token={ssoToken}
  - Link for manager: https://hibernator.company.com/approve/{approvalId}?role=manager&token={ssoToken}
  ↓
Links shared via:
  - Email to approvers (automatically by controller)
  - Slack notification (optional, with links embedded)
  - Status field in HibernatePlan CR
  ↓
Approver clicks link
  ↓
SSO authentication (OIDC/SAML) verifies user identity and org membership
  ↓
User sees approval details page with options: [APPROVE] [REJECT]
  ↓
Approval action updates HibernatePlan status
  ↓
Audit trail records: who approved, when, from which IP/session
  ↓
When all approvals received → Exception becomes "Active"
```

**Advantages**:
- Leverages existing SSO infrastructure (no new auth system)
- Works for any org member, not just Slack/kubectl users
- Simple sharing via email/link
- Built-in audit trail via SSO logs
- No external dependencies (Slack API, custom webhooks)
- Stateless — links contain all needed context

**Implementation details**:
1. Controller generates `approvalId` (UUID) per exception + role combination
2. Create OIDC/SAML integration endpoint for token validation
3. Store approval link context in temporary ConfigMap or in-memory cache (TTL: 30 days)
4. Approval endpoint: POST to `/approve/{approvalId}` with SSO-validated user context
5. Webhook for SSO provider to push identity context (optional, for enhanced audit)

### Implementation Plan

**Phase 1: Core Approval State**
1. Add `approvalRequired` and `approvalRequiredRoles` to exception spec
2. Add `pendingApprovals` and `approvalHistory` to status
3. Implement approval state machine in controller
4. Add RBAC roles for approvers

**Phase 2: Slack Integration**
1. Create Slack App with interactive message handling
2. Implement webhook endpoint for Slack button interactions
3. Send interactive messages on pending exceptions
4. Approve/reject via Slack buttons

**Phase 3: kubectl Plugin**
1. Extend kubectl hibernator plugin
2. Add `exception approve/reject/list` subcommands
3. Support filtering by state and role

**Phase 4: SSO/URL-based Approval**
1. Implement OIDC/SAML integration endpoint
2. Generate unique approval links with SSO tokens per approver role
3. Create approval endpoint that validates SSO context
4. Add email notification integration for sending approval links
5. Store approval link metadata in temporary storage (ConfigMap or cache with TTL)
6. Audit trail integration with SSO provider logs

**Phase 5: Dashboard UI**
1. Build simple web dashboard
2. Exception state visualization
3. Approval interface
4. Audit trail viewer

### RBAC Roles

```yaml
# Approver role - can approve/reject exceptions
approverRole:
  rules:
    - apiGroups: ["hibernator.ardikabs.com"]
      resources: ["hibernateplans/status/approvals"]
      verbs: ["patch", "update"]

# View-only role - can see pending approvals
auditorRole:
  rules:
    - apiGroups: ["hibernator.ardikabs.com"]
      resources: ["hibernateplans"]
      verbs: ["get", "list", "watch"]
```

### Example: Slack Approval Flow

**Slack Message**:
```
📋 New Schedule Exception Pending Approval

Plan: event-support
Exception: on-site-event
Requested by: alice (alice@company.com)

Type: extend
Period: Jan 29 - Feb 28, 2026

Required Approvals:
□ Engineering Head
□ Manager

[APPROVE] [REJECT] [VIEW IN DASHBOARD]
```

**Audit Trail**:
```yaml
status:
  exceptionApprovals:
    - name: "on-site-event"
      state: "Approved"
      history:
        - action: "requested"
          actor: "alice"
          timestamp: "2026-01-29T10:00:00Z"
        - action: "approved"
          actor: "bob"
          role: "engineeringHead"
          timestamp: "2026-01-29T10:15:00Z"
        - action: "approved"
          actor: "carol"
          role: "manager"
          timestamp: "2026-01-29T10:30:00Z"
```

### Benefits

- ✅ Safety: No exception takes effect without approval
- ✅ Compliance: Full audit trail of changes
- ✅ Usability: Multiple approval channels
  - Slack for non-technical approvers in messaging platforms
  - kubectl for engineers who work with CLI
  - SSO URL for platform teams leveraging existing identity infrastructure
  - Dashboard UI for visibility across the organization
- ✅ Scalability: Works across organizations with different roles and auth models
- ✅ Flexibility: Choose one or combine multiple integration options

---

## Approval Options Comparison & Difficulty Analysis

### Comparison Matrix

| Dimension | Option A: Slack | Option B: kubectl | Option C: Dashboard UI | Option D: SSO/URL |
|-----------|-----------------|-------------------|----------------------|-------------------|
| **Implementation Difficulty** | Simple-Moderate | Low | High | Medium-High |
| **Implementation Time (Estimate)** | 2-3 weeks | 2-3 weeks | 6-8 weeks | 4-6 weeks |
| **Dependencies** | Slack API, bot tokens | kubectl client library | Web framework, DB | OIDC/SAML provider |
| **Maintenance Burden** | Low-Medium | Low | High | Medium |
| **Scalability** | Good (Slack rate limits) | Excellent | Good | Excellent |
| **User Experience** | Excellent (DM-based) | Good (CLI users) | Excellent | Excellent |
| **Security Posture** | Medium (bearer tokens) | Medium (RBAC only) | Medium (session-based) | High (SSO + tokens) |
| **Audit Trail Quality** | Good (stored in status) | Good (stored in status) | Excellent (DB logs) | Excellent (SSO + CR) |
| **Non-Tech Approvers** | ✅ Yes | ❌ No | ✅ Yes | ✅ Yes |
| **Dependency on External Services** | ✅ Slack infrastructure | ❌ None | ❌ None | ✅ SSO provider |
| **Offline Support** | ❌ No | ✅ Yes | ❌ Limited | ❌ No |
| **Cost** | Free (if using existing Slack) | Free | Low (hosting) | Free (if using existing SSO) |
| **User Lookup Method** | Email-based (simple) | N/A | N/A | N/A |

### Difficulty Scoring

**Scoring scale:**
- 1 = Trivial (< 1 week)
- 2 = Simple (1-2 weeks)
- 3 = Moderate (2-4 weeks)
- 4 = Complex (4-8 weeks)
- 5 = Very Complex (> 8 weeks)

#### Option A: Slack Integration → **Difficulty: 2.5 (Simple-Moderate)** [Reduced from 3]

**What's required:**
1. Slack App registration and OAuth setup (1-2 days)
   - Enable bot permissions: `chat:write` and `users:lookupByEmail`
   - Set up event subscriptions for button interactions

2. **On-call engineer specifies approvers by email** (no role mapping)
   - On-call sets `spec.schedule.exceptions[].approverEmails: ["bob@company.com", "carol@company.com"]`
   - Controller directly uses these emails for Slack lookup
   - Simplifies workflow: no need for role-based directory mapping

3. Slack email-based user lookup integration (1 day) [Simplified from 2]
   - For each email in `approverEmails`:
     - Call Slack `users.lookupByEmail(email)` → Get Slack user ID
     - Open DM conversation and send interactive message
   - Much simpler than maintaining Slack ID mappings
3. DM-based interactive message sending (2-3 days)
   - Generate message blocks with approval buttons
   - Handle button click callbacks
4. Error handling and retry logic (1-2 days)
5. Testing and integration (3-5 days)

**Challenges:**
- Slack API rate limiting (handle gracefully)
- Email format consistency (normalize before lookup)
- Message context persistence (email → conversation ID mapping)
- Handling delayed approvals (links expire after ~30 days)
- Slack workspace app installation/approval

**Mitigations:**
- Use Slack Python SDK (well-documented)
- Email-based lookup is more reliable than ID mapping
- Cache email → Slack ID lookups with 24h TTL
- Re-send approval message if clicked after timeout

**Advantages of email-based lookup:**
- ✅ Emails are universal and stable across systems
- ✅ Works with LDAP/directory groups directly (no manual ID mapping)
- ✅ More portable (email > Slack user ID)
- ✅ Simpler integration (one Slack API call per approver)
- ✅ Easier to troubleshoot (emails vs opaque user IDs)

---

#### Option B: kubectl Plugin → **Difficulty: 2 (Simple)**

**What's required:**
1. kubectl plugin scaffolding (1 day)
2. Exception list/view commands (1-2 days)
3. Approval/reject commands (1-2 days)
4. kubeconfig integration (1 day)
5. Testing (2-3 days)

**Challenges:**
- Minimal - leverages existing Kubernetes client libraries
- Simple RBAC integration
- No external dependencies

**Advantages:**
- Least complex to implement
- Works offline (local kubeconfig)
- Leverages existing kubectl ecosystem

---

#### Option C: Dashboard UI → **Difficulty: 4 (Complex)**

**What's required:**
1. Frontend framework setup (React/Vue/Angular) (1-2 days)
2. Backend API for exception querying (3-4 days)
3. WebSocket/polling for real-time updates (2-3 days)
4. User authentication/session management (2-3 days)
5. Database schema for audit logs (1-2 days)
6. UI components and approval interface (5-7 days)
7. Deployment and hosting (2-3 days)
8. Testing, E2E tests (4-5 days)

**Challenges:**
- Full-stack development required
- Database schema design and migrations
- Session management and CSRF protection
- WebSocket connection handling at scale
- Hosting infrastructure needed
- Browser compatibility testing

**Advantages:**
- Best user experience
- Excellent audit trail
- Real-time updates

---

#### Option D: SSO/URL-based Approval → **Difficulty: 3.5 (Moderate-Complex)**

**What's required:**
1. OIDC/SAML integration (2-3 days)
   - Configure identity provider (OKTA, Azure AD, Auth0)
   - Implement token validation
2. Approval link generation (1-2 days)
3. Stateless approval endpoint (2-3 days)
4. Email integration (1-2 days)
5. Temporary secret storage (ConfigMap or TTL cache) (1-2 days)
6. Testing and security audit (3-4 days)

**Challenges:**
- OIDC/SAML configuration varies by provider
- Token validation and audience checking
- Preventing token replay attacks
- Handling token expiration gracefully
- Email delivery reliability
- Security review for authentication flow

**Advantages:**
- Leverages existing SSO infrastructure (no new auth)
- Highest security posture
- Works in any environment (email, Slack, Teams, etc.)
- Excellent audit trail through SSO provider

---

### Decision Matrix by Use Case

#### Use Case 1: Already have Slack infrastructure
**Recommended:** Option A (Slack) → Start with this
- Difficulty: 2.5 (simple-moderate) [Reduced with email lookup]
- Time: 2-3 weeks [Faster than before]
- Already integrated with team workflows
- Email-based user lookup simplifies integration
- Familiar UX for non-technical approvers

**Optional addition:** Option D (SSO) for formal change control

---

#### Use Case 2: Engineering-heavy organization with kubectl
**Recommended:** Option B (kubectl) + Option A (Slack for visibility)
- Option B difficulty: 2 (simple) — implement first
- Time: 2-3 weeks for kubectl
- Later add Slack for non-technical stakeholders

---

#### Use Case 3: Enterprise with SSO mandate
**Recommended:** Option D (SSO/URL) → Primary choice
- Difficulty: 3.5 (moderate-complex)
- Time: 4-6 weeks
- Leverage existing identity infrastructure
- Highest security and audit trail
- Works across all channels (email, Teams, Slack, etc.)

**Optional:** Add Option C (Dashboard) for consolidated visibility

---

#### Use Case 4: Full visibility and multi-channel approval
**Recommended:** Options D + C (SSO + Dashboard)
- Start with Option D (SSO/URL): 4-6 weeks
- Add Option C (Dashboard): +6-8 weeks
- Total: 10-14 weeks for complete solution
- Result: Best UX + highest security + best audit trail

---

### Phased Implementation Recommendation

**MVP (Weeks 1-3):**
- Phase 1: Core approval state machine (all options share this)
- Phase 2: Option B (kubectl) — lowest friction, engineers can start approving immediately

**Phase 2 (Weeks 4-6):**
- Phase 2: Option A (Slack) — with email-based lookup, quick integration with team workflows

**Phase 3 (Weeks 7-10):**
- Phase 4: Option D (SSO/URL) — platform team gets formal approval links

**Phase 4+ (Weeks 11+):**
- Phase 5: Option C (Dashboard) — consolidated visibility (optional, can be skipped if satisfied with existing options)

### Quick Start by Organization Type

| Organization Type | Quick Start Strategy | Timeline |
|-------------------|---------------------|----------|
| **Startup / Small (< 50 people)** | Option B (kubectl) | 2-3 weeks |
| **Mid-size (50-500)** | Option A (Slack with email lookup) → Option B | 4-5 weeks |
| **Enterprise (500+)** | Option D (SSO/URL) | 4-6 weeks |
| **Enterprise + formal audits** | Option D + C (SSO + Dashboard) | 10-14 weeks |

---

## Known Limitations

### Multiple OffHours Windows (MVP Constraint)

**Issue:** When the base HibernationPlan has multiple `offHours` windows, only the **first** window is evaluated by the scheduler. Additional windows are silently ignored.

**Example:**
```yaml
spec:
  schedule:
    offHours:
      - start: "20:00"        # ✅ Processed
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
      - start: "00:00"        # ⚠️ Silently ignored (MVP constraint)
        end: "23:59"
        daysOfWeek: ["SAT", "SUN"]
```

**Impact on ScheduleException:**
- `type: extend` exceptions work correctly (add windows to first base window)
- `type: suspend` exceptions work correctly (carve-out from first base window)
- `type: replace` exceptions work correctly (override with exception windows)
- Multi-window base schedules require workarounds

**Workarounds:**
1. **Create separate HibernationPlans** per schedule pattern (Recommended)
2. **Use `type: extend` exception** to permanently add supplementary windows
3. **Reference RFC-0002** for full discussion and Phase 4 enhancement plan

**Timeline:** Phase 4+ enhancement (see RFC-0002 Future Enhancements)

---

## References

- RFC-0001: Hibernator Operator Core Design
- HibernatePlan API: `api/v1alpha1/hibernateplan_types.go`
- Schedule Format: RFC-0002
