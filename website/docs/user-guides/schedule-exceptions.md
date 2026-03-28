# Schedule Exceptions

This guide covers creating and managing temporary schedule overrides using `ScheduleException` resources.

## Extending Hibernation for a Weekday Holiday

If your base schedule already covers weekday nights (e.g., `start: "20:00"`, `end: "06:00"`, `daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]`), weekends are **already fully hibernated** — wakeup only triggers on listed days. So there's no need to extend for weekends.

A practical use of `extend` is covering a **weekday holiday**. Suppose Wednesday is a public holiday and there's no reason to wake up resources in the morning. The base schedule would wake resources at 06:00 on Wednesday, but you want them to stay hibernated all day. Add an extend exception to cover the daytime gap:

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

```bash
kubectl apply -f exception.yaml
```

This fills the 06:00–20:00 gap on Wednesday, creating continuous hibernation from Tuesday 20:00 through Wednesday 20:00 (when the normal nightly cycle begins again). The base schedule handles the rest — no need to modify it.

## Suspending Hibernation for Cross-Timezone Work

When a team in a different timezone needs resources to stay alive beyond the normal hibernation start time, use a `suspend` exception to hold off hibernation.

**Scenario**: Your base schedule hibernates weekday resources in `Asia/Jakarta` from 20:00 to 06:00. A team in Japan (UTC+9, 2 hours ahead) needs to debug an issue on Wednesday evening and expects resources to stay up past the Jakarta 20:00 cutoff until end of day:

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

The `leadTime: "1h"` prevents the controller from starting a new hibernate cycle within 1 hour before the suspension window begins (i.e., no hibernation from 19:00 onward on Wednesday). Resources stay awake until 23:59 on Wednesday instead of going down at 20:00.

## Emergency Incident Override

To keep resources awake during an active incident that may span the full day:

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
# wednesday-holiday     dev-plan   extend    Active   2026-02-11T00:00:00Z       2026-02-11T23:59:59Z       2d
# japan-team-debug      dev-plan   suspend   Pending  2026-02-11T00:00:00Z       2026-02-11T23:59:59Z       1h
```

### Check Plan Exception History

```bash
kubectl get hibernateplan dev-plan -n hibernator-system \
  -o jsonpath='{.status.exceptionReferences}' | jq
```

Up to 10 exception references are tracked, ordered by active state first, then most recent.

## Composing Exceptions

You can apply compatible exceptions to the same plan simultaneously. For example, an `extend` and `suspend` exception can coexist — the controller merges them using `mergeByType` semantics.

For detailed scenarios covering all type combinations (extend + suspend, replace + extend, replace + suspend) and validation rules, see [Composing Multiple Exceptions](composing-multiple-exceptions.md).

## Cleaning Up

Exceptions expire automatically based on `validUntil`. You can delete them early:

```bash
kubectl delete scheduleexception wednesday-holiday -n hibernator-system
```
