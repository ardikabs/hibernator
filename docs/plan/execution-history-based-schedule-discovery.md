# Plan: Execution History-Based Schedule Discovery

## Summary

Replace the 7-day backwards-search heuristic in `findLastOccurrence` with a
deterministic lookup that consults the plan's `status.executionHistory` to
identify the last known action timestamp. This eliminates the theoretical
edge case where a weekly-only cron schedule falls outside the search window.

## Problem

`internal/scheduler/schedule.go` — `findLastOccurrence()` discovers when a
cron schedule last fired by walking backwards from `now`:

```go
func (e *ScheduleEvaluator) findLastOccurrence(sched cron.Schedule, now time.Time) time.Time {
    searchStart := now.Add(-24 * time.Hour)
    // ... iterate forward until we pass `now`
    // fallback: search -7 days
}
```

The 24-hour primary window works for most daily schedules. The 7-day fallback
covers weekly (e.g., `0 20 * * 1` — Monday 20:00). However:

- A schedule that fires **less frequently** than weekly (hypothetical future
  extension, e.g., monthly maintenance windows) would return a zero-value
  `lastOccurrence`, causing `eval()` to produce incorrect
  `ShouldHibernate` results.
- Even within the 7-day window, long forward-scans on minute-granularity
  crons are wasteful when the answer is already stored in the plan's status.

## Proposed Solution

### High-Level Approach

Use the plan's `status.executionHistory` (specifically the last
`ShutdownExecution.StartedAt` or `WakeupExecution.StartedAt`) as a hint for
the search start time instead of the hardcoded `-24h / -7d` offsets.

### Design

```
                 ┌─────────────────────────────────┐
                 │   ScheduleEvaluator.Evaluate()   │
                 └────────────┬────────────────────┘
                              │
                 ┌────────────▼────────────────────┐
                 │ Caller provides LastKnownAction  │
                 │  (from executionHistory or zero) │
                 └────────────┬────────────────────┘
                              │
              ┌───────────────▼───────────────┐
              │ lastKnownAction.IsZero() ?    │
              │   YES → fall back to -7d scan │
              │   NO  → search from hint      │
              └───────────────────────────────┘
```

1. **Add an optional `LastKnownAction time.Time` field** to a new
   `EvaluateOptions` struct (or as a functional option on
   `ScheduleEvaluator`).

2. **In `findLastOccurrence`**, if the hint is non-zero, use
   `hint.Add(-1 * time.Hour)` as the search start (small buffer for clock
   skew) instead of `now.Add(-24 * time.Hour)`.

3. **In the provider/worker layer**, extract the timestamp from
   `ExecutionHistory`:

   ```go
   var hint time.Time
   if n := len(plan.Status.ExecutionHistory); n > 0 {
       cycle := plan.Status.ExecutionHistory[n-1]
       if cycle.WakeupExecution != nil && cycle.WakeupExecution.StartedAt != nil {
           hint = cycle.WakeupExecution.StartedAt.Time
       } else if cycle.ShutdownExecution != nil && cycle.ShutdownExecution.StartedAt != nil {
           hint = cycle.ShutdownExecution.StartedAt.Time
       }
   }
   ```

4. **Fallback**: When `ExecutionHistory` is empty (new plan, first cycle),
   fall back to the current 7-day scan.

### API Change

```go
// ScheduleEvaluator
type EvalOption func(*evalConfig)

func WithLastKnownAction(t time.Time) EvalOption { ... }

func (e *ScheduleEvaluator) Evaluate(
    window ScheduleWindow,
    exceptions []Exception,
    opts ...EvalOption,
) (*EvaluationResult, error)
```

The public `Evaluate` signature gains a variadic option; existing callers
that pass no option keep the current behavior — zero breaking changes.

### Benefits

| Before | After |
|--------|-------|
| Hardcoded -24h/-7d scan from `now` | Anchored to last real event |
| Breaks for < weekly schedules | Works for any frequency |
| O(n) cron iterations per eval | O(1) when hint is fresh |
| No connection to plan status | Leverages existing status data |

### Risks / Open Questions

- **Clock skew**: The hint comes from `metav1.Time` written by the
  controller; if the controller clock drifts, the hint may be slightly off.
  Mitigated by the 1-hour buffer.
- **History pruning**: `ExecutionHistory` is capped at 5 cycles. If all 5 are
  old, the hint may be stale. In practice the most recent cycle is always the
  freshest.
- **First-cycle cold start**: No history → must fall back to the existing
  scan. This is acceptable since the 7-day window covers all currently
  supported schedule frequencies.

## Task Breakdown

1. [ ] Add `EvalOption` / `WithLastKnownAction` to `ScheduleEvaluator`
2. [ ] Update `findLastOccurrence` to accept hint
3. [ ] Pass hint from provider/worker layer using `ExecutionHistory`
4. [ ] Unit tests: with hint, without hint (fallback), stale hint
5. [ ] Update existing schedule tests to ensure backwards compatibility

## References

- Code Review item 4.3
- `internal/scheduler/schedule.go` — `findLastOccurrence()`
- `api/v1alpha1/hibernateplan_types.go` — `ExecutionHistory`, `ExecutionCycle`
