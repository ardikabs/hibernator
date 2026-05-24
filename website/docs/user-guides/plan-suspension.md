# Plan Suspension

Temporarily disable a HibernatePlan without deleting it or losing its configuration.

## Suspending a Plan

Set `spec.suspend: true` to pause all operations:

```bash
kubectl patch hibernateplan dev-offhours -n hibernator-system \
  --type=merge -p '{"spec":{"suspend":true}}'
```

Or edit the YAML directly:

```yaml
spec:
  suspend: true
```

### What Happens

1. The plan transitions to the `Suspended` phase
2. No new hibernation or wakeup cycles are started
3. **Running jobs complete naturally** — the controller doesn't kill active runners
4. The plan retains all configuration and execution history

## Auto-Suspend with Deadline

By default, suspension is **indefinite** and remains active until you explicitly resume the plan. However, you can set an automatic expiration time using the `--until` flag (CLI) or the `suspend-until` annotation (kubectl). When the deadline is reached, the controller automatically removes the suspension and restores normal schedule control.

=== "CLI (Natural Language)"

    ```bash
    # Suspend for 2 hours using relative time
    kubectl hibernator suspend dev-offhours --until "in 2 hours" --reason "maintenance window"

    # Suspend until tomorrow morning
    kubectl hibernator suspend dev-offhours --until "tomorrow at 8am" --reason "scheduled maintenance"

    # Suspend until a specific date/time
    kubectl hibernator suspend dev-offhours --until "2026-01-15 14:30" --reason "holiday freeze"
    ```

=== "CLI (Seconds)"

    ```bash
    # Suspend for exactly 7200 seconds (2 hours)
    kubectl hibernator suspend dev-offhours --seconds 7200 --reason "production deployment"
    ```

=== "kubectl (RFC3339)"

    ```bash
    # Set suspension with automatic expiration using RFC3339 UTC format
    kubectl patch hibernateplan dev-offhours -n hibernator-system \
      --type=merge -p '{
        "spec":{"suspend":true},
        "metadata":{
          "annotations":{
            "hibernator.ardikabs.com/suspend-until":"2026-01-15T14:30:00Z",
            "hibernator.ardikabs.com/suspend-reason":"holiday freeze"
          }
        }
      }'
    ```

**Supported Time Formats:**

| Format Type | CLI Example | Annotation Value |
|-------------|-------------|------------------|
| **Relative** | `in 30 minutes`, `in 2 hours`, `in 1 day` | N/A |
| **Tomorrow** | `tomorrow`, `tomorrow at 6am`, `tomorrow at 14:30` | N/A |
| **Next** | `next Monday`, `next week` | N/A |
| **Date** | `2026-01-15`, `Jan 15, 2026` | N/A |
| **Date+Time** | `2026-01-15 14:30`, `Jan 15, 2026 2:30pm` | N/A |
| **RFC3339** | `2026-01-15T14:30:00Z` | `2026-01-15T14:30:00Z` |

!!! note "Timezone Handling"
    CLI natural language and date/time formats are interpreted in your **local timezone** and stored as UTC internally. The `suspend-until` annotation must always use **RFC3339 format in UTC** (e.g., `2026-01-15T14:30:00Z`).

### How Auto-Resume Works

1. The controller detects `hibernator.ardikabs.com/suspend-until` on the plan.
2. If the current time is before the deadline, the plan stays suspended and the controller schedules an internal deadline timer.
3. When the deadline is reached, the controller automatically:
   - Sets `spec.suspend: false`
   - Removes the `suspend-until` and `suspend-reason` annotations
   - Resumes the plan with the same phase-aware logic used for manual resume (see [Resuming a Plan](#resuming-a-plan))
4. Normal schedule evaluation resumes immediately.

!!! warning "Persistent Suspension"
    Without `--until`, `--seconds`, or the `suspend-until` annotation, the suspension stays active **indefinitely**. If you forget to resume it, the plan will **never** follow its schedule again until `spec.suspend` is set to `false`. Always set an expiration when possible, or remember to resume when done.

## Resuming a Plan

Set `spec.suspend: false` to resume:

```bash
kubectl patch hibernateplan dev-offhours -n hibernator-system \
  --type=merge -p '{"spec":{"suspend":false}}'
```

When you resume a suspended plan, the controller restores it to the appropriate phase based on where it was when suspended and what the current schedule indicates:

### Resuming from Active

The plan returns to `Active` and continues normal schedule evaluation.

### Resuming from Hibernated

The outcome depends on whether the current time falls within off-hours or on-hours:

- **During on-hours (business time)** → the plan wakes up, transitioning through `Hibernated` to `WakingUp`
- **During off-hours (hibernation time)** → the plan returns to `Active`, then the schedule re-evaluates and transitions back to `Hibernating`

!!! note "Idempotent shutdown"
    If you resume a hibernated plan during off-hours, the controller may briefly re-run the shutdown sequence. This is safe because hibernation operations are **idempotent** — re-invoking shutdown on an already-hibernated resource leaves it in the same state. Your resources remain safely shut down.

### Resuming from Hibernating or WakingUp

If the plan was suspended while a hibernation or wakeup operation was in progress:

- **Same schedule window** (e.g., still off-hours for a hibernating plan) → the plan resumes the operation from where it left off
- **Different schedule window** (e.g., now on-hours for a hibernating plan) → the plan routes to the appropriate idle phase (`Active` or `Hibernated`) based on the current schedule

### Resuming from Error

If the plan was in the `Error` phase when suspended, resuming clears the error state and routes to the correct idle phase based on the failed operation:

- **Error occurred during shutdown** → resumes to `Active`
- **Error occurred during wakeup** → resumes to `Hibernated`

!!! note "Resume from Error vs Retry"
    Resuming from suspension is **not** the same as retrying. A resume clears the error state and lets the schedule take over naturally. To immediately retry the failed operation instead, use `kubectl hibernator retry`.

## Use Cases

- **Debugging**: Pause hibernation while investigating a resource issue
- **Manual operations**: Prevent interference during cluster maintenance
- **Cost control investigation**: Temporarily keep resources running to measure baseline costs
- **Holiday freeze**: Pause all automated changes during a change freeze period

!!! tip
    For time-bounded pauses, you have two options:
    - **`suspend-until` annotation** (this page): pauses *all* operations and resumes automatically at the deadline. Use when you want to completely freeze the plan.
    - **[Schedule Exceptions](schedule-exceptions.md) (type `suspend`)**: pauses only schedule-driven transitions while still allowing manual overrides and restarts. Use when you want the schedule to be temporarily inactive but still allow manual control.

## Annotation Reference

| Annotation | Value | Behaviour |
|------------|-------|-----------|
| `hibernator.ardikabs.com/suspend-until` | RFC3339 UTC (e.g., `2026-01-15T14:30:00Z`) | Optional deadline for automatic suspension expiration. When current time exceeds this value, the controller automatically resumes the plan. |
| `hibernator.ardikabs.com/suspend-reason` | Free-form text | Human-readable reason for suspension. Displayed by `kubectl hibernator show status`. |
