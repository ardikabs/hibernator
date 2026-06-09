# Error Recovery

Hibernator includes automatic retry and manual recovery mechanisms for handling execution failures.

## Automatic Retries

When a runner Job fails, the controller automatically retries with exponential backoff:

- **Backoff formula**: `min(60s × 2^attempt, 30m)`
- **Default retries**: 3 (configurable via `spec.behavior.retries`)
- **Maximum retries**: 10

### Retry Schedule

| Attempt | Delay |
|---------|-------|
| 1 | 60 seconds |
| 2 | 120 seconds |
| 3 | 240 seconds |
| 4 | 480 seconds |
| ... | ... |
| Max | 30 minutes |

### Configure Retries

```yaml
behavior:
  retries: 5    # 0 to disable, max 10
```

## Error Classification

The controller classifies errors as:

| Type | Behavior | Examples |
|------|----------|---------|
| **Transient** | Automatic retry | Network timeout, API throttling, temporary unavailability |
| **Permanent** | No retry, plan enters Error phase | Invalid credentials, missing resource, permission denied |

## Manual Recovery

When automatic retries are exhausted or a permanent error occurs, the plan enters the `Error` phase.

### Retry via Annotation

Trigger a manual retry:

```bash
kubectl annotate hibernateplan dev-offhours -n hibernator-system \
  hibernator.ardikabs.com/retry-now=true
```

This resets the retry counter and immediately attempts the failed operation.

!!! warning "`retry-now` retries the original operation, not the current schedule"
    `retry-now` reads `.status.currentOperation` and retries the **same** operation (hibernate or wakeup) that originally failed. It does **not** re-evaluate the schedule. If the schedule window has shifted since the failure, the plan will retry the *wrong* operation for the current time. See [Schedule Window Mismatch](#schedule-window-mismatch) below for details and workarounds.

### Check Error Details

```bash
# View the error message
kubectl get hibernateplan dev-offhours -n hibernator-system \
  -o jsonpath='{.status.errorMessage}'

# Check retry count
kubectl get hibernateplan dev-offhours -n hibernator-system \
  -o jsonpath='{.status.retryCount}'

# View per-target execution state
kubectl get hibernateplan dev-offhours -n hibernator-system \
  -o jsonpath='{.status.executions}' | jq '.[] | select(.state == "Failed")'
```

### View Runner Logs

```bash
# Find the failed job
kubectl get jobs -n hibernator-system -l hibernator.ardikabs.com/plan=dev-offhours

# Check the pod logs
kubectl logs job/hibernate-runner-dev-offhours-dev-database -n hibernator-system
```

### Schedule Window Mismatch

When a plan fails, `.status.currentOperation` is recorded (e.g., `hibernate`). If the schedule window shifts before the retry is triggered (e.g., the next day arrives and the schedule now says `wakeup`), `retry-now` will still retry the **original** operation.

The controller logs a structured warning when this happens:

```
retrying failed operation that conflicts with current schedule window —
proceeding with original operation; to follow the current schedule,
suspend the plan and perform manual intervention or resubmit the plan
```

This is intentional and safety-first: a recovery attempt must resume from the point of failure to avoid corrupting resource state (e.g., waking up resources that were never fully hibernated).

### How to Skip a Failed Cycle and Follow the Current Schedule

If the schedule window has shifted and you want the plan to follow the current schedule instead of retrying the failed operation, use one of these two approaches:

**1. Suspend Until the Next Schedule (safest)**

Suspend the plan with a deadline that aligns with the next schedule boundary. When the deadline is reached, the controller automatically resumes the plan and evaluates the current schedule.

=== "CLI (Natural Language)"

    ```bash
    # Suspend until the next schedule window (e.g., next morning)
    kubectl hibernator suspend dev-offhours --until "tomorrow at 6am" \
      --reason "skipping failed cycle, waiting for next schedule"
    ```

=== "CLI (Seconds)"

    ```bash
    # Suspend for exactly 8 hours (e.g., until the next on-hours window)
    kubectl hibernator suspend dev-offhours --seconds 28800 \
      --reason "skipping failed cycle"
    ```

=== "kubectl"

    ```bash
    # Set suspension with a deadline
    kubectl patch hibernateplan dev-offhours -n hibernator-system --type=merge -p '{
      "spec":{"suspend":true},
      "metadata":{
        "annotations":{
          "hibernator.ardikabs.com/suspend-until":"2026-01-15T06:00:00Z",
          "hibernator.ardikabs.com/suspend-reason":"skipping failed cycle"
        }
      }
    }'
    ```

When the plan is resumed (either manually or automatically at the deadline), the controller routes it based on the current schedule:

- If the plan was in `Error` from a failed **shutdown**, it resumes to `Active` and schedule evaluation continues
- If the plan was in `Error` from a failed **wakeup**, it resumes to `Hibernated` and schedule evaluation continues

See the [Plan Suspension](plan-suspension.md) guide for full details on auto-resume behavior.

**2. Resubmit the Plan (destructive)**

Delete and re-create the plan to reset all status fields and start fresh with schedule evaluation. This is the fastest option but loses execution history.

```bash
# Export the plan first
kubectl get hibernateplan dev-offhours -n hibernator-system -o yaml > /tmp/dev-offhours-backup.yaml

# Delete the plan (this clears all status fields)
kubectl delete hibernateplan dev-offhours -n hibernator-system

# Re-apply it
kubectl apply -f /tmp/dev-offhours-backup.yaml
```

!!! warning "History Loss"
    Resubmitting the plan permanently discards `.status.executionHistory`, `.status.executions`, and any `.status.retryCount` or `.status.errorMessage`. Only use this if you do not need the failed cycle's history for debugging or audit.

---

## BestEffort Mode

With `behavior.mode: BestEffort`, the plan continues executing other targets even when some fail:

```yaml
behavior:
  mode: BestEffort
  failFast: false
  retries: 3
```

- Failed targets are retried independently
- Downstream DAG dependents of failed targets are marked `Aborted`
- The plan enters `Error` only if all retries are exhausted and the failure affects overall completion

## Recovery Checklist

When a plan is stuck in `Error` phase:

1. **Check the error message**: `kubectl get hibernateplan <name> -o jsonpath='{.status.errorMessage}'`
2. **Check runner logs**: Look at the failed Job's pod logs
3. **Fix the root cause**: Credentials, permissions, resource state, etc.
4. **Check if the schedule window has shifted**: If the failure occurred during a different schedule window than the current time, `retry-now` will retry the *wrong* operation. See [Schedule Window Mismatch](#schedule-window-mismatch).
5. **Retry or workaround**: Apply the `retry-now` annotation (if the schedule window is unchanged) or use a [workaround](#how-to-skip-a-failed-cycle-and-follow-the-current-schedule) if the window has shifted.
6. **Monitor**: Watch the plan phase until it transitions back to a healthy state
