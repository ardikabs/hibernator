# Dry-Run Rehearsal (NoOp Executor)

You are about to roll out hibernation to production for the first time. The plan you designed is non-trivial — a DAG with failure tolerance — and you want to prove the *shape* end-to-end before it touches a single real resource: do runner Jobs spawn? Do phases transition? Does the DAG respect levels? What does the ledger look like when a target fails and its dependent gets `Aborted`?

The `noop` executor answers all of this. Everything around the execution — controller, schedule, Jobs, status, notifications — runs for real. Only the resource operations are simulated.

## Goal

- Rehearse the exact structure of the intended production plan (same strategy, dependencies, behavior).
- Simulate a shutdown failure on one target and observe `BestEffort` + DAG semantics: failed target retries, dependent gets `Aborted`, cycle completes with failures recorded.
- Produce a checklist for promoting the rehearsed plan to production.

## Configuration

`connectorRef` is required by the API even for `noop` targets — point it at a throwaway connector. The noop executor never calls it.

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: K8SCluster
metadata:
  name: rehearsal
  namespace: hibernator-system
spec:
  k8s:
    inCluster: true
---
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: prod-rehearsal
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
      type: DAG
      maxConcurrency: 2
      dependencies:
        - from: apps
          to: nodes
        - from: nodes
          to: database

  behavior:
    mode: BestEffort
    retries: 2

  targets:
    - name: apps
      type: noop
      connectorRef:
        kind: K8SCluster
        name: rehearsal
      parameters:
        randomDelaySeconds: 10

    - name: nodes
      type: noop
      connectorRef:
        kind: K8SCluster
        name: rehearsal
      parameters:
        randomDelaySeconds: 5
        failureMode: shutdown
        failureMessage: "simulated node drain timeout"

    - name: database
      type: noop
      connectorRef:
        kind: K8SCluster
        name: rehearsal
      parameters:
        randomDelaySeconds: 5
```

```bash
kubectl apply -f prod-rehearsal.yaml
```

`noop` parameters (see the [NoOp executor guide](../user-guides/noop-executor.md)):

| Parameter | Effect |
|-----------|--------|
| `randomDelaySeconds` | Sleeps a random 0–N seconds per operation (max 30) — makes runner behavior realistic |
| `failureMode` | `none` (default), `shutdown`, `wakeup`, or `both` — which operation fails |
| `failureMessage` | Custom error text for the simulated failure |

## How It Executes

At **20:00**, real runner Jobs spawn for the DAG:

1. **`apps`** starts first (DAG level 0), sleeps up to 10s, completes.
2. **`nodes`** starts next — and fails, by design. It retries (`retries: 2`), fails again, and is marked **`Failed`** with your custom message.
3. **`database`** is downstream of `nodes`. With `BestEffort` + DAG, it is marked **`Aborted`** — skipped, not failed.
4. The cycle completes with failures recorded; the plan does not enter `Error` for target failures.

That ledger — `Completed` / `Failed` / `Aborted` — is exactly what a bad night in production will look like. Rehearse reading it now, not then.

If you also want to rehearse alerting, attach a `HibernateNotification` matching this plan's labels — events fire for noop cycles exactly as for real ones. See [Notifications](../user-guides/notifications.md).

## Verification

```bash
# Runner Jobs really ran
kubectl get jobs -n hibernator-system -l hibernator.ardikabs.com/plan=prod-rehearsal

# The expected ledger
kubectl get hibernateplan prod-rehearsal -n hibernator-system \
  -o jsonpath='{range .status.executions[*]}{.target}{"\t"}{.state}{"\t"}{.message}{"\n"}{end}'
```

```
apps       Completed    simulated shutdown completed
nodes      Failed       simulated node drain timeout
database   Aborted      upstream dependency failed
```

```bash
kubectl get hibernateplan prod-rehearsal -n hibernator-system -o jsonpath='{.status.phase}'
```

## Promote to Production — Checklist

1. **Swap executor types** — replace `type: noop` with the real types (`eks`, `rds`, `ec2`, `karpenter`, `workloadscaler`) and their real `parameters` — keep the `execution` and `behavior` blocks exactly as rehearsed. See the [executor parameters reference](../reference/executor-parameters.md).
2. **Point at real connectors** — replace the throwaway `rehearsal` connector with your `CloudProvider`/`K8SCluster`; verify IRSA and role assumption first ([Connectors](../concepts/connectors.md)).
3. **Rehearse your real failure paths** — decide per target what failure means (drain timeout? API throttle?) and confirm `mode: BestEffort` vs `Strict` still matches your intent. See [Error Recovery](../user-guides/error-recovery.md).
4. **Start with one non-production environment** — the rehearsal proves the *controller path*; the first real run proves *your selectors*.
5. **Keep the rehearsal plan** — rename it and reuse it whenever you change plan shape (new dependencies, new strategy) as a regression check.

## Variations

- **Rehearse wakeup failures** — `failureMode: wakeup` (or `both` to fail the whole cycle).
- **Rehearse Strict** — switch `mode: Strict` and watch the cycle halt on the first failure instead.
- **Rehearse an exception** — attach a `ScheduleException` with `targetOverrides` to the noop plan and inspect `status.planSnapshot` — zero risk while learning override semantics.

## Next Steps

- [NoOp Executor](../user-guides/noop-executor.md) — full parameter reference
- [Production-Grade Setup](production-grade-setup.md) — the capstone: cross-account, notifications, governance
- [Troubleshooting](../user-guides/troubleshooting.md) — what to check when real runs misbehave
