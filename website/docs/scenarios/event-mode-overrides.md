# Event-Mode Overrides (extend + targetOverrides)

Black Friday week is coming. Your production stack normally hibernates weeknights — but this week is different:

1. **Extra hibernation windows** — you want an additional daytime window on the weekend (06:00–11:00) to trim cost during the event's predictable low-traffic hours.
2. **The database must not hibernate at all** — the analytics team needs it around the clock during the event.
3. **The compute target manages a different fleet** — during the event you scale the `event-mode` EC2 fleet, not the regular `production` one.

All of this without editing the base plan. That is what `ScheduleException` with `targetOverrides` does.

## Goal

- Keep the base plan untouched and reusable after the event.
- Add windows with `type: extend`.
- Per target, for the exception window only: swap the frontend target's parameters to the event fleet; exclude the database entirely.

## Base Plan (unchanged)

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: production-plan
  namespace: hibernator-system
spec:
  schedule:
    timezone: "Asia/Jakarta"
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]

  execution:
    strategy:
      type: Parallel
      maxConcurrency: 2

  behavior:
    mode: Strict
    retries: 3

  targets:
    - name: frontend
      type: ec2
      connectorRef:
        kind: CloudProvider
        name: aws-production
      parameters:
        selector:
          tags:
            Environment: production

    - name: backend
      type: eks
      connectorRef:
        kind: CloudProvider
        name: aws-production
      parameters:
        clusterName: production-cluster
        nodeGroups: []
        awaitCompletion:
          enabled: true

    - name: database
      type: rds
      connectorRef:
        kind: CloudProvider
        name: aws-production
      parameters:
        selector:
          tags:
            Environment: production
          discoverInstances: true
        snapshotBeforeStop: true
        awaitCompletion:
          enabled: true
          timeout: "15m"
```

(Uses the `aws-production` CloudProvider from [Dependency-Ordered Shutdown](dependency-ordered-shutdown.md).)

## The Exception

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: black-friday-week
  namespace: hibernator-system
spec:
  planRef:
    name: production-plan
  type: extend
  validFrom: "2026-11-23T00:00:00Z"
  validUntil: "2026-11-30T23:59:59Z"
  windows:
    - start: "06:00"
      end: "11:00"
      daysOfWeek: ["SAT", "SUN"]
  targetOverrides:
    - targetName: frontend
      parameters:
        selector:
          tags:
            Environment: event-mode
    - targetName: database
      disabled: true
```

```bash
kubectl apply -f black-friday-exception.yaml
```

## How It Works

- **`type: extend`** — the exception windows are *added* to the base schedule (union). Weeknights still hibernate 20:00→06:00; the weekend now also hibernates 06:00→11:00. See [Schedule Exceptions](../user-guides/schedule-exceptions.md).
- **`targetOverrides[].parameters` is a FULL replacement, not a merge.** The `frontend` target receives *exactly* the parameters above during exception cycles — anything you omit is gone. Restate the complete parameter block for that executor.
- **`disabled: true`** excludes the target from **both shutdown and wakeup** for the entire exception window. The database simply does not cycle.
- Overrides apply while the exception is active (`validFrom` → `validUntil`). When it expires, the base plan resumes untouched.

!!! warning "Overrides are an advanced feature"
    Execution overrides change the shape of a cycle. A wrong parameter replacement will hibernate the wrong fleet — double-check selectors, and consider a [dry-run rehearsal](dry-run-rehearsal.md) of the plan shape first.

!!! note "Cycle intent is locked at cycle start"
    When a cycle begins, the controller snapshots the resolved intent (`status.planSnapshot`) and uses it for the cycle's lifetime — including retries. You can safely edit or delete the exception mid-cycle: the in-flight cycle keeps its locked intent; the *next* cycle picks up the new state. See [Operational Semantics](../user-guides/schedule-exceptions.md#operational-semantics-retry-resume-and-restart).

## Verification

```bash
# Exception state (Pending → Active → Expired)
kubectl get schedex black-friday-week -n hibernator-system

# Which exception's overrides are driving the current cycle
kubectl get hibernateplan production-plan -n hibernator-system \
  -o jsonpath='{.status.appliedExceptionOverride}'
# black-friday-week

# The effective target list for this cycle — database is gone
kubectl get hibernateplan production-plan -n hibernator-system \
  -o jsonpath='{.status.planSnapshot.targets[*].name}'
# frontend backend
```

## Variations

- **No hibernation at all during the event** — use `type: suspend` with a full-day window instead. Note: `targetOverrides` is only valid on `extend` and `replace`, not on `suspend`.
- **Different schedule AND different strategy** — use `type: replace` with an `executionOverride`: see [Holiday Schedule Replacement](holiday-schedule-replacement.md).
- **Stack multiple exceptions** — an extend plus a suspend can coexist on one plan: see [Composing Multiple Exceptions](../user-guides/composing-multiple-exceptions.md).

## Next Steps

- [Schedule Exceptions](../user-guides/schedule-exceptions.md) — all exception types and lifecycle states
- [Composing Multiple Exceptions](../user-guides/composing-multiple-exceptions.md) — combination rules
- [Holiday Schedule Replacement](holiday-schedule-replacement.md) — replace the whole schedule for a period
