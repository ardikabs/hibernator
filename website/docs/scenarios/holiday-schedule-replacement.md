# Holiday Schedule Replacement (replace + executionOverride)

Your company shuts down between Christmas and New Year. The office environment (`office-plan`: EKS node groups plus the reporting database) normally hibernates weeknights only — but during the shutdown week you want something completely different:

1. **Stay hibernated almost 24/7**, waking only for a daily **10:00–12:00 maintenance window** when internal automation runs.
2. **Run those cycles carefully** — sequentially, tolerating individual failures, with minimal retries. Nobody is watching dashboards on December 26.

`type: replace` swaps the entire schedule; `executionOverride` swaps the strategy and behavior. The base plan goes back to normal automatically when the exception expires.

## Goal

- For **2026-12-24 → 2027-01-01**: ignore the base schedule; hibernate 12:00 → 10:00 (next day), every day.
- During exception cycles: `Sequential` strategy, `BestEffort`, `retries: 1`.
- After Jan 1: resume the base weeknight schedule with zero manual steps.

## Base Plan (unchanged)

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: office-plan
  namespace: hibernator-system
spec:
  schedule:
    timezone: "America/New_York"
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
    retries: 2

  targets:
    - name: office-nodegroups
      type: eks
      connectorRef:
        kind: CloudProvider
        name: aws-production
      parameters:
        clusterName: office-cluster
        nodeGroups: []
        awaitCompletion:
          enabled: true
          timeout: "10m"

    - name: reporting-db
      type: rds
      connectorRef:
        kind: CloudProvider
        name: aws-production
      parameters:
        selector:
          instanceIds: [office-reporting]
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
  name: year-end-shutdown
  namespace: hibernator-system
spec:
  planRef:
    name: office-plan
  type: replace
  validFrom: "2026-12-24T00:00:00Z"
  validUntil: "2027-01-01T23:59:59Z"
  windows:
    - start: "12:00"
      end: "10:00"
      daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"]
  executionOverride:
    strategy:
      type: Sequential
    behavior:
      mode: BestEffort
      retries: 1
```

```bash
kubectl apply -f year-end-exception.yaml
```

!!! tip "One window, not two"
    "Off all day except 10:00–12:00" is a single window: `start: "12:00"`, `end: "10:00"`. Because `end < start`, the window crosses midnight — hibernation runs continuously from noon to 10 AM the next morning, every day. No gaps, no midnight edge cases.

## How It Works

- **`type: replace`** — the base schedule is *ignored* while the exception is active. Only the exception windows drive cycles.
- **`executionOverride` is a FULL replacement of strategy and behavior** for exception cycles. Here: targets run one at a time (`Sequential`), a single failure does not abort the cycle (`BestEffort`), and each target gets only one retry.
- Omitting a field inside `executionOverride` keeps the base behavior for that aspect (e.g., no `strategy` = base strategy).
- **`executionOverride` is only valid on `extend` and `replace`** — not on `suspend`.
- When `validUntil` passes, the exception transitions to `Expired` and the base schedule resumes by itself. Cycle intent for any in-flight cycle stays locked via the plan snapshot (see [Event-Mode Overrides](event-mode-overrides.md)).

**Daily timeline during the shutdown week:**

| Time | What happens |
|------|--------------|
| 00:00–10:00 | `Hibernated` (window active since yesterday noon) |
| 10:00 | Window ends → `WakingUp` → `Active` |
| 10:00–12:00 | Awake — automation does its work |
| 12:00 | Window opens → `Hibernating` → `Hibernated` |
| 12:00–24:00 | `Hibernated`, straight through to next day's 10:00 wakeup |

## Verification

```bash
# Watch the exception lifecycle: Pending → Active → Expired
kubectl get schedex year-end-shutdown -n hibernator-system

# Confirm the override is driving current cycles
kubectl get hibernateplan office-plan -n hibernator-system \
  -o jsonpath='{.status.appliedExceptionOverride}{"\n"}{.status.planSnapshot.execution.strategy.type}{"\n"}'
# year-end-shutdown
# Sequential

# After expiry — exception history is preserved on the plan
kubectl get hibernateplan office-plan -n hibernator-system \
  -o jsonpath='{.status.exceptionReferences}' | jq
```

On the first weekday after January 1, verify the base schedule has resumed (plan follows the 20:00→06:00 MON–FRI window again).

## Variations

- **Full-stop holiday (zero awake time)** — a single `00:00`–`23:59` window for all seven days keeps everything down for the entire period; see the [holiday example](../user-guides/schedule-exceptions.md#replacing-the-schedule-for-holidays).
- **Only soften behavior, keep base schedule** — use `type: extend` with an `executionOverride` and no extra practical windows... or better, decide which schedule you actually want first; `replace` exists precisely so you do not have to reason about unions.
- **Ramp into the holiday** — compose this `replace` with an `extend` for the week *before* it: see [Composing Multiple Exceptions](../user-guides/composing-multiple-exceptions.md).

## Next Steps

- [Schedule Exceptions](../user-guides/schedule-exceptions.md) — types, lifecycle, operational semantics
- [Composing Multiple Exceptions](../user-guides/composing-multiple-exceptions.md) — stacking rules
- [Dry-Run Rehearsal](dry-run-rehearsal.md) — Expert tier: prove a plan shape before it touches production
