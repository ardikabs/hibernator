# Schedule Exceptions

Schedule exceptions provide temporary deviations from a plan's base schedule. They are defined as independent `ScheduleException` CRDs that reference a `HibernatePlan`.

## Exception Types

### Extend

Adds additional hibernation windows to the base schedule (OR-union):

```yaml
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

**Use case**: A weekday public holiday falls on Wednesday. The base schedule (20:00–06:00 MON–FRI) would wake resources at 06:00, but nobody is working. The extend exception fills the daytime gap (06:00–20:00), creating continuous hibernation from Tuesday 20:00 through Wednesday 20:00.

!!! note
    If your base schedule uses `daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]`, weekends are already fully hibernated — wakeup only triggers on listed days. There is no need to extend for weekends.

### Suspend

Prevents hibernation during specified windows (carve-out):

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: japan-team-debug
  namespace: hibernator-system
spec:
  planRef:
    name: dev-plan
  type: suspend
  validFrom: "2026-02-11T00:00:00Z"
  validUntil: "2026-02-11T23:59:59Z"
  leadTime: "1h"
  windows:
    - start: "20:00"
      end: "23:59"
      daysOfWeek: ["WED"]
```

**Use case**: The base schedule hibernates resources at 20:00 Asia/Jakarta, but a team in Bangalore (IST/UTC+5:30) needs to continue debugging on Wednesday might. The suspend exception holds off hibernation from 20:00–23:59, keeping resources alive past the normal cutoff.

The `leadTime` field creates a buffer before the suspension window. With `leadTime: "1h"`, the controller won't start a new hibernation cycle within 1 hour before the suspension begins (i.e., from 19:00 onward).

### Replace

Completely replaces the base schedule during the exception period:

```yaml
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
    - start: "00:00"
      end: "23:59"
      daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"]
```

**Use case**: Apply a completely different schedule during holidays.

## Lifecycle States

| State | Description |
|-------|-------------|
| `Pending` | Exception is created but not yet within its valid period |
| `Active` | Exception is currently in effect |
| `Expired` | Exception has passed its `validUntil` time |
| `Detached` | Referenced plan no longer exists |

## Composable Exceptions

Multiple exceptions can coexist on the same plan. Whether they are allowed depends on two factors: whether their **schedule windows collide** (overlap in time-of-day on shared days), and whether their **types** form a permitted pair.

### Non-Colliding Windows (Any Type Combination)

If two exceptions target different days or non-overlapping time ranges, they are always allowed regardless of type:

| Combination | Windows Collide? | Allowed | Behavior |
|-------------|-----------------|---------|----------|
| extend + extend | No | Yes | Windows merged during evaluation |
| suspend + suspend | No | Yes | Windows merged during evaluation |
| replace + replace | No | Yes | Each replaces for its own days |
| Any cross-type pair | No | Yes | Each applies independently |

### Colliding Windows (Type Pairing Rules)

When windows do collide, only certain cross-type combinations are permitted:

| Combination | Windows Collide? | Allowed | Behavior |
|-------------|-----------------|---------|----------|
| extend + suspend | Yes | Yes | Suspend carves out from the extended schedule |
| replace + extend | Yes | Yes | Replace becomes the new base; extend adds on top |
| replace + suspend | Yes | Yes | Replace becomes the new base; suspend carves out |
| extend + extend | Yes | No | Redundant — merge into a single extend exception |
| suspend + suspend | Yes | No | Redundant — merge into a single suspend exception |
| replace + replace | Yes | No | Ambiguous — which replacement wins? |

### Evaluation Order

When multiple exceptions are active, the controller composes them in this order:

1. **Replace** (if present): overwrites the base schedule entirely
2. **Extend**: adds windows on top of the effective base (original or replaced)
3. **Suspend**: carves out windows from the effective result

The controller uses `mergeByType` semantics — windows of the same type are merged before cross-type composition.

For practical scenarios showing how to combine exceptions (extend + suspend, replace + extend, replace + suspend), see the [Composing Multiple Exceptions](../user-guides/composing-multiple-exceptions.md) operational guide.

## Validation Rules

- `validUntil` must be after `validFrom`
- At least one window must be specified
- `leadTime` is only valid for `suspend` type exceptions
- **Window-level overlap detection**: exceptions with overlapping validity periods are checked for schedule window collisions (time-of-day + day-of-week), not just temporal overlap
- Same-type exceptions with colliding windows are rejected (merge into one instead)
- Cross-type exceptions with colliding windows are allowed for permitted pairs (see table above)
- The controller automatically transitions exception states based on time

## See Also

- [API Reference: ScheduleException](../api-reference/index.md#scheduleexception) — Full field documentation
- [User Guide: Schedule Exceptions](../user-guides/schedule-exceptions.md) — Step-by-step operations
