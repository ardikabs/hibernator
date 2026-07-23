# Suspend Scheduled Shutdown for Testing

Your base plan hibernates a service every night from 20:00 to 07:00 — reliable, cost-effective, and automated.

Tonight is different: the QA team is running a long load-test suite that must run until morning. You do not want to edit the base plan (it is shared infrastructure-as-code), and you do not want to remember to re-enable it tomorrow. A `suspend` exception carves out exactly one night.

## Goal

- Keep the base plan untouched and reusable after the test.
- Suppress the **entire Wednesday-night cycle** (20:00 → 07:00) so the environment stays awake.
- Prevent an accidental hibernation start just before the test window with `leadTime`.

## Base Plan

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: nightly-service
  namespace: hibernator-system
spec:
  schedule:
    timezone: "Asia/Jakarta"
    offHours:
      - start: "20:00"
        end: "07:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"]
  execution:
    strategy:
      type: Parallel
  behavior:
    mode: Strict
    retries: 2
  targets:
    - name: service-nodegroups
      type: eks
      connectorRef:
        kind: CloudProvider
        name: aws-dev
      parameters:
        clusterName: service-cluster
        nodeGroups: []
        awaitCompletion:
          enabled: true
```

## The Exception

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: qa-test-night
  namespace: hibernator-system
spec:
  planRef:
    name: nightly-service
  type: suspend
  validFrom: "2026-08-12T00:00:00Z"
  validUntil: "2026-08-13T23:59:59Z"
  leadTime: "30m"
  windows:
    - start: "20:00"
      end: "07:00"
      daysOfWeek: ["WED"]
```

```bash
kubectl apply -f qa-test-exception.yaml
```

## How It Works

- **`type: suspend`** — prevents hibernation during the specified windows. The base schedule is carved out, not replaced.
- **`leadTime: "30m"`** — no **new** hibernation cycle may start within 30 minutes before the suspension window begins. Here: no shutdown starts from **19:30** onward on Wednesday, so the test runner is not interrupted mid-setup.
- The window `20:00 → 07:00` on Wednesday matches the base window exactly — the entire night is suppressed. Thursday night resumes normally with zero manual steps.
- The exception expires automatically at `validUntil` and transitions to `Expired`.

!!! note "LeadTime is optional"
    For an emergency stop (active incident), omit `leadTime` so suppression takes effect immediately. See the [emergency example](../user-guides/schedule-exceptions.md#emergency-incident-override).

## Verification

```bash
# Exception state: Pending → Active → Expired
kubectl get schedex qa-test-night -n hibernator-system

# On Wednesday at 20:00, confirm the plan stays Active
kubectl get hibernateplan nightly-service -n hibernator-system \
  -o jsonpath='{.status.phase}'
# Active (not Hibernating)

# Check exception history on the plan
kubectl get hibernateplan nightly-service -n hibernator-system \
  -o jsonpath='{.status.exceptionReferences}' | jq
```

## Variations

- **Partial evening only** — if the team only needs until 23:59, use `end: "23:59"`. After midnight the base schedule resumes and the plan hibernates 00:00–07:00.
- **Recurring testing window** — e.g., every Tuesday and Thursday: add both days to `daysOfWeek` and extend `validFrom/validUntil` to cover the recurring period.
- **Suspend without leadTime** — for unplanned incidents where you need immediate suppression.
- **Compose with an extend** — a `suspend` and an `extend` can coexist on the same plan: see [Composing Multiple Exceptions](../user-guides/composing-multiple-exceptions.md).

## Next Steps

- [Schedule Exceptions](../user-guides/schedule-exceptions.md) — all types and lifecycle states
- [Event-Mode Overrides](event-mode-overrides.md) — when you need to override targets, not just suppress the schedule
- [Holiday Schedule Replacement](holiday-schedule-replacement.md) — replace the whole schedule for a longer period
