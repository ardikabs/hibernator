# Approve Hibernation Exceptions

**Tier:** `[Enhanced]`

**Personas:** Engineering Manager, Tech Lead, Security Officer, Engineering Head, CI/CD Pipeline

**When:** Temporary hibernation exceptions (suspend, extend, replace) require governance approval before taking effect

**Why:** Approval workflows ensure exceptions follow organizational policies, maintain compliance, and provide audit trails for governance.

---

## User Stories

**Story 1:** As an **Engineering Manager**, I want to **approve exceptions via Slack**, so that **I can review and approve without leaving my chat app**.

**Story 2:** As an **Engineering Head**, I want to **approve exceptions via kubectl CLI**, so that **I can integrate with my existing CLI workflows and automation**.

**Story 3:** As a **Security Officer**, I want to **audit all approvals and rejections**, so that **I maintain governance records for compliance**.

---

## When/Context

- **Multiple approval channels:** Slack for managers, CLI for automation teams
- **Time-sensitive approvals:** Some exceptions are urgent (incident response)
- **Compliance auditing:** All approvals recorded in Kubernetes and audit system
- **Flexible workflows:** Support both interactive (Slack) and programmatic (CLI) approval

---

## Business Outcome

Enable organizations to approve temporary hibernation exceptions through their preferred channel (Slack, CLI, or automation), with complete audit trail and policy enforcement.

---

## Step-by-Step Flow

### Common Setup: Create Exception with Approval Required

All approaches start with an exception that requires approval:

```yaml
apiVersion: hibernator.ardikasaputro.io/v1alpha1
kind: HibernationPlan
metadata:
  name: prod-offhours
spec:
  schedule:
    exceptions:
      - name: "on-site-event"
        description: "On-site event - need extended support"
        type: suspend
        approvalRequired: true
        approverEmails:              # Specify who must approve
          - "bob.johnson@company.com"
          - "carol.chen@company.com"
        validFrom: "2026-02-01T22:00:00Z"
        validUntil: "2026-02-01T23:30:00Z"
        windows: [...]
```

```bash
kubectl apply -f hibernation-plan.yaml
# Exception enters "Pending Approval" state
```

---

## Approach 1: Slack (Interactive)

**Best for:** Non-technical approvers, real-time notifications, quick decisions

### 1. Controller detects pending exception

Controller sees `approvalRequired: true` and `approverEmails` list.

For each approver email:
- Looks up Slack user via `users.lookupByEmail()`
- Opens DM conversation
- Sends interactive Slack message

### 2. Manager receives Slack DM

**Message:**
```
📋 New Exception Pending Approval

Plan: prod-offhours
Exception: on-site-event
Requested by: alice.dev@company.com

Type: suspend (keep services awake)
Period: Feb 1, 2026 22:00-23:30 UTC
Lead Time: 5 minutes
Reason: "On-site event - need extended support hours"

Required Approvals:
□ Engineering Lead (bob.johnson@company.com)
□ Manager (carol.chen@company.com)

[APPROVE]  [REJECT]  [VIEW DETAILS]
```

### 3. Manager clicks [APPROVE] in DM

Slack bot receives button click:
1. Identifies approver (bob.johnson@company.com)
2. Calls controller endpoint
3. Updates HibernationPlan status
4. Sends confirmation DM

### 4. When all approvals received

Exception transitions to "Approved" → "Active":

```bash
kubectl describe hibernateplan prod-offhours

# Status:
#   Exceptions:
#     - Name: on-site-event
#       State: Active
#       Approvals:
#         - Email: bob.johnson@company.com
#           Status: approved
#           ApprovedAt: 2026-02-01T22:05:00Z
#         - Email: carol.chen@company.com
#           Status: approved
#           ApprovedAt: 2026-02-01T22:10:00Z
```

### 5. Manager receives confirmation

```
✅ Exception Approved

Exception: on-site-event
Status: ACTIVE

Services remain awake until 23:30 UTC as requested.
Auto-expires at that time.
```

---

## Approach 2: CLI (Programmatic)

**Best for:** Engineers, automation, CI/CD pipelines, policy enforcement

### 1. Install hibernator CLI plugin

```bash
# Install via Krew (Kubernetes plugin manager)
kubectl krew install hibernator

# Or download directly
curl -L https://github.com/ardikasaputro/hibernator/releases/latest/download/hibernator \
  -o /usr/local/bin/hibernator
chmod +x /usr/local/bin/hibernator
```

### 2. List pending exceptions

```bash
hibernator exception list --pending

# Output:
# NAMESPACE  PLAN            EXCEPTION          STATE    CREATED
# default    prod-offhours   on-site-event      Pending  2m ago
```

### 3. View exception details

```bash
hibernator exception view on-site-event --plan prod-offhours

# Output:
# Name: on-site-event
# Type: suspend
# Status: Pending Approval
# Requested by: alice.dev@company.com
# Duration: Feb 1, 22:00-23:30 UTC
#
# Required Approvals:
#   [ ] bob.johnson@company.com (Engineering Head)
#   [ ] carol.chen@company.com (Manager)
```

### 4. Approve exception

As Engineering Head:

```bash
hibernator exception approve on-site-event \
  --plan prod-offhours \
  --reason "Approved for on-site event"

# Output:
# ✓ Exception approved
# Approver: bob.johnson@company.com
# Timestamp: 2026-02-01T22:05:00Z
```

### 5. Check updated status

```bash
hibernator exception view on-site-event --plan prod-offhours

# Output:
# Required Approvals:
#   [x] bob.johnson@company.com (approved 22:05 UTC)
#   [ ] carol.chen@company.com (pending)
```

### 6. Auto-Approval in CI/CD (Optional)

For routine exceptions, CI/CD can auto-approve:

```bash
# In your CI/CD pipeline (GitHub Actions example)
- name: Auto-approve low-risk exceptions
  run: |
    # List exceptions tagged as "auto-approvable"
    hibernator exception list --pending --label risk=low | while read line; do
      EXCEPTION=$(echo "$line" | awk '{print $1}')
      PLAN=$(echo "$line" | awk '{print $2}')
      hibernator exception approve "$EXCEPTION" \
        --plan "$PLAN" \
        --reason "Auto-approved by CI/CD (low-risk)"
    done
```

---

## Rejection Workflow (Both Approaches)

### Via Slack

Manager clicks [REJECT]:
```
Manager types reason: "Budget impact too high; suggest 1 hour instead"
  ↓
Controller records rejection with reason
  ↓
Requester notified: "Exception rejected: Budget impact too high..."
```

### Via CLI

```bash
hibernator exception reject on-site-event \
  --plan prod-offhours \
  --reason "Budget impact too high; suggest 1 hour instead"

# Output:
# ✓ Exception rejected
# Reason: "Budget impact too high; suggest 1 hour instead"
# Timestamp: 2026-02-01T22:05:00Z
```

---

## Decision Branches

### Which Approval Method Should I Use?

**Use Slack if:**
- ✅ Approver is non-technical manager
- ✅ Decision needs to be made in <5 minutes
- ✅ Approver is already on Slack during work hours
- ✅ You want one-click approval without CLI knowledge
- ✅ Real-time notification is critical

**Use CLI if:**
- ✅ Approver prefers kubectl and terminal
- ✅ Approval can be scripted/automated
- ✅ Exception is part of CI/CD release workflow
- ✅ You want to enforce policies programmatically
- ✅ Exception needs audit trail in kubectl/API logs

**Use Both (Hybrid):**
- ✅ Engineering managers approve via Slack
- ✅ CI/CD auto-approves low-risk exceptions via CLI
- ✅ On-call engineers can override via CLI if needed

---

## Related Journeys

- [Create Emergency Exception](create-emergency-exception.md) — Create the exception before requesting approval
- [Extend Hibernation for Event](extend-hibernation-for-event.md) — Multi-day exceptions
- [Suspend Hibernation During Incident](suspend-hibernation-during-incident.md) — Quick incident response
- [Configure RBAC for Hibernation](configure-rbac-for-hibernation.md) — Set up approval permissions

---

## Pain Points Solved

- **Governance gap:** Exceptions were previously ad-hoc; now they require approval
- **Audit trail:** Complete record of who approved what and when
- **Team friction:** Managers don't need to log into CLI; engineers can stay in terminal
- **Automation:** Low-risk exceptions can be auto-approved without manual intervention
- **Emergency response:** Quick approval paths for incident-driven exceptions

---

## Trade-offs by Approach

| Dimension | Slack | CLI |
|-----------|-------|-----|
| **Setup time** | ~2 weeks (Slack bot integration) | ~1 week (plugin packaging) |
| **User learning curve** | None (native Slack users) | Medium (kubectl required) |
| **Latency** | <1 min (real-time DM) | ~30s (manual kubectl call) |
| **Automation** | Limited (button clicks) | Excellent (scriptable) |
| **Policy enforcement** | Manual (judgement-based) | Programmatic (rules-based) |
| **Target users** | Managers, non-technical approvers | Engineers, automation systems |
| **Audit quality** | Good (Slack logs + Kubernetes) | Excellent (full kubectl audit trail) |
| **When to use** | Interactive, time-sensitive | Routine, scripted, CI/CD |

---

## RFC References

- [RFC-0003](../enhancements/0003-schedule-exceptions.md) — Schedule exceptions and approval workflows
