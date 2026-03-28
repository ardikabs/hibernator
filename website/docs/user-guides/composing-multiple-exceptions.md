# Composing Multiple Exceptions

This guide covers combining multiple `ScheduleException` resources on the same `HibernatePlan` — when it's allowed, how the controller composes them, and practical scenarios.

## When You Need Multiple Exceptions

A single exception handles one deviation. But real operations often require layered adjustments:

- A **holiday replace** defines a special schedule, but you also need to **suspend** a window for a release deployment
- An **extend** covers a public holiday, but a team still needs resources alive for part of that day (**suspend** carve-out)
- Two separate teams request independent **extend** windows on different days

## Compatibility Rules

Whether two exceptions can coexist depends on two factors: whether their **schedule windows collide** (overlap in time-of-day on shared days), and whether their **types** form a permitted pair.

### Non-Colliding Windows

If two exceptions target different days or non-overlapping time ranges, they are **always allowed** regardless of type:

```yaml
# extend-morning: 06:00–12:00 on WED
# extend-afternoon: 14:00–20:00 on THU
# → No collision. Both accepted and merged during evaluation.
```

### Colliding Windows

When windows overlap in time on the same day, only certain type combinations are permitted:

| Combination | Allowed | Why |
|-------------|---------|-----|
| extend + suspend | Yes | Suspend carves out from extended hibernation |
| replace + extend | Yes | Replace becomes the new base; extend adds on top |
| replace + suspend | Yes | Replace becomes the new base; suspend carves out |
| extend + extend | No | Redundant — merge into a single extend exception |
| suspend + suspend | No | Redundant — merge into a single suspend exception |
| replace + replace | No | Ambiguous — which replacement wins? |

!!! tip
    If you need two overlapping windows of the same type, combine them into a single exception with multiple windows instead.

## Composition Order

When multiple exceptions are active, the controller applies them in a deterministic order:

1. **Replace** (if present): overwrites the base schedule entirely
2. **Extend**: adds windows on top of the effective base (original or replaced)
3. **Suspend**: carves out windows from the effective result

This means a suspend always wins over extend and replace for the time ranges it covers.

## Scenarios

### Extend + Suspend: Holiday with a Maintenance Carve-Out

**Situation**: Wednesday is a public holiday. You want resources hibernated all day (extend), but the SRE team needs a 2-hour maintenance window in the afternoon (suspend).

Base schedule: `20:00–06:00` weekdays.

```yaml
# 1. Extend: keep resources hibernated during the daytime gap
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: wednesday-holiday
  namespace: hibernator-system
spec:
  planRef:
    name: dev-plan
  type: extend
  validFrom: "2026-02-11T00:00:00Z"
  validUntil: "2026-02-11T23:59:59Z"
  windows:
    - start: "06:00"
      end: "20:00"
      daysOfWeek: ["WED"]
```

```yaml
# 2. Suspend: carve out a maintenance window
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: wednesday-maintenance
  namespace: hibernator-system
spec:
  planRef:
    name: dev-plan
  type: suspend
  validFrom: "2026-02-11T00:00:00Z"
  validUntil: "2026-02-11T23:59:59Z"
  leadTime: "30m"
  windows:
    - start: "14:00"
      end: "16:00"
      daysOfWeek: ["WED"]
```

**Result on Wednesday**:

- 00:00–06:00: Hibernated (base overnight window)
- 06:00–13:30: Hibernated (extend fills daytime gap; leadTime prevents hibernate after 13:30)
- 13:30–16:00: Awake (leadTime buffer + suspend carve-out)
- 16:00–20:00: Hibernated (extend resumes)
- 20:00–06:00: Hibernated (base overnight window)

### Replace + Extend: Holiday Week with Extra Coverage

**Situation**: During a holiday week, you replace the base schedule with a reduced window. But Wednesday has a special all-day shutdown need.

```yaml
# 1. Replace: holiday week schedule (hibernate 22:00–08:00 only)
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: holiday-reduced
  namespace: hibernator-system
spec:
  planRef:
    name: prod-plan
  type: replace
  validFrom: "2026-12-24T00:00:00Z"
  validUntil: "2026-12-31T23:59:59Z"
  windows:
    - start: "22:00"
      end: "08:00"
      daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
```

```yaml
# 2. Extend: full day off on Christmas Day (Wednesday)
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: christmas-day-off
  namespace: hibernator-system
spec:
  planRef:
    name: prod-plan
  type: extend
  validFrom: "2026-12-25T00:00:00Z"
  validUntil: "2026-12-25T23:59:59Z"
  windows:
    - start: "08:00"
      end: "22:00"
      daysOfWeek: ["WED"]
```

**Result on Christmas Day**:

- The **replace** sets the base to 22:00–08:00 (instead of the normal schedule)
- The **extend** fills 08:00–22:00, creating continuous hibernation for the full day
- Other holiday weekdays follow the relaxed 22:00–08:00 schedule

### Replace + Suspend: Holiday Schedule with a Release Window

**Situation**: During a holiday week with a custom schedule, you need resources alive for a Friday afternoon release.

```yaml
# 1. Replace: holiday schedule (hibernate 18:00–10:00)
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: holiday-schedule
  namespace: hibernator-system
spec:
  planRef:
    name: prod-plan
  type: replace
  validFrom: "2026-12-24T00:00:00Z"
  validUntil: "2026-12-31T23:59:59Z"
  windows:
    - start: "18:00"
      end: "10:00"
      daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
```

```yaml
# 2. Suspend: keep resources alive for Friday release
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: friday-release
  namespace: hibernator-system
spec:
  planRef:
    name: prod-plan
  type: suspend
  validFrom: "2026-12-26T00:00:00Z"
  validUntil: "2026-12-26T23:59:59Z"
  leadTime: "1h"
  windows:
    - start: "18:00"
      end: "22:00"
      daysOfWeek: ["FRI"]
```

**Result on Friday**:

- 00:00–10:00: Hibernated (replaced schedule's overnight window)
- 10:00–17:00: Awake (replaced schedule's active window; leadTime prevents hibernate after 17:00)
- 17:00–22:00: Awake (leadTime buffer + suspend carve-out)
- 22:00–06:00: Hibernated (replaced schedule resumes)

### Non-Colliding Same-Type: Independent Extend Windows

**Situation**: Two separate events need extra hibernation on different days.

```yaml
# Team A: extend for Monday training day
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: monday-training
  namespace: hibernator-system
spec:
  planRef:
    name: dev-plan
  type: extend
  validFrom: "2026-03-02T00:00:00Z"
  validUntil: "2026-03-02T23:59:59Z"
  windows:
    - start: "06:00"
      end: "20:00"
      daysOfWeek: ["MON"]
```

```yaml
# Team B: extend for Friday offsite
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: friday-offsite
  namespace: hibernator-system
spec:
  planRef:
    name: dev-plan
  type: extend
  validFrom: "2026-03-06T00:00:00Z"
  validUntil: "2026-03-06T23:59:59Z"
  windows:
    - start: "06:00"
      end: "20:00"
      daysOfWeek: ["FRI"]
```

These are accepted because their windows don't collide (different days). The controller merges both during evaluation.

## Validation Errors

The webhook rejects invalid combinations at admission time. Common rejection messages:

| Scenario | Error |
|----------|-------|
| Two extend exceptions with overlapping windows | `same-type exceptions with colliding windows are not allowed; merge into a single exception` |
| Two replace exceptions with overlapping windows | `same-type exceptions with colliding windows are not allowed; only one replace can be active for a given time range` |
| Two suspend exceptions with overlapping windows | `same-type exceptions with colliding windows are not allowed; merge into a single exception` |

## Monitoring Composed Exceptions

Check which exceptions are active on a plan:

```bash
kubectl get scheduleexception -n hibernator-system -l hibernator.ardikabs.com/plan=dev-plan
```

Check the plan's exception references:

```bash
kubectl get hibernateplan dev-plan -n hibernator-system \
  -o jsonpath='{.status.exceptionReferences}' | jq
```

Up to 10 exception references are tracked, ordered by active state first.

## See Also

- [Concepts: Schedule Exceptions](../concepts/schedule-exceptions.md) — Type definitions, lifecycle states, and composition rules
- [User Guide: Schedule Exceptions](schedule-exceptions.md) — Creating individual exceptions
- [Schedule Boundaries](schedule-boundaries.md) — Edge cases in schedule evaluation
