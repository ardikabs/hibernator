<!--
RFC: 0003
Title: Temporary Schedule Exceptions and Overrides
Author: Hibernator Team
Status: Proposed
Date: 2026-01-29
-->

# RFC 0003 — Temporary Schedule Exceptions and Overrides

**Keywords:** Schedule-Exceptions, Maintenance-Windows, Lead-Time, Time-Bound, Extend, Suspend, Replace, Emergency-Events, Validation, Status-Tracking

**Status:** Proposed (Not Yet Implemented)

## Summary

Introduce time-bound schedule exceptions to handle temporary deviations from the planned hibernation schedule. Exceptions are part of the schedule configuration and allow teams to add hibernation windows, prevent hibernation (suspend), or completely replace the schedule without modifying the base HibernatePlan.

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

## Goals

- Enable temporary schedule exceptions as part of schedule configuration
- Support three exception types: "extend" (add windows), "suspend" (carve-out with lead time), "replace" (full override)
- Provide lead time configuration for suspensions to prevent mid-process hibernation interruption
- Automatically expire exceptions to prevent stale overrides
- Provide clear visibility into active exceptions in status

## Non-Goals

- Support infinite exceptions (time-bound only)
- Complex boolean logic (keep it simple: extend/suspend/replace)

## Proposal

### API Changes

Add `exceptions` field under `schedule` to define temporary exceptions to the hibernation schedule:

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

    # NEW: Temporary schedule exceptions (part of schedule configuration)
    exceptions:
      - name: "on-site-event"
        description: "Team on-site event support - 1 month"
        validFrom: "2026-01-29T00:00:00Z"
        validUntil: "2026-02-28T23:59:59Z"

        # Type of exception: "extend", "suspend", or "replace"
        type: "extend"

        # Windows (meaning depends on exception type)
        windows:
          - start: "06:00"
            end: "11:00"
            daysOfWeek: ["Saturday", "Sunday"]
          - start: "01:00"
            end: "06:00"
            daysOfWeek: ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday"]

  targets: [...]
```

### Exception Types

**1. Extend** (`type: extend`)
- **Meaning**: Apply these windows IN ADDITION to the base `offHours`
- **Use case**: "Hibernate during these additional times (e.g., weekend support, early morning)"
- **Behavior**: Union of base `offHours` + exception windows

**2. Suspend** (`type: suspend`)
- **Meaning**: Prevent hibernation during this window (carve-out from hibernation)
- **Use case**: "Keep services awake during this window (e.g., maintenance, incident response, deployment)"
- **Behavior**: Subtract these windows from the combined hibernation schedule
- **Lead time**: Specifies buffer period before suspension begins where hibernation should not start
  - Default: "0s" (no buffer)
  - Format: String duration (e.g., "3600s", "1h", "60m")
  - Example: `leadTime: "1h"` → Don't start hibernation within 1 hour before suspension

**3. Replace** (`type: replace`)
- **Meaning**: Completely replace base schedule during this period
- **Use case**: "Temporary schedule change (e.g., holiday mode, different timezone support)"
- **Behavior**: Use only extension windows during this period, ignore base `offHours`

### Status Tracking

Add `activeExceptions` and `expiredExceptions` to `HibernatePlanStatus`:

```yaml
status:
  phase: "Active"
  activeExceptions:
    - name: "on-site-event"
      type: "extend"
      validUntil: "2026-02-28T23:59:59Z"
      reason: "Active: 15 days remaining"

  expiredExceptions:
    - name: "on-site-event"
      expiredAt: "2026-02-28T23:59:59Z"
```

### Semantics

**Schedule Evaluation Logic**:

1. Start with base `offHours` windows from schedule
2. Check if current time has any active exceptions
3. For `extend` type:
   - Add these windows to the hibernation schedule
   - Union of base + exception windows
4. For `suspend` type:
   - Check if current time falls within suspension window OR within lead time window
   - If yes, do NOT start hibernation (hold/delay)
   - Remove these windows from the combined hibernation schedule
   - Behavior: Subtract from schedule + prevent hibernation start if lead time active
5. For `replace` type:
   - Use only exception windows during this period (ignore base offHours)
6. Remove expired exceptions from `activeExceptions` during reconciliation

**Lead Time Semantics** (for `suspend` type):
```
Base schedule: 20:00-06:00 hibernation
Suspend: 21:00-02:00 (keep awake)
Lead time: 3600s (1 hour)

Timeline:
20:00: Check for hibernation → Lead time zone active (20:00-21:00) → DON'T start
       Reschedule to 02:00 (after suspension ends)
21:00-02:00: Suspension active → Stay awake
02:00: Suspension ended, lead time passed → Hibernation can start
```

**Precedence**:
- Lead time is checked before hibernation starts
- If in lead time or active suspension, hibernation is delayed
- Base schedule is always evaluated when no exceptions active
- Multiple non-overlapping exceptions allowed
- For overlapping exceptions: explicit validation or controller rejects

### Validation Rules

1. `validFrom <= validUntil`
2. `validUntil - validFrom <= 90 days` (max exception window, configurable)
3. For `suspend` type: `leadTime` must be valid duration string (e.g., "0s", "30m", "1h")
4. No overlapping exceptions of same type (optional, can be strict or lenient)
5. Exception names must be unique within plan
6. Windows inside exception must follow same format as base `offHours`

### Controller Changes

**Schedule Evaluation with Exceptions**:

1. Load HibernatePlan
2. Extract base `offHours` from schedule
3. Check for active/expired exceptions
4. For each active `extend` exception: add windows to hibernation schedule
5. For each active `suspend` exception:
   - Check if current time is within lead time window (suspension start - leadTime)
   - If yes, mark as "hibernation_held" (don't start hibernation)
   - Otherwise, subtract windows from hibernation schedule
6. For each active `replace` exception: use only exception windows (ignore base schedule)
7. Evaluate combined schedule against current time
8. If hibernation should start but is held (lead time active), reschedule for after suspension
9. Proceed with normal hibernation/wakeup logic
10. Move expired exceptions to `expiredExceptions` during reconciliation

### Example Scenarios

**Scenario 1: Extend Hibernation During Weekend Event**
```yaml
exceptions:
  - name: "conference-weekend"
    type: "extend"
    validFrom: "2026-02-10T00:00:00Z"
    validUntil: "2026-02-15T23:59:59Z"
    windows:
      - start: "00:00"
        end: "23:59"  # Hibernate entire day
        daysOfWeek: ["Saturday", "Sunday"]
```

**Scenario 2: Extend Hibernation for Additional Work Windows**
```yaml
exceptions:
  - name: "early-bird-support"
    type: "extend"
    validFrom: "2026-01-29T00:00:00Z"
    validUntil: "2026-02-28T23:59:59Z"
    windows:
      - start: "01:00"
        end: "06:00"
        daysOfWeek: ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday"]
```

**Scenario 3: Suspend with Lead Time (Maintenance Window)**
```yaml
exceptions:
  - name: "maintenance-window"
    type: "suspend"
    validFrom: "2026-02-01T22:00:00Z"
    validUntil: "2026-02-02T02:00:00Z"
    leadTime: "1h"  # 1 hour buffer before suspension
    windows:
      - start: "22:00"
        end: "02:00"
        daysOfWeek: ["Saturday"]
```

**Effect**:
- 21:00-22:00: Lead time active → Don't start hibernation (reschedule to 02:00)
- 22:00-02:00: Suspension active → Stay awake
- After 02:00: Can resume normal hibernation schedule

## Implementation Plan

### Phase 1: Core Exception Support
1. Update HibernatePlan CRD with `exceptions` field under `schedule`
2. Implement exception validation in webhook
3. Add exception processing in schedule evaluator
4. Update status ledger with `activeExceptions`/`expiredExceptions`

### Phase 2: Cleanup and Monitoring
1. Add metrics for active/expired exceptions
2. Implement exception history tracking
3. Add optional TTL-based cleanup for expired exceptions

### Phase 3: CLI/UI
1. Add kubectl plugin to manage exceptions
2. Dashboard display of active exceptions

## Alternatives Considered

1. **Top-level `exceptions` field**
   - Simpler to add but semantically unclear
   - Rejected: Exceptions are part of schedule configuration, not separate

2. **Separate ScheduleException CRD**
   - More flexible but adds complexity
   - Rejected: Overkill for MVP, harder to manage lifecycle

3. **Manual pause/resume annotation**
   - Simpler but no time-bound guarantee
   - Rejected: Requires manual intervention; doesn't auto-expire

4. **cron-based exception syntax**
   - More powerful but steeper learning curve
   - Rejected: Keep user-friendly format consistent with base schedule

## Migration

No breaking changes. Existing HibernatePlans without `exceptions` field work as before.

## Examples

### Use Case 1: On-Site Event (Original Problem)

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

    # Extend hibernation during on-site event
    exceptions:
      - name: "on-site-event"
        description: "Team on-site event support"
        validFrom: "2026-01-29T00:00:00Z"
        validUntil: "2026-02-28T23:59:59Z"
        type: "extend"
        windows:
          - start: "06:00"
            end: "11:00"
            daysOfWeek: ["Saturday", "Sunday"]
          - start: "01:00"
            end: "06:00"
            daysOfWeek: ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday"]

  targets: [...]
```

**Effect**: During Jan 29 - Feb 28, services will hibernate during:
- Mon-Fri 20:00-06:00 (base offHours) + 01:00-06:00 (extended)
- Sat-Sun 06:00-11:00 (extended, normally awake)

After Feb 28: Extension expires, plan reverts to base schedule automatically.

### Use Case 2: Quarterly Sprint

```yaml
exceptions:
  - name: "q1-sprint-push"
    validFrom: "2026-02-15T00:00:00Z"
    validUntil: "2026-03-15T23:59:59Z"
    type: "extend"
    windows:
      - start: "06:00"  # Extend hibernation throughout day
        end: "18:00"
        daysOfWeek: ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday"]
```

### Use Case 3: Holiday Schedule (Replace)

```yaml
exceptions:
  - name: "holiday-week"
    description: "Holiday week - different schedule"
    validFrom: "2026-12-24T00:00:00Z"
    validUntil: "2026-12-31T23:59:59Z"
    type: "replace"
    windows:
      - start: "00:00"
        end: "23:59"  # Always hibernated (skeleton crew)
        daysOfWeek: ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"]
```

**Effect**: During Dec 24-31, use only the replacement schedule (ignore base offHours). Services are fully hibernated 24/7.

## Next Steps

1. Implement Phase 1 API changes (exceptions under schedule)
2. Update validation webhook with exception validation
3. Update schedule evaluator to process exceptions
4. Add status ledger tracking for active/expired exceptions
5. Write comprehensive tests
---

## Future Work: Exception Approval Workflow

### Motivation

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
# Step 1: On-call engineer creates HibernatePlan with exception
# Directly specifies approver emails (no role mapping needed)
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: event-support
spec:
  schedule:
    timezone: "America/New_York"
    offHours: [...]
    exceptions:
      - name: "on-site-event"
        approvalRequired: true
        approverEmails:  # On-call specifies approvers by email
          - "bob@company.com"      # Engineering Head email
          - "carol@company.com"    # Manager email
        type: "extend"
        windows: [...]
        validFrom: "2026-01-29T00:00:00Z"
        validUntil: "2026-02-28T23:59:59Z"
```

**Workflow:**

1. **On-call engineer applies HibernatePlan**
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
   - Updates HibernatePlan status with approval/rejection

6. **When all approvals received**
   - Exception state transitions to "Active"
   - Controller sends confirmation to all approvers
   - Hibernation plan starts executing

**Benefits of this approach:**
- On-call engineer has full control (specifies exact approvers)
- No pre-configured role mappings needed
- Works for ad-hoc approvals (one-time exceptions)
- Email + Slack DM = private, direct communication
- Audit trail: exactly who approved what and when
```

**Approval Workflow (Core Orchestration):**

1. **User creates/updates exception** via kubectl plugin or direct manifest edit
   ```yaml
   exceptions:
     - name: "on-site-event"
       approvalRequired: true
       approverEmails:       # On-call specifies approver emails
         - "bob@company.com"
         - "carol@company.com"
       # ... rest of exception config
   ```

2. **Exception created in "Pending" state**
   ```yaml
   status:
     activeExceptions: []
     pendingApprovals:
       - name: "on-site-event"
         state: "Pending"
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

## References

- RFC-0001: Hibernator Operator Core Design
- HibernatePlan API: `api/v1alpha1/hibernateplan_types.go`
- Schedule Format: RFC-0002
