# Weekend Subset Wakeup (suspend + targetOverrides)

Ten RDS instances hibernate every night to save cost. This weekend, developers need just two of them back online for testing — waking all ten wastes money, and hand-patching the restore point is slow and easy to forget to revert.

The answer is a `suspend` exception with `targetOverrides`: for the weekend window only, the wakeup runs a subset. On Monday everything reverts automatically.

## Goal

- Friday night: all 10 databases hibernate as usual.
- Saturday–Sunday: only the 2 dev databases run; the other 8 stay stopped.
- Monday: the next scheduled wakeup brings back all 10. The 2 already running are skipped safely.
- The base plan is never edited.

## Base Plan (unchanged)

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: rds-fleet
  namespace: hibernator-system
spec:
  schedule:
    timezone: "UTC"
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"]

  execution:
    strategy:
      type: Parallel
      maxConcurrency: 4

  behavior:
    mode: Strict

  targets:
    - name: db-prod-01
      type: rds
      connectorRef:
        kind: CloudProvider
        name: aws-production
      parameters:
        selector:
          tags:
            Environment: production
    - name: db-prod-02
      type: rds
      connectorRef:
        kind: CloudProvider
        name: aws-production
      parameters:
        selector:
          tags:
            Environment: production
    # ... db-prod-03 through db-prod-08 identical ...
    - name: db-dev-01
      type: rds
      connectorRef:
        kind: CloudProvider
        name: aws-production
      parameters:
        selector:
          tags:
            Environment: development
    - name: db-dev-02
      type: rds
      connectorRef:
        kind: CloudProvider
        name: aws-production
      parameters:
        selector:
          tags:
            Environment: development
```

## The Exception

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: weekend-dev-subset
  namespace: hibernator-system
spec:
  planRef:
    name: rds-fleet
  type: suspend
  validFrom: "2026-09-06T00:00:00Z"
  validUntil: "2026-09-07T23:59:59Z"
  windows:
    - start: "00:00"
      end: "23:59"
      daysOfWeek: ["SAT", "SUN"]
  targetOverrides:
    - targetName: db-prod-01
      disabled: true
    - targetName: db-prod-02
      disabled: true
    # ... disable db-prod-03 through db-prod-08 the same way ...
    # db-dev-01 and db-dev-02 are unlisted → they follow the base plan as-is.
```

```bash
kubectl apply -f weekend-dev-subset.yaml
```

## How It Works

- **`type: suspend`** is a carve-out: while active, the plan stays awake instead of hibernating. Because the fleet is already `Hibernated` from Friday night, the suspend window triggers a `Hibernated → WakingUp` transition — but only for the enabled targets.
- **`disabled: true` means "stay stopped", not "hibernate".** There is no partial hibernation via suspend. A disabled target keeps its Friday restore data untouched and is marked `Completed` with a `Skipped: disabled by exception ...` message in `status.executions` — visibly skipped, never run.
- **Only what you list changes.** Unlisted targets (`db-dev-01`, `db-dev-02`) and unlisted fields follow the base plan exactly. `parameters`, when set, is a FULL replacement for that target — restate the complete block.
- **Monday is automatic.** Expiry rebuilds from the base plan, so the next wakeup covers all 10; the 2 already-running instances no-op via runner idempotency.
- **`executionOverride` is forbidden on `suspend`.** Weekend subsets change *which* targets run, never the strategy or behavior.

## All Suspend + Override Possibilities

| Situation | Result |
|---|---|
| Plan `Active`, suspend active (overrides or not) | Nothing happens — stays `Active`, no jobs |
| Plan `Hibernated`, suspend active, no overrides | Full wakeup (all targets), as before |
| Plan `Hibernated`, suspend + `disabled` subset | Only enabled targets wake; disabled stay stopped, marked `Skipped` |
| Suspend + `parameters` only (nothing disabled) | All targets wake; listed ones run with replaced params |
| Entry with both `disabled: true` and `parameters` | Target skipped; `parameters` ignored |
| Entry with neither field | No-op; target follows base |
| Override names an unknown target | Skipped with a log line (rejected at admission in normal flow) |
| All targets disabled | Wakeup cycle completes immediately with zero jobs |
| Suspend validity covers now but daily window doesn't match | No wakeup — overrides only matter at a real `Hibernated → WakingUp` transition |
| Suspend expires or is deleted mid-wakeup | Cycle completes its original set (intent is frozen at transition); next cycle uses base |
| Suspend starts mid-execution | No effect until the in-flight cycle finishes — overrides apply at transitions, never interrupt jobs |

## Strategy Notes

- **Sequential / Parallel** — subsets just work; fewer targets, same order and concurrency.
- **DAG** — disabling any target is allowed, upstream or downstream. A disabled target counts as instantly succeeded, so dependents proceed with ordering preserved.
- **Staged** — disabled targets are skipped inside their stages; stages always complete. No ghost entries, no hangs.
- **Only one exception with overrides** may overlap a plan at a time (any type mix). Plain exceptions without overrides can still coexist.

## Verification

```bash
# Exception state (Pending → Active → Expired)
kubectl get schedex weekend-dev-subset -n hibernator-system

# Who ran and who was skipped this cycle
kubectl get hibernateplan rds-fleet -n hibernator-system \
  -o jsonpath='{range .status.executions[*]}{.target}={.state} {.message}{"\n"}{end}'

# No wakeup Jobs for the disabled eight
kubectl get jobs -n hibernator-system \
  -l hibernator.ardikabs.com/plan=rds-fleet,hibernator.ardikabs.com/target=db-prod-01
# No resources found.
```

## Variations

- **Different weekend fleet** — use `parameters` instead of `disabled` to point a target at a different selector (e.g. a weekend replica) while keeping all ten running. See [Event-Mode Overrides](event-mode-overrides.md) for the parameters pattern on `extend`.
- **No hibernation at all this weekend** — use a plain `type: suspend` with no `targetOverrides`. See [Suspend Scheduled Shutdown](suspend-scheduled-shutdown.md).
- **Different schedule AND different targets** — use `type: replace` instead. See [Holiday Schedule Replacement](holiday-schedule-replacement.md).

## Next Steps

- [Schedule Exceptions](../user-guides/schedule-exceptions.md) — all exception types, override rules, and operational semantics
- [Suspend Scheduled Shutdown](suspend-scheduled-shutdown.md) — plain carve-outs without overrides
- [Event-Mode Overrides](event-mode-overrides.md) — overrides on `extend` (extra windows + subset)
