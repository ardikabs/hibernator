---
date: August 15, 2026
status: resolved
component: Validation Webhook / Scheduler
---

# Findings: Inconsistent validation of equal start/end schedule windows

## Problem Description

A user asked how equal `start`/`end` times are treated. Investigation showed an inconsistency:

- `HibernatePlan.spec.schedule.offHours` rejects equal start/end at admission.
- `ScheduleException.spec.windows` accepts equal start/end at admission, then fails at runtime in `ParseWindowToCron`, causing schedule evaluation to error and the provider to retry every 3 minutes indefinitely.

Additionally, webhook validation can be bypassed (disabled webhooks, direct API access, or objects created before the validation existed), so runtime protection was needed.

## Root Cause Analysis

1. `HibernatePlanValidator.validateSchedule` contains an explicit `start == end` check.
2. `ScheduleExceptionValidator.validateWindows` validates time format and days but never checks whether `start == end`.
3. `scheduler.evaluateWindows` returns an error immediately when `ParseWindowToCron` fails, causing the entire schedule evaluation to fail.

## Chosen Direction

1. Add the same `start == end` guardrail to `ScheduleExceptionValidator.validateWindows`.
2. Make `scheduler.evaluateWindows` skip invalid windows at runtime as defense-in-depth, so one malformed window cannot break the whole plan.

## Implementation Details

### Webhook guardrail

In `internal/validationwebhook/scheduleexception_validator.go`:

```go
if timePattern.MatchString(window.Start) && timePattern.MatchString(window.End) && window.Start == window.End {
    allErrs = append(allErrs, field.Invalid(
        windowPath.Child("start"),
        window.Start,
        "start and end times must be different; a window requires a clear start and end schedule",
    ))
}
```

### Runtime protection

In `internal/scheduler/schedule.go`, `evaluateWindows` now skips windows that fail `ParseWindowToCron`:

```go
hibernateCron, wakeUpCron, err := ParseWindowToCron(w.Start, w.End, w.DaysOfWeek...)
if err != nil {
    // Defense-in-depth: skip invalid windows rather than failing the entire
    // schedule evaluation. The validating webhook is the primary guardrail;
    // this path handles cases where the webhook was bypassed.
    continue
}
```

## Verification

- `go test ./internal/scheduler/...` — pass
- `go test ./internal/validationwebhook/...` — pass
- `go test ./internal/provider/...` — pass
- `go vet ./internal/scheduler/... ./internal/validationwebhook/...` — pass

## Impact [of Fix]

- Consistent admission rejection for equal start/end across both `HibernatePlan` and `ScheduleException`.
- Runtime resilience: a bypassed or pre-existing invalid window no longer causes infinite schedule-evaluation retry loops.
- Remaining valid windows continue to drive hibernation behavior.
