---
date: June 19, 2026
status: resolved
component: RDS Executor
---

# Findings: RDS executor ignores pending-resource failures during await completion

> Tracked as beads issue: `hib-pcd`

## Problem Description

When the RDS executor's `Shutdown` or `WakeUp` operation encounters a resource in a transitional state and `awaitCompletion` is enabled, the resource is placed on a pending list. A dedicated goroutine then waits for the resource to become ready and re-applies the stop/start action.

If that goroutine encounters a real error (for example, `DescribeDBInstances` fails while waiting, or the follow-up stop/start API call errors), the failure is only logged. The executor still returns `nil` and a success-style result message such as:

```
stopped 0 RDS resource(s), pending 1 resource awaiting state transition; all resources confirmed stopped
```

The user-visible execution status therefore hides the failure, making operational triage difficult.

## Root Cause Analysis

In `internal/executor/rds/rds.go`:

- `handleShutdownAwaitCompletion` (lines 439-521) declares `pendingFailed atomic.Int32` and increments it when a pending resource fails `WaitForAvailable` or the re-issued `Stop`.
- `handleWakeupAwaitCompletion` (lines 527-607) does the same for failures in `WaitForStopped` or the re-issued `Start`.

After `completionWg.Wait()`, the code only reads `timedOut` and `pendingApplied`. `pendingFailed` is never loaded, so:

1. `stats.pending` is not reduced for failed resources.
2. The result message never mentions the failure.
3. The underlying error is logged but not propagated to the runner / user.

Because `Shutdown`/`WakeUp` return `nil`, the runner exits successfully and writes a misleading message to the Kubernetes termination log.

## Proposed Solution

Capture pending-resource failures and include them in `result.Message`, while keeping the executor returning `nil`. Timeout-only cases continue to be reported at message-level (consistent with EC2 timeout behavior and the user's expectation that "awaiting state transition" refers to timeout).

### Decision

Do **not** return an executor error for pending failures. Returning an error would mark the whole target execution as `Failed` and force the plan into an error/retry loop, which is inappropriate for a partial failure where other resources may have succeeded. Instead, surface the failure in the result message so it propagates through the runner termination log, execution status, and notifications.

### Approach

1. In both await-completion handlers, capture each pending failure together with the resource type/identifier and the original error, e.g.:

   ```go
   fmt.Errorf("%s %s: %w", rt, p.id, err)
   ```

2. Keep the handlers returning `string`.

3. After `completionWg.Wait()`:
   - Decrement `stats.pending` for both applied and failed pending resources.
   - Track `stats.failed` for logging.
   - If failures occurred, append a segment such as:
     ```
     ; 1 pending resource failed to transition: instance db-1: <actual error>
     ```
   - Append the existing timeout clause if resources timed out.
   - Only append `"; all resources confirmed stopped/available"` when there are zero failures and zero timeouts.

4. Add unit tests covering pending-failure paths for both shutdown and wakeup.

### Pros

- Failures are surfaced to users through the runner termination log, execution status, and notifications.
- Distinguishes real errors from timeout cases.
- Avoids forcing a partial failure into a plan-level error/retry loop.
- Minimal change; does not alter the happy path or timeout-only path.

### Cons

- A pending failure will not trigger the controller's retry/backoff path automatically. The user must decide to restart the operation if desired.

## Impact of Fix

- Users will see the actual error and affected resource ID instead of a false "all confirmed" message.
- Operational alerts and notifications will correctly reflect failed RDS operations.
- The plan remains in its normal completed state, leaving retry decisions to the user.
