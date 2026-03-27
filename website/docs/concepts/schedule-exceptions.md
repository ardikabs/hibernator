# Schedule Exceptions

Schedule exceptions provide temporary deviations from a plan's base schedule. They are defined as independent `ScheduleException` CRDs that reference a `HibernatePlan`.

## Exception Types

### Extend

Adds additional hibernation windows to the base schedule (OR-union):

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: weekend-hibernation
  namespace: hibernator-system
spec:
  planRef:
    name: dev-plan
  type: extend
  validFrom: "2026-02-10T00:00:00Z"
  validUntil: "2026-02-15T23:59:59Z"
  windows:
    - start: "00:00"
      end: "23:59"
      daysOfWeek: ["SAT", "SUN"]
```

**Use case**: Extend hibernation to weekends during a quiet period.

### Suspend

Prevents hibernation during specified windows (carve-out):

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: maintenance-window
  namespace: hibernator-system
spec:
  planRef:
    name: prod-plan
  type: suspend
  validFrom: "2026-02-01T20:00:00Z"
  validUntil: "2026-02-02T02:00:00Z"
  leadTime: "1h"
  windows:
    - start: "21:00"
      end: "02:00"
      daysOfWeek: ["SAT"]
```

**Use case**: Keep resources awake during a maintenance window or incident.

The `leadTime` field creates a buffer before the suspension window. With `leadTime: "1h"`, the controller won't start a new hibernation cycle within 1 hour before the suspension begins.

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

You can combine compatible exception types on the same plan:

| Combination | Allowed | Behavior |
|-------------|---------|----------|
| extend + suspend | Yes | Extend adds windows, suspend carves out from result |
| extend + replace | No | Replace overrides everything |
| suspend + replace | No | Replace overrides everything |
| extend + extend | No | Only one extend per plan at a time |

The controller uses `mergeByType` semantics to combine compatible exception pairs.

## Validation Rules

- `validUntil` must be after `validFrom`
- At least one window must be specified
- `leadTime` is only valid for `suspend` type exceptions
- Temporal overlap between Active exceptions on the same plan is prevented
- The controller automatically transitions states based on time

## See Also

- [API Reference: ScheduleException](../api-reference/index.md#scheduleexception) — Full field documentation
- [User Guide: Schedule Exceptions](../user-guides/schedule-exceptions.md) — Step-by-step operations
