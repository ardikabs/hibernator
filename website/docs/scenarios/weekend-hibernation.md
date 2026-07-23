# Weekend Hibernation

You run a development environment that the team only uses during weekday business hours. The goal: **off Friday at 20:00, back Monday at 06:00** — and effectively off for the entire weekend in between, without anyone touching anything.

The surprise: you need *less* configuration than you think. This scenario is all about understanding schedule semantics.

## Goal

- Environment hibernates **Friday 20:00** along with the regular weeknight cycle.
- It stays hibernated through Saturday and Sunday.
- It wakes up **Monday 06:00**, ready for the work week.

## The Key Insight: Wakeup Only Happens on Listed Days

Hibernator evaluates your windows per day. Two rules do all the work here:

1. **Wakeup only triggers on days listed in `daysOfWeek`.** A window that starts Friday 20:00 does not wake up on Saturday or Sunday if those days are not listed.
2. **`end` earlier than `start` crosses midnight.** `start: "20:00"`, `end: "06:00"` means 20:00 today → 06:00 the following day.

So the standard weekday window you already know...

```yaml
  schedule:
    timezone: "Asia/Jakarta"
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
```

...**already gives you the whole weekend for free**:

| When | What happens |
|------|--------------|
| Fri 20:00 | Shutdown begins (Friday is a listed day) |
| Sat 06:00 | *Nothing* — Saturday is not listed, no wakeup |
| Sat – Sun | Plan sits in `Hibernated` |
| Mon 06:00 | Wakeup (Monday is a listed day) |

## Configuration (Option A — off all weekend)

Nothing special needed. A complete plan:

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: dev-weekender
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

  behavior:
    mode: Strict
    retries: 2

  targets:
    - name: dev-nodegroups
      type: eks
      connectorRef:
        kind: CloudProvider
        name: aws-dev
      parameters:
        clusterName: dev-cluster
        nodeGroups: []
        awaitCompletion:
          enabled: true

    - name: dev-database
      type: rds
      connectorRef:
        kind: CloudProvider
        name: aws-dev
      parameters:
        selector:
          instanceIds: [dev-postgres]
        snapshotBeforeStop: true
        awaitCompletion:
          enabled: true
```

This assumes the `aws-dev` CloudProvider connector from the [Nightly Full Shutdown](nightly-full-shutdown.md) scenario already exists.

## Option B — awake on weekend days, off at night

Maybe the team *does* use the environment on weekends — say a support rotation needs it 06:00–20:00 every day, but it should still sleep every night. List all seven days:

```yaml
  schedule:
    timezone: "Asia/Jakarta"
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"]
```

Now wakeup fires every morning including Saturday and Sunday, and shutdown fires every night. The environment is up seven days a week from 06:00 to 20:00 and down every night — weekends behave exactly like weekdays.

!!! tip "Choosing between A and B"
    Count the days you need the environment *awake*, and list exactly those days. The window times apply uniformly to every listed day — if weekend hours should differ from weekday hours, use [multiple windows](../user-guides/multi-window-schedules.md).

## Verification

On Saturday, confirm the plan is parked in `Hibernated` (not cycling):

```bash
kubectl get hibernateplan dev-weekender -n hibernator-system \
  -o jsonpath='{.status.phase}'
# Hibernated
```

On Monday after 06:00, confirm the wakeup completed:

```bash
kubectl get hibernateplan dev-weekender -n hibernator-system \
  -o jsonpath='{range .status.executions[*]}{.target}{"\t"}{.state}{"\n"}{end}'
# dev-nodegroups    Completed
# dev-database      Completed
```

## Variations

- **Public holiday on a weekday** — weekends are handled by `daysOfWeek`, but holidays are not. Add an `extend` exception to keep resources down for the holiday: see [Schedule Exceptions](../user-guides/schedule-exceptions.md).
- **Different weekend hours** — e.g., awake only Saturday morning: add a separate window for `["SAT", "SUN"]`. See [Multi-Window Schedules](../user-guides/multi-window-schedules.md).
- **Edge cases** (windows touching midnight, overlapping days) — see [Schedule Boundaries](../user-guides/schedule-boundaries.md).

## Next Steps

- [Multi-Window Schedules](../user-guides/multi-window-schedules.md) — combine several windows in one plan
- [Schedule Boundaries](../user-guides/schedule-boundaries.md) — exactly how window edges are evaluated
- [Partial Hibernation](partial-hibernation-critical-services.md) — next tier: keep only critical services running
