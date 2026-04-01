# Metrics Reference

Hibernator exposes [Prometheus](https://prometheus.io/) metrics via the controller's metrics endpoint (default: `:8080/metrics`). These metrics provide observability into reconciliation, execution, internal pipeline health, and notification delivery.

## Quick Start: Verify Metrics

To quickly verify that metrics are being exposed from the controller, you can use `curl` from within the cluster or via a port-forward.

### 1. Port-forward the controller
```bash
kubectl port-forward -n hibernator-system deployment/hibernator-controller 8080:8080
```

### 2. Query the metrics endpoint
```bash
curl -s http://localhost:8080/metrics | grep hibernator_
```

### 3. Check for specific metrics
Verify execution metrics are flowing:
```bash
curl -s http://localhost:8080/metrics | grep hibernator_execution_total
```

## Scraping Metrics

The controller exposes metrics at the path configured by `--metrics-bind-address` (default `:8080`). To scrape with Prometheus, add a `ServiceMonitor` or a scrape config targeting the controller pod.

---

## Execution Metrics

Metrics for hibernation and wakeup operations against targets.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `hibernator_execution_total` | Counter | `plan`, `operation`, `target_type`, `status` | Total number of hibernation and wakeup operations |
| `hibernator_execution_duration_seconds` | Histogram | `plan`, `operation`, `target_type`, `status` | Duration of hibernation and wakeup operations. Buckets: 1 s to ~17 min (exponential) |

**Label values:**

- `operation`: `Hibernate`, `WakeUp`
- `target_type`: executor type (e.g., `eks`, `rds`, `ec2`, `karpenter`, `workloadscaler`)
- `status`: `success`, `failure`

---

## Reconciliation Metrics

Metrics for the HibernatePlan reconciliation loop.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `hibernator_reconcile_total` | Counter | `plan`, `phase`, `result` | Total number of HibernatePlan reconciliations |
| `hibernator_reconcile_duration_seconds` | Histogram | `plan`, `phase` | Duration of HibernatePlan reconciliation |
| `hibernator_active_plans` | Gauge | `phase` | Number of active HibernatePlans by phase |

---

## Job Metrics

Metrics for runner Jobs created by the controller.


| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `hibernator_jobs_created_total` | Counter | `plan`, `target` | Total number of runner Jobs created |
| `hibernator_job_failures_total` | Counter | `plan`, `target` | Total number of runner Job failures |

**Label values:**

- `plan`: HibernatePlan name
- `target`: Target name

---

## Pipeline Metrics

Internal metrics for the async phase-driven reconciler pipeline (Coordinator, Workers, watchable subscriptions).

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `hibernator_watchable_subscribe_total` | Counter | `runner`, `message`, `status` | Total watchable subscription handler invocations |
| `hibernator_watchable_subscribe_duration_seconds` | Histogram | `runner`, `message` | Duration of watchable subscription handler processing |
| `hibernator_worker_goroutines` | Gauge | — | Number of live plan Worker goroutines managed by the Coordinator |
| `hibernator_enqueue_drop_total` | Counter | `plan` | Plan requeue events dropped because the enqueue channel was full |

**Label values:**

- `status` (subscribe): `success`, `error`, `panic`

!!! note
    A non-zero `hibernator_enqueue_drop_total` signals backpressure on the controller-runtime work queue. Affected plans are reconciled on the next natural trigger (schedule tick, annotation change), but the time-based requeue was silently skipped.

---

## Status Writer Metrics

Metrics for the per-object status writer that batches and deduplicates API server writes.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `hibernator_status_writer_active_objects` | Gauge | `type`, `key` | Number of objects with an active status-writer goroutine |
| `hibernator_status_writer_updates_total` | Counter | `type`, `key` | Total status updates successfully written to the API server |
| `hibernator_status_writer_noop_total` | Counter | `type`, `key` | Status update attempts skipped due to unchanged status |
| `hibernator_status_writer_errors_total` | Counter | `type`, `key`, `event` | Errors during status write operations |

**Label values:**

- `type`: `HibernatePlan`, `ScheduleException`
- `key`: `namespace/name`
- `event` (errors): `pre_hook`, `apply`, `post_hook`

---

## Notification Metrics

Metrics for the async notification dispatcher. See the [Notifications guide](../user-guides/notifications.md) for configuration details.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `hibernator_notification_sent_total` | Counter | `sink_type`, `event` | Successfully delivered notifications |
| `hibernator_notification_errors_total` | Counter | `sink_type`, `event` | Failed notification dispatch attempts |
| `hibernator_notification_latency_seconds` | Histogram | `sink_type` | End-to-end dispatch latency (Secret lookup + render + HTTP POST) |
| `hibernator_notification_drop_total` | Counter | `sink_type`, `event` | Notifications dropped (dispatcher shutdown or buffer full) |

**Label values:**

- `sink_type`: `slack`, `telegram`, `webhook`
- `event`: `Start`, `Success`, `Failure`, `Recovery`, `PhaseChange`

### Example Queries

```promql
# Notification failure rate over 5 minutes
rate(hibernator_notification_errors_total[5m])

# Average notification latency by sink type
rate(hibernator_notification_latency_seconds_sum[5m]) / rate(hibernator_notification_latency_seconds_count[5m])

# Dropped notifications (indicates dispatcher overload)
increase(hibernator_notification_drop_total[1h])
```

---

## Alerting Examples

```yaml
groups:
  - name: hibernator
    rules:
      - alert: HibernatorExecutionFailure
        expr: increase(hibernator_execution_total{status="failure"}[1h]) > 0
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "Hibernation execution failure for {{ $labels.plan }}"

      - alert: HibernatorNotificationErrors
        expr: rate(hibernator_notification_errors_total[5m]) > 0
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: "Notification delivery failing for sink {{ $labels.sink_type }}"

      - alert: HibernatorEnqueueDrops
        expr: increase(hibernator_enqueue_drop_total[30m]) > 0
        labels:
          severity: info
        annotations:
          summary: "Plan requeue events dropped — controller may be under pressure"
```
