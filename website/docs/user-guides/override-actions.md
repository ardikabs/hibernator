# Override Actions

Manually control hibernation operations outside of the regular schedule. Hibernator provides two complementary mechanisms — **Override Action** for persistent schedule bypass and **Restart** for one-shot re-execution.

!!! info "No Force Actions"
    Hibernator does not have a force-hibernate or force-wakeup mechanism. Instead, use **Override Action** to persistently drive a plan toward a target phase, or **Restart** to re-trigger the last executor operation as a one-shot action.

## Override Action

Override Action is a **persistent** manual phase override. While active, the controller ignores the configured schedule entirely and drives the plan toward the specified target phase (hibernate or wakeup). The plan stays locked at the target phase until the override is explicitly deactivated.

### Activate Override

=== "CLI"

    ```bash
    # Force the plan to hibernate immediately
    kubectl hibernator override dev-offhours --to hibernate

    # Force the plan to wake up immediately
    kubectl hibernator override dev-offhours --to wakeup
    ```

=== "kubectl"

    ```bash
    # Force hibernate
    kubectl annotate hibernateplan dev-offhours -n hibernator-system \
      hibernator.ardikabs.com/override-action=true \
      hibernator.ardikabs.com/override-phase-target=hibernate

    # Force wakeup
    kubectl annotate hibernateplan dev-offhours -n hibernator-system \
      hibernator.ardikabs.com/override-action=true \
      hibernator.ardikabs.com/override-phase-target=wakeup
    ```

### Deactivate Override

=== "CLI"

    ```bash
    kubectl hibernator override dev-offhours --disable
    ```

=== "kubectl"

    ```bash
    kubectl annotate hibernateplan dev-offhours -n hibernator-system \
      hibernator.ardikabs.com/override-action- \
      hibernator.ardikabs.com/override-phase-target-
    ```

After deactivation the controller resumes normal schedule evaluation immediately.

### How It Works

1. The controller detects `hibernator.ardikabs.com/override-action=true` and reads the companion `override-phase-target` annotation.
2. Schedule-driven phase transitions are suppressed — the plan will not automatically switch between Active and Hibernated based on the schedule.
3. The plan is driven toward the target phase via executor dispatch.
4. Once the target phase is reached, subsequent reconcile ticks become no-ops (the override remains active, but no work is dispatched).
5. The annotations are **persistent** — the controller never removes them. The user must explicitly deactivate.

!!! warning "Persistent Override"
    Unlike a one-shot action, the override stays active indefinitely. If you forget to deactivate it, the plan will **never** follow its schedule again until the annotations are removed. Always deactivate when done.

### When to Use Override

- **Emergency wakeup**: Need resources running urgently outside of scheduled hours
- **Planned maintenance**: Force hibernation during an off-schedule maintenance window
- **Testing**: Drive the plan through a full hibernate/wakeup cycle without waiting for the schedule
- **Incident response**: Wake up resources and keep them running throughout an incident

---

## Restart

Restart is a **one-shot** action that re-triggers the last executor operation as recorded in `.status.currentOperation`. The controller consumes the annotation atomically before re-execution — it cannot loop.

### Trigger Restart

=== "CLI"

    ```bash
    kubectl hibernator restart dev-offhours
    ```

=== "kubectl"

    ```bash
    kubectl annotate hibernateplan dev-offhours -n hibernator-system \
      hibernator.ardikabs.com/restart=true
    ```

### How It Works

1. The controller detects `hibernator.ardikabs.com/restart=true`.
2. The annotation is **consumed (deleted)** in a single atomic patch before re-execution.
3. The controller reads `.status.currentOperation` to determine the operation:
    - If `currentOperation=hibernate` and the plan is in `Hibernated` phase → re-triggers the hibernation executor.
    - If `currentOperation=wakeup` and the plan is in `Active` phase (with restore data present) → re-triggers the wakeup executor.
4. Phase/operation mismatches are no-ops with a warning; the annotation is still consumed.

### Requirements

- The plan must be in **Active** or **Hibernated** phase (stable resting phases).
- `.status.currentOperation` must be recorded — the plan must have completed at least one full hibernation cycle.
- For wakeup restarts, restore data must exist.

### When to Use Restart

- **Partial success**: Re-apply a hibernation or wakeup that succeeded for some targets but not others
- **Idempotency testing**: Verify that an executor handles repeated invocations correctly
- **Configuration change**: Re-run after updating executor parameters on a target

!!! tip "Restart + Override"
    Restart works both standalone and alongside Override Action. While an override is active and the plan has reached the target phase, you can annotate `restart=true` to re-run the executor one more time.

---

## Quick Comparison

| Feature | Override Action | Restart | Retry |
|---------|-----------------|---------|-------|
| **Behaviour** | Persistent | One-shot | One-shot |
| **When to use** | Bypass schedule entirely | Re-run last operation | Recover from Error phase |
| **Required phase** | Active or Hibernated | Active or Hibernated | Error |
| **Annotation consumed?** | No (user removes) | Yes (atomic) | Yes |
| **CLI command** | `kubectl hibernator override` | `kubectl hibernator restart` | `kubectl hibernator retry` |

!!! note "Retry vs Restart"
    **Retry** is for plans stuck in the `Error` phase — it clears error state and re-attempts the failed operation. **Restart** is for plans already at a stable phase (Active or Hibernated) — it voluntarily re-triggers the last operation. Use the right tool for the situation.

## Annotation Reference

| Annotation | Value | Behaviour |
|------------|-------|-----------|
| `hibernator.ardikabs.com/override-action` | `"true"` | Enables persistent schedule override. Must be paired with `override-phase-target`. |
| `hibernator.ardikabs.com/override-phase-target` | `hibernate` or `wakeup` | Specifies the target phase for override. |
| `hibernator.ardikabs.com/restart` | `"true"` | One-shot re-trigger of last executor operation. Consumed by controller. |
| `hibernator.ardikabs.com/retry-now` | `"true"` | One-shot retry for Error phase plans. Consumed by controller. |
