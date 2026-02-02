# [Journey Title]

**Tier:** `[MVP | Enhanced | Advanced]`

**Personas:** [Persona1], [Persona2], [Persona3]

**When:** [Context for when this journey applies - describe the trigger or scenario]

**Why:** [Business value delivered by this journey - the problem being solved]

---

<!-- Optional: Add RFC implementation status notice if journey spans multiple RFC phases -->
<!-- Example for features with implemented + future phases:
> **👉 RFC-XXXX Implementation Status**
> This journey covers features from **RFC-XXXX Phase 1-3** (✅ Implemented):
> - Feature A
> - Feature B
> - Feature C
>
> **NOT covered:** Feature D (Phase 4, future work)
-->

<!-- Optional: Add future work notice if entire journey describes unimplemented features -->
<!-- Example for future-only features:
> **⚠️ FUTURE WORK NOTICE**
> This journey describes the [feature name] feature documented in **RFC-XXXX Phase N**, which is **NOT YET IMPLEMENTED**.
> Current implementation (Phase 1-M) supports [what's currently available].
> See [RFC-XXXX "Future Work: [Section Name]"](../../enhancements/XXXX-name.md#section-anchor) for the planned design.
-->

---

## User Stories

**Story 1:** As a **[Persona]**, I want to **[action]**, so that **[benefit]**.

**Story 2:** As a **[Persona]**, I want to **[action]**, so that **[benefit]**.

<!-- Add more stories as needed -->

---

## When/Context

[Shared context that applies to all stories in this journey. Describe:]
- **Trigger conditions:** What prompts this journey?
- **Prerequisites:** What must exist before starting?
- **Environment:** DEV/STG/PROD, single/multi-cluster, etc.
- **Constraints:** Time, resources, permissions, compliance

---

## Business Outcome

[What the user achieves or what problem is solved when this journey is complete. Be specific about measurable outcomes like cost savings, time saved, risk reduction, etc.]

---

## Step-by-Step Flow

### 1. **[First major step]**

[Description of what happens in this step]

```yaml
# Example YAML configuration (if applicable)
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: example-plan
  namespace: hibernator-system
spec:
  schedule:
    timezone: "America/New_York"
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday"]

  targets:
    - name: example-target
      type: [executor-type]
      connectorRef:
        kind: CloudProvider  # or K8SCluster
        name: [connector-name]
      parameters:
        # Executor-specific parameters
```

**Verification:**
```bash
# Commands to verify this step completed successfully
kubectl get hibernateplan example-plan -n hibernator-system
kubectl describe hibernateplan example-plan -n hibernator-system
```

### 2. **[Second major step]**

[Description of what happens in this step]

**Expected output:**
```
[Example output showing success]
```

### 3. **[Third major step]**

[Continue with remaining steps...]

---

## Decision Branches

### Branch A: [Scenario description]

**When:** [Condition that triggers this branch]

**Steps:**
1. [Specific step for this scenario]
2. [Another step]

### Branch B: [Alternative scenario]

**When:** [Different condition]

**Steps:**
1. [Different approach]
2. [Alternative steps]

---

## Common Examples

### Example 1: [Use case name]

**Scenario:** [Brief description]

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: example-1
  namespace: hibernator-system
spec:
  # Configuration specific to this use case
```

### Example 2: [Another use case]

**Scenario:** [Brief description]

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException  # Or CloudProvider, K8SCluster
metadata:
  name: example-2
  namespace: hibernator-system
spec:
  # Configuration for this use case
```

---

## Troubleshooting

### Issue 1: [Common problem]

**Symptoms:**
- [Observable symptom 1]
- [Observable symptom 2]

**Root cause:** [Why this happens]

**Solution:**
```bash
# Commands to resolve the issue
kubectl get [resource] -n hibernator-system
kubectl logs [pod] -n hibernator-system
```

### Issue 2: [Another common problem]

**Symptoms:** [What users see]

**Solution:** [How to fix it]

---

## Related Journeys

- **[Related Journey 1](./related-journey-1.md)** — [Brief description of relationship]
- **[Related Journey 2](./related-journey-2.md)** — [How it connects to this journey]
- **[Related Journey 3](./related-journey-3.md)** — [Prerequisite or follow-up journey]

---

## Pain Points Solved

- ✅ **[Pain point 1]** — [How this journey addresses it]
- ✅ **[Pain point 2]** — [Solution provided]
- ✅ **[Pain point 3]** — [Improvement delivered]

---

## RFC References

- **[RFC-XXXX](../../enhancements/XXXX-name.md)** — [Brief description of which parts of the RFC this journey covers]
- **[RFC-YYYY](../../enhancements/YYYY-name.md)** — [Another relevant RFC]

---

## Additional Resources

- [Kubernetes RBAC Documentation](https://kubernetes.io/docs/reference/access-authn-authz/rbac/)
- [AWS IAM Roles for Service Accounts (IRSA)](https://docs.aws.amazon.com/eks/latest/userguide/iam-roles-for-service-accounts.html)
- [Related external documentation]

---

**Last Updated:** [Date]
**Status:** [✅ Implemented | 🚀 In Progress | 📋 Planned | ⏳ Proposed]
