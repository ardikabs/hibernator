# EC2 Instance Hibernation

**Tier:** `[MVP]`

**Personas:** Platform Engineer, SRE, Infrastructure Engineer

**When:** Running standalone EC2 instances (not part of ASG/managed groups) that don't need 24/7 availability

**Why:** EC2 charges by instance-hour; stopping off-peak instances can reduce compute costs significantly.

---

## User Stories

**Story 1:** As an **Infrastructure Engineer**, I want to **automatically stop EC2 instances using tag-based discovery**, so that **I can reduce compute costs with minimal manual effort**.

---

## When/Context

- **Cost reduction:** Pause expensive compute during predictable idle periods
- **Tag-based automation:** Mark which instances to hibernation; operator handles the rest
- **Simplicity:** Works with any EC2 instance, no ASG dependency
- **State preservation:** Instance is paused, not terminated; data and configuration preserved

---

## Business Outcome

Automatically stop EC2 instances during off-hours using tag-based or explicit selection, then restart during business hours.

---

## Step-by-Step Flow

### 1. **Tag EC2 instances for hibernation**

Mark which instances should be hibernated by adding tags:

```bash
# Tag specific instances
aws ec2 create-tags \
  --resources i-0123456789abcdef0 i-fedcba9876543210 \
  --tags Key=Hibernate,Value=true Key=Environment,Value=staging \
  --region us-east-1

# Verify tags
aws ec2 describe-instances \
  --filters "Name=tag:Hibernate,Values=true" \
  --query 'Reservations[*].Instances[*].[InstanceId,Tags]' \
  --region us-east-1
```

### 2. **Configure in HibernationPlan**

Add an EC2 target with tag-based selector:

```yaml
targets:
  - name: worker-instances
    type: ec2
    connectorRef:
      kind: CloudProvider
      name: aws-prod
    parameters:
      selector:
        tags:
          Hibernate: "true"         # Required tag
          Environment: "staging"    # Optional additional filter
      # OR use explicit instance IDs:
      # instanceIds:
      #   - i-0123456789abcdef0
      #   - i-fedcba9876543210
```

**Key concepts highlighted:**
- **`tags` selector**: Match all instances with matching tags (flexible, auto-discovers)
- **`instanceIds`**: Explicit list of instance IDs (fixed, manual maintenance)

### 3. **Understand what happens at hibernation**

When schedule triggers hibernation:

1. **Instances stopped** → EC2 state changes to `stopped`
2. **Restore metadata saved** → Instance IDs and region captured
3. **Billing paused** → No compute charges while stopped (EBS storage still charges)
4. **Data preserved** → EBS volumes attached; data intact

Example state transitions:

```
Before hibernation:
  i-0123456789abcdef0: State=running

During hibernation:
  i-0123456789abcdef0: State=stopping (10-30 sec)
  i-0123456789abcdef0: State=stopped (billing paused)

After wakeup:
  i-0123456789abcdef0: State=pending (10-30 sec)
  i-0123456789abcdef0: State=running
```

### 4. **Configure CloudProvider credentials**

Ensure CloudProvider has permissions:

```yaml
# CloudProvider must allow:
# - ec2:DescribeInstances
# - ec2:StopInstances
# - ec2:StartInstances
# - ec2:DescribeTags
```

### 5. **Verify before first hibernation**

```bash
# Check which instances will be targeted
aws ec2 describe-instances \
  --filters "Name=tag:Hibernate,Values=true" \
  --query 'Reservations[*].Instances[*].[InstanceId,State.Name,Tags]' \
  --region us-east-1

# Verify HibernationPlan config
kubectl get hibernateplan prod-offhours -o yaml | grep -A 10 "ec2:"
```

### 6. **Monitor hibernation**

**During hibernation (20:00):**

```bash
kubectl describe hibernateplan prod-offhours
# status.executions:
#   - target: ec2/worker-instances
#     state: Running
#     message: "Stopping 5 instances"

# In AWS console:
aws ec2 describe-instances \
  --filters "Name=tag:Hibernate,Values=true" \
  --query 'Reservations[*].Instances[*].[InstanceId,State.Name]' \
  --region us-east-1
# Output: stopped
```

**After wakeup (06:00):**

```bash
# Verify instances restarted
aws ec2 describe-instances \
  --filters "Name=tag:Hibernate,Values=true" \
  --query 'Reservations[*].Instances[*].[InstanceId,State.Name]' \
  --region us-east-1
# Output: running
```

---

## Decision Branches

| Decision | Option | Notes |
|----------|--------|-------|
| **Discover by tags or IDs?** | Tags (flexible) | Automatically discovers all tagged instances; scales with infrastructure |
| | Instance IDs (explicit) | Fixed list; requires manual updates when instances added/removed |
| **Which tags to use?** | `Hibernate=true` | Simple, standard tag |
| | Multiple filters | Add Environment, Application, etc. for more precise targeting |
| **Handle failures?** | Strict | Fail if any instance fails to stop |
| | BestEffort | Stop what you can, continue with others |

---

## Outcome

✓ EC2 instances automatically stop during off-hours and restart during business hours using tag-based discovery.

---

## Related Journeys

- [Hibernation Plan Initial Design](hibernation-plan-initial-design.md) — Overview of plan structure
- [Monitor Hibernation Execution](monitor-hibernation-execution.md) — Track execution status
- [Troubleshoot Hibernation Failure](troubleshoot-hibernation-failure.md) — Debug EC2 issues

---

## Pain Points Solved

**RFC-0001:** Manual EC2 stop/start for non-ASG instances via Lambda or CronJobs is fragile and hard to audit. HibernationPlan centralizes with declarative config and status ledger.

---

## RFC References

- **RFC-0001:** Control Plane + Runner Model (EC2 executor, tag-based selection, restore metadata)
