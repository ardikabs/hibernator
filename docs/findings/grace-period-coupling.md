---
date: July 10, 2026
status: acked
component: Scheduler, Schedule Evaluation
---

# Findings: Grace Period Logic Couples Side-Effects into eval()

## Problem Description

The `eval()` function in `internal/scheduler/schedule.go` is responsible for
determining the current hibernation state by comparing the most recent
hibernate and wake-up cron events. It also applies a "grace period" (schedule
buffer) to prevent unnecessary phase transitions around window boundaries.

The grace period logic **mutates `nextHibernate` and `nextWakeUp`** as
side-effects within `eval()`:

```go
if gracePeriodEnd.Before(nextHibernate) {
    nextHibernate = gracePeriodEnd  // ← side-effect
}
// ...
if gracePeriodEnd.Before(nextWakeUp) {
    nextWakeUp = gracePeriodEnd     // ← side-effect
}
```

These adjustments exist so that downstream composition functions (e.g.,
`applyExtend`) pick up the correct next-event time even if the `InGracePeriod`
flag is lost during composition. While this works correctly today, the coupling
between schedule evaluation and grace-period adjustments creates several risks.

## Root Cause Analysis

The `eval()` function serves two distinct responsibilities:

1. **Pure schedule evaluation**: parse crons, find last occurrences, determine
   `shouldHibernate` and compute `nextHibernate`/`nextWakeUp`.
2. **Grace period adjustment**: detect if `now` falls within a lead-time
   buffer, flip `shouldHibernate`, and rewrite next-event timestamps.

These are combined in a single function body (\~60 lines), making it
difficult to test or reason about each concern independently.

### Specific Coupling Points

| Line | Coupling |
|------|----------|
| `shouldHibernate = false` / `true` | Grace period inverts the pure cron result |
| `nextHibernate = gracePeriodEnd` | Overwrites the real cron-computed next time |
| `nextWakeUp = gracePeriodEnd` | Same |
| `isContinuous` check | Grace period logic depends on `lastWakeUp` (cron result) |

### Why the Next-Event Mutation Exists

The comment explains: _"Adjust nextHibernate to ensure composition functions
(like applyExtend) pick up the correct next event time even if InGracePeriod
flag is lost."_ This means the grace-period logic is **defensive** against
downstream consumers that don't propagate `InGracePeriod` properly — a form
of action-at-a-distance that's fragile as the composition pipeline grows.

## Impact Assessment

| Risk | Severity | Detail |
|------|----------|--------|
| Testability | **Medium** | Testing pure schedule evaluation requires accounting for grace-period side-effects; testing grace-period logic requires a full cron context. |
| Debugging difficulty | **Medium** | When `nextHibernate` doesn't match the expected cron time, the developer must trace through the grace-period branch to understand why. |
| Composition fragility | **Low-Medium** | Adding new exception types or composition functions must be aware that `nextHibernate`/`nextWakeUp` may already be rewritten by grace-period logic. |
| Correctness today | **None** | Current behavior is correct and well-tested. |

> **Key finding**: The mutation is **functionally necessary**, not a side-effect to be eliminated.
> `applySuspend`'s lead-time check reads `baseResult.NextWakeUpTime` to decide whether the cluster
> would naturally wake before a suspension window starts. During an end-boundary grace period,
> the rewritten `nextWakeUp = gracePeriodEnd` (e.g. `06:05`) correctly signals an imminent wakeup,
> preventing the lead-time suppression from incorrectly keeping the cluster hibernated through
> the suspension. Without the mutation, `nextWakeUp` would show tomorrow's cron time and the
> suspension lead-time check would fire the wrong branch — a real behavioral bug.

## Proposed Solutions

### Option A: Extract Grace Period into a Post-Processing Step

Separate `eval()` into two phases:

```go
// Phase 1: Pure cron evaluation (no side-effects)
func (e *ScheduleEvaluator) evalCron(window ScheduleWindow) (*RawEvalResult, error) {
    // Returns: shouldHibernate, nextHibernate, nextWakeUp, lastHibernate, lastWakeUp
}

// Phase 2: Grace period adjustment (explicit transformation)
func (e *ScheduleEvaluator) applyGracePeriod(raw *RawEvalResult, window ScheduleWindow) *EvaluationResult {
    // Transforms the raw result, clearly documenting what changes and why
}
```

- **Pros**: Each function is single-responsibility and independently testable.
  The next-event mutation is explicit: `applyGracePeriod` clearly returns
  adjusted values.
- **Cons**: Slightly more code. Two structs instead of one.

### Option B: Introduce a `NextActionTime` Field

Instead of mutating `nextHibernate`/`nextWakeUp`, add a new field:

```go
type EvaluationResult struct {
    // ... existing fields ...

    // NextActionTime is when the controller should next take action.
    // During grace periods, this equals GracePeriodEnd.
    // Outside grace periods, this equals min(NextHibernateTime, NextWakeUpTime).
    NextActionTime time.Time
}
```

- **Pros**: `nextHibernate`/`nextWakeUp` remain pure cron values.
  Composition functions use `NextActionTime` instead. No structural refactor
  needed.
- **Cons**: Adds a field that partially overlaps with existing fields.
  Consumers must know to prefer `NextActionTime` over the individual times.

### Option C: Accept Current Design (Status Quo)

Keep `eval()` as-is. The current behavior is correct and the coupling is
well-documented via comments. The risk only materializes if new composition
functions are added that don't understand the rewritten timestamps.

- **Pros**: No code change. No risk of regression.
- **Cons**: Coupling remains; future composition work must be aware of it.

## Recommendation

**Option C (status quo) is the correct choice.** The mutation is not a design smell to be
refactored away — it is the mechanism that makes grace-period-aware composition correct.
Options A and B would produce identical runtime behavior but require migrating all downstream
consumers (`applyExtend`, `applySuspend`, `computeNextEvent`, `ComputeUpcomingEvents`, printers);
missing any one of them would silently reproduce the bug the mutation was designed to prevent.

Accept the current design. Add inline comments to `eval()` marking the coupling explicitly
if a future contributor needs to add a new composition function.

## References

- Code Review item 5.8
- `internal/scheduler/schedule.go` — `eval()`, lines 120–205
- `internal/scheduler/evaluate.go` — composition functions (`applyExtend`, `applySuspend`, `applyReplace`)
