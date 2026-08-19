---
date: August 15, 2026
status: resolved
component: Scheduler / PlanRequeueProcessor
---

# Findings: Suspend exception with overnight window overruns into excluded days

## Problem Description

A user reported that a suspend exception configured with:

- Type: `suspend`
- Days: `[TUE, WED]`
- Start: `20:00`
- End: `19:59`
- ValidUntil: a future date (e.g., THU or later)

kept resources alive through THU 00:00 and beyond, instead of ending the suspension at the WED/THU day boundary. The expectation was that the suspension should remain active only up to the end of WED, because WED is the last matching day in `DaysOfWeek`.

The symptom: the plan was not reconciled at the point the exception should have stopped applying, so the cluster stayed active until the far-future `NextHibernateTime`.

## Root Cause Analysis

The schedule evaluator uses two different helpers to reason about the same suspension window:

1. `isInTimeWindows()` in `internal/scheduler/windows.go` only matches the **current calendar day's** `DaysOfWeek`. On THU 00:00, with days `[TUE, WED]`, it correctly reports the exception as **not** active.
2. `findSuspensionEnd()` in `internal/scheduler/schedule.go` extends an overnight (backward) window into the next calendar day **without checking** whether that next day is in `DaysOfWeek`. For a WED `20:00→19:59` window, it returns THU 19:59 as the suspension end.

Because `applySuspend()` uses `findSuspensionEnd()` to compute `NextHibernateTime`, the provider stores `Schedule.NextEvent = THU 19:59 + buffers`. The `PlanRequeueProcessor` arms its timer for that time, so no reconcile fires when the exception actually stops applying at the WED/THU boundary. The cluster therefore stays active until THU 19:59.

This is an inconsistency: the two helpers disagree about whether the overnight portion of a window may extend into a day that is excluded from `DaysOfWeek`.

## Proposed Solutions

### Option A: Fix `findSuspensionEnd()` in the scheduler

Cap the overnight end at 23:59 of the current matching day when the following day is not listed in `DaysOfWeek`. This makes `findSuspensionEnd()` consistent with `isInTimeWindows()`.

- **Pros**: Fixes the actual incorrect `NextHibernateTime`/`NextEvent`; keeps the requeue processor simple; no extra daily reconciles.
- **Cons**: Changes semantics for overnight windows with non-contiguous days (but the new semantics match user intent and the existing `isInTimeWindows()` behavior).

### Option B: Add a day-boundary safety requeue in `PlanRequeueProcessor.computeBoundary()`

When an exception window crosses midnight, force a boundary at 00:05 the next day so the controller re-evaluates after the day boundary.

- **Pros**: Localized change in the requeue processor; acts as a generic safety net.
- **Cons**: `computeBoundary()` must parse windows/times/timezones; it papers over the scheduler bug; `NextHibernateTime` remains wrong in logs/status; adds daily reconciles for every overnight exception; overlaps with planned issue **hib-8mr** (global day-boundary requeue processor).

## Chosen Direction

Proceed with **Option A**: fix the scheduler at the source so that overnight suspension windows do not overrun into days excluded by `DaysOfWeek`.

## Implementation Details

Modified `findSuspensionEnd()` in `internal/scheduler/schedule.go`.

When an overnight window (`end <= start`) is in its first-day portion (`currentTimeMinutes >= startMinutes`), the end is normally computed as the end time on the following day. The fix adds a cap: if the following weekday is **not** listed in the window's `DaysOfWeek`, the end is capped to the last minute of the current matching day.

```go
const (
    dayBoundaryEndHour   = 23
    dayBoundaryEndMinute = 59
)

// ...

if endMinutes <= startMinutes && currentTimeMinutes >= startMinutes {
    // Overnight window, end is tomorrow
    endTime = endTime.Add(24 * time.Hour)

    // Cap the overnight end at the day boundary if the following day is
    // not included in the window's DaysOfWeek.
    tomorrow := now.Add(24 * time.Hour)
    if !dayInDays(tomorrow.Weekday(), w.DaysOfWeek) {
        endTime = time.Date(now.Year(), now.Month(), now.Day(), dayBoundaryEndHour, dayBoundaryEndMinute, 0, 0, now.Location())
    }
}
```

Additional refinements:

- Added named constants `dayBoundaryEndHour` and `dayBoundaryEndMinute` instead of hardcoding `23:59`.
- Removed the redundant `if dayEnd.Before(endTime)` guard: in this branch `endTime` is always on the following day (at least `00:00`), so the day-boundary time (`23:59` today) is always earlier.
- Changed `findSuspensionEnd()` to iterate all matching windows and return the **latest** end time. Previously it returned the first match, which could cause an early requeue when multiple windows overlap.
- Added a `dayInDays()` helper to check weekday membership against `DaysOfWeek` strings.

The regression test `TestSuspendExceptionOvernightWindowCapsAtDayBoundary` in `internal/scheduler/schedule_boundary_test.go` now covers three cases:

1. **Capped path**: Base `20:00 → 06:00` daily; suspend `20:00 → 19:59` on `TUE`/`WED`; clock WED 20:30 → `NextHibernateTime == WED 23:59`.
2. **Non-capped path**: Base `20:00 → 06:00` daily; suspend `20:00 → 06:00` on `MON`–`FRI`; clock WED 20:30 → `NextHibernateTime == THU 06:00` (no cap because THU is in days).
3. **Multiple overlapping windows**: Two WED windows ending at 22:00 and 23:30; clock WED 20:30 → `NextHibernateTime == WED 23:30` (latest end wins).

## Verification

- `go test ./internal/scheduler/...` — pass
- `go test ./internal/provider/processor/requeue/...` — pass
- `go test ./internal/provider/...` — pass
- `go vet ./internal/scheduler/...` — pass

## Impact [of Fix]

- Deterministic behavior: `isInTimeWindows()` and `findSuspensionEnd()` now agree on window boundaries.
- The requeue processor receives a correct `NextEvent` and fires at the right transition time.
- Resources hibernate when the base schedule says they should, rather than lingering because of an over-long suspension end calculation.
- Existing suspend-exception tests (backward windows, weekend carve-out, next-wakeup adjustment) continue to pass.
