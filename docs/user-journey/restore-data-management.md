# Restore Data Management

**Tier:** `[Enhanced]`

**Personas:** Platform Engineer, SRE

**When:** Ensuring reliable state preservation during hibernation cycles, especially during edge cases like failures, suspensions, and manual interventions.

**Why:** To prevent data loss and ensure resources are restored to their correct pre-hibernation state, even if the hibernation cycle is interrupted or repeated.

---

> **👉 RFC-0001 Implementation Status**
> This journey covers features from **RFC-0001** (✅ Implemented):
> - Quality-aware restore data (IsLive flag)
> - Annotation-based locking mechanism
> - RestoreManager API
> - Controller orchestration for suspension/unsuspension

---

## User Stories

**Story 1:** As an **SRE**, I want to **ensure my restore points are valid**, so that **I don't overwrite good restore data with empty data if a shutdown runs twice**.

**Story 2:** As a **Platform Engineer**, I want to **automatically recover from interrupted hibernation cycles**, so that **suspending and unsuspending a plan doesn't leave resources in a stuck state**.

**Story 3:** As an **On-Call Engineer**, I want to **know if a restore point exists before attempting a wakeup**, so that **I can fail fast and investigate missing data instead of performing a partial restore**.

---

## When/Context

- **Data Loss Scenario:** Without this management, a second shutdown attempt on already-stopped resources could capture "empty" state, overwriting the original valid state.
- **Suspension:** Users may suspend a plan mid-cycle (while hibernated) and unsuspend it later.
- **Manual Intervention:** Operations teams might manually stop/start resources during a hibernation window.
- **Quality Semantics:**
    - `IsLive=true`: Data captured from running resources (high quality).
    - `IsLive=false`: Data captured from already-shutdown state (low quality).

---

## Business Outcome

Eliminate the risk of restore data corruption during complex operational scenarios (retries, suspensions, manual changes), ensuring 100% reliable restoration of services.

---

## Step-by-Step Flow

### 1. **Understand the Protection Mechanism**

The system automatically protects your data using a **Quality-Aware Preservation** logic. You don't need to configure this; it's built-in.

**Logic Flow:**
1. **Shutdown Triggered:** Runner executes shutdown.
2. **State Capture:** Executor checks if resources are running.
    - If running: Captures state, sets `IsLive=true`.
    - If stopped: Captures empty/stopped state, sets `IsLive=false`.
3. **Preservation Check:** RestoreManager compares new data vs. existing data in ConfigMap.
    - **Existing `IsLive=true` vs New `IsLive=false`**: **PRESERVE EXISTING** (Don't overwrite good data with bad).
    - **Otherwise**: Save new data.

### 2. **Scenario: Protecting Data during Duplicate Shutdowns**

If a schedule re-evaluation or manual trigger causes a second shutdown:

```bash
# 1. First Shutdown (Success)
# ConfigMap saved with isLive=true (High Quality)
kubectl get cm restore-data-prod-offhours -o yaml
# data:
#   ec2_worker: '{"instances":["i-123"],"isLive":true}'

# 2. Second Shutdown (Redundant)
# Resources are already stopped. Executor returns isLive=false.
# Runner logs: "Preserving existing high-quality restore point"

# 3. Verify Data Intact
# ConfigMap still contains the original isLive=true data.
```

### 3. **Scenario: Force Wake-Up on Unsuspend**

If you suspended a plan while it was hibernating, and then unsuspended it during active hours:

1. **Suspend**: You set `spec.suspend: true`.
    - Controller records `suspended-at-phase: Hibernated` in annotations.
2. **Unsuspend**: You set `spec.suspend: false`.
3. **Controller Action**:
    - Detects `suspended-at-phase` was "Hibernated".
    - Detects schedule should be "Active".
    - Checks if valid restore data exists.
    - **Action**: Transitions to `WakingUp` immediately (forcing a restore) instead of just switching to `Active` (which would leave resources stopped).

### 4. **Monitor Restoration Locking**

During wakeup, the system uses annotations to track progress and prevent premature cleanup.

```bash
# Check lock status
kubectl get cm restore-data-prod-offhours -o yaml

# metadata:
#   annotations:
#     hibernator.ardikabs.com/restored-rds-db: "true"
#     hibernator.ardikabs.com/restored-eks-cluster: "true"
```

- **Locked**: Annotations exist. Wakeup is in progress or partially complete.
- **Unlocked**: Annotations cleared. Wakeup fully complete for all targets.

### 5. **Verify Restore Data Quality**

You can inspect the quality of your restore points manually:

```bash
# Get the JSON data for a specific target
kubectl get cm restore-data-prod-offhours -o jsonpath='{.data.ec2_worker-instances}'

# Output:
# {
#   "type": "ec2",
#   "data": { ... },
#   "isLive": true,           <-- Quality Indicator
#   "capturedAt": "2026-02-07T20:00:00Z"
# }
```

---

## Decision Branches

### Branch A: Manual Restoration Needed?

**When:** Restore data is missing or corrupted (very rare with this feature enabled).

**Steps:**
1. Check controller logs for "cannot wake up: no restore point found".
2. Manually start resources via Cloud Console / CLI.
3. The next shutdown cycle will capture fresh `IsLive=true` data.

### Branch B: Clearing Stuck Locks

**When:** A wakeup failed partially, and you want to reset the state for a fresh run.

**Steps:**
1. The controller automatically retries.
2. If permanent failure, you can manually clear annotations (advanced):
   ```bash
   kubectl annotate cm restore-data-prod-offhours hibernator.ardikabs.com/restored-rds-db-
   ```

---

## Common Examples

### Example 1: View Restore Data Quality

```bash
kubectl get cm restore-data-prod-offhours -o json | \
  jq '.data | to_entries[] | .key + ": isLive=" + (.value | fromjson | .isLive | tostring)'
```

**Output:**
```
ec2_worker-instances: isLive=true
rds_database: isLive=true
```

---

## Troubleshooting

### Issue 1: Resources didn't wake up after unsuspend

**Symptoms:**
- Plan is `Active`.
- Resources are `Stopped`.

**Root Cause:**
- `suspended-at-phase` annotation might have been lost or manually deleted.
- Restore data might be missing.

**Solution:**
- Trigger a manual wakeup by editing the status (advanced) or manually start resources.

### Issue 2: Restore Data ConfigMap is missing

**Symptoms:**
- Wakeup fails with "no restore point found".

**Root Cause:**
- ConfigMap was manually deleted.
- Initial shutdown completely failed to save data.

**Solution:**
- Manually start resources.
- Ensure next shutdown succeeds.

---

## Related Journeys

- **[Troubleshoot Hibernation Failure](./troubleshoot-hibernation-failure.md)** — General troubleshooting steps.
- **[Wakeup and Restore Resources](./wakeup-and-restore-resources.md)** — The standard wakeup flow.

---

## Pain Points Solved

- ✅ **Data Loss Prevention** — Prevents overwriting valid restore data with empty state from redundant shutdowns.
- ✅ **Suspension Safety** — Ensures resources don't get "left behind" when unsuspending a plan.
- ✅ **State Integrity** — Uses `IsLive` flag to differentiate between high-quality (running) and low-quality (stopped) captures.

---

## RFC References

- **[RFC-0001](../../enhancements/0001-hibernate-operator.md)** — Core Architecture (Restore Data Management)

---

**Last Updated:** February 2026
**Status:** ✅ Implemented
