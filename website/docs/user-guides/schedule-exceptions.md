# Schedule Exceptions

This guide covers creating and managing temporary schedule overrides using `ScheduleException` resources.

## Extending Hibernation for a Weekend

Add hibernation windows during a period when the team won't be working:

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: conference-weekend
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

```bash
kubectl apply -f exception.yaml
```

The exception adds weekend all-day hibernation on top of the plan's existing weekday schedule.

## Suspending Hibernation for Maintenance

Keep resources awake during a scheduled maintenance window:

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

The `leadTime: "1h"` prevents the controller from starting a new hibernate cycle within 1 hour before the suspension window begins.

## Emergency Incident Override

Immediately prevent hibernation during an active incident:

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: incident-override
  namespace: hibernator-system
spec:
  planRef:
    name: prod-plan
  type: suspend
  validFrom: "2026-02-03T00:00:00Z"
  validUntil: "2026-02-04T23:59:59Z"
  windows:
    - start: "00:00"
      end: "23:59"
      daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"]
```

No `leadTime` — the suspension takes effect immediately.

## Replacing the Schedule for Holidays

Replace the entire schedule during a holiday period:

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: holiday-week
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

During the holiday week, the base schedule is ignored and replaced by the exception windows.

## Monitoring Exceptions

### Check Exception State

```bash
kubectl get scheduleexception -n hibernator-system
# NAME                  PLAN       TYPE      STATE    VALIDFROM                  VALIDUNTIL                 AGE
# conference-weekend    dev-plan   extend    Active   2026-02-10T00:00:00Z       2026-02-15T23:59:59Z       2d
# maintenance-window    prod-plan  suspend   Pending  2026-02-01T20:00:00Z       2026-02-02T02:00:00Z       1h
```

### Check Plan Exception History

```bash
kubectl get hibernateplan dev-plan -n hibernator-system \
  -o jsonpath='{.status.exceptionReferences}' | jq
```

Up to 10 exception references are tracked, ordered by active state first, then most recent.

## Composing Exceptions

You can apply compatible exceptions to the same plan simultaneously:

```bash
# Add weekend hibernation (extend)
kubectl apply -f weekend-extension.yaml

# Keep Friday evening awake for release (suspend)
kubectl apply -f friday-release-window.yaml
```

An `extend` and `suspend` exception can coexist on the same plan. The controller merges them using `mergeByType` semantics.

!!! warning
    A `replace` exception cannot coexist with other active exceptions. It overrides everything.

## Cleaning Up

Exceptions expire automatically based on `validUntil`. You can delete them early:

```bash
kubectl delete scheduleexception conference-weekend -n hibernator-system
```
