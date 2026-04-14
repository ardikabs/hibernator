---
rfc: RFC-0006
title: Notification System via HibernateNotification CRD
status: In Progress 🚀
date: 2026-02-16
last-updated: 2026-04-13
---

# RFC 0006 — Notification System via HibernateNotification CRD

**Keywords:** Notifications, Alerting, Sinks, Webhooks, Slack, Teams, Discord, HibernateNotification, Observability, Event-Driven, Hooks, Templates

**Status:** In Progress 🚀

## Summary

This RFC proposes a decoupled, selector-based notification system for the Hibernator Operator. By introducing a new Custom Resource Definition (CRD) called `HibernateNotification`, users can define notification rules (triggers and sinks) that apply to one or multiple `HibernatePlan` resources based on label selectors. Notifications are delivered through a dedicated lifecycle that integrates with the async phase-driven reconciler via `PlanContext`, using pre/post hooks on status transitions to fire events. Users can customize message formatting through Go templates referenced from ConfigMaps.

For Slack-specific formatting modes (`format=text|json`), template behavior per mode, and preset JSON layouts (`default|compact|progress`), see [RFC-0009](./0009-slack-block-kit-notification-format.md).

## Motivation

As Hibernator manages critical infrastructure resources, visibility into execution status is paramount. Currently, operators must rely on checking `kubectl get hibernateplan` or setting up external monitoring (Prometheus/Grafana) to detect failures. There is no built-in mechanism to actively push alerts to communication channels (Slack, Microsoft Teams, Discord, Email) when a hibernation or wakeup cycle fails or succeeds.

Embedding notification configuration directly into `HibernatePlan` leads to repetition and maintenance overhead, especially for platform teams managing hundreds of plans across multiple Cloud environments. A decoupled, intent-based approach is required to allow centralized management of notification policies.

## Goals

- **Decoupled Configuration:** Separate notification logic from execution intent (`HibernatePlan`).
- **Selector-Based Targeting:** Allow a single notification rule to apply to multiple plans via Kubernetes label selectors (`matchLabels` / `matchExpressions`).
- **Multi-Channel Support:** Support pluggable sinks (Slack, Discord, Microsoft Teams, Generic Webhook).
- **Hook-Based Triggers:** Fire notifications at well-defined hook points in the execution lifecycle (pre-execution, post-execution, failure, recovery, phase changes).
- **Custom Templating:** Allow users to define message formats via Go templates (with Sprig functions) stored in ConfigMaps.
- **Secure Integration:** Reference secrets for sensitive data (webhook URLs, tokens) without exposing them in the CRD status or plain text.
- **Non-Blocking Execution:** Ensure notification failures do not disrupt the core hibernation/wakeup lifecycle.
- **Reconciler-Native:** Integrate as a first-class citizen of the async phase-driven reconciler — loaded into `PlanContext` (like `ScheduleException`), distributed to Workers, and dispatched through the status processor hook system.

## Non-Goals

- **Complex Routing Logic:** No advanced conditional routing (e.g., "if time > 5PM send to pagerduty").
- **Guaranteed Delivery:** The system is "best-effort". Persistent queues or retries for failed notifications are out of scope to keep the controller lightweight.
- **ClusterHibernateNotification:** Cluster-wide admin alerts are future work.
- **Deduplication/Grouping:** Intelligent grouping of alerts during outages is future work.

## Proposal

### 1. Hook Points

Notifications fire at well-defined points in the hibernation lifecycle. Each hook point maps to a position in the state handler flow:

| Hook Point | Timing | Status Processor Hook | Description |
|------------|--------|----------------------|-------------|
| `Start` | Right before execution begins | **PreHook** on `Hibernating` / `WakingUp` transition | Fires when the plan transitions from `Active`/`Hibernated` into an execution phase. The status write has not yet been persisted. |
| `Success` | After execution completes successfully | **PostHook** on `Hibernated` / `Active` transition | Fires after all targets complete and the plan transitions to its resting state. |
| `Failure` | When retries exhausted (permanent error) | **PostHook** on `Error` transition, gated by `retryCount >= behavior.retries` | Fires only when all automatic recovery attempts have been exhausted and the plan enters a permanent `Error` state. This is an escalation signal — someone needs to look at this NOW. |
| `PhaseChange` | On every phase transition | **PostHook** on any status write where phase changed | Fires on every `phaseBefore != phaseAfter`. Noisy — intended for audit trails and debugging. |
| `Recovery` | When recovery retry is triggered | **PreHook** on retry transition from `Error` | Early warning signal — fires each time the recovery system retries. Frequency correlates with retry attempts. Pairs with `Failure` as the escalation counterpart: Recovery warns, Failure escalates. |
| `ExecutionProgress` | When a target's execution state changes | **PostHook** on status write where any target state changed | Fires when an individual target transitions state (e.g., Pending→Running, Running→Completed). Only fires on actual state transitions, not on every poll tick. Payload includes `TargetExecution` with the specific target that changed. |

**Hook integration with the status processor:**

State handlers already send `statusprocessor.Update[T]` with `PreHook` and `PostHook` fields. Notification dispatch will be wired into these hooks:

- **PreHook triggers** (`Start`, `Recovery`): Notification fires before the status write is persisted to the API server. If the PreHook returns an error, the status write is aborted (but notification dispatch itself must never return errors — it is fire-and-forget within the hook).
- **PostHook triggers** (`Success`, `Failure`, `PhaseChange`): Notification fires after a successful status write. PostHook errors are logged but do not roll back the write. The `Failure` hook is **conditional** — it only dispatches when `plan.Status.RetryCount >= plan.Spec.Behavior.Retries`, meaning all automatic recovery has been exhausted. On earlier Error transitions (where retries remain), only `Recovery` fires as an early warning.

### 2. PlanContext Integration

`HibernateNotification` resources are loaded into `PlanContext` following the same pattern as `ScheduleException`:

```
Provider.Reconcile()
  ├── fetchAllExceptions()     → planCtx.Exceptions
  ├── fetchAllNotifications()  → planCtx.Notifications   ← NEW
  ├── evaluateSchedule()       → planCtx.Schedule
  └── Store in watchable.Map   → delivered to Worker
```

**Changes to `PlanContext`:**

```go
type PlanContext struct {
    Plan           *hibernatorv1alpha1.HibernatePlan
    Schedule       *ScheduleEvaluation
    Exceptions     []hibernatorv1alpha1.ScheduleException
    Notifications  []hibernatorv1alpha1.HibernateNotification  // NEW
    HasRestoreData bool
    DeliveryNonce  int64
}
```

**Fetch mechanism:** Field index on `HibernateNotification` by label selector match against the plan's labels. The Provider fetches all `HibernateNotification` in the same namespace and filters by selector match, caching results in the PlanContext.

**Worker access:** When a state handler constructs a `statusprocessor.Update`, it can attach notification dispatch logic in the PreHook/PostHook closures, referencing `planCtx.Notifications` for the list of matching notification configs and their sinks.

### 3. New CRD: `HibernateNotification`

The `HibernateNotification` resource defines *what* to watch (via selector), *when* to notify (hook points), and *where* to send it (sinks).

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernateNotification
metadata:
  name: prod-db-alerts
  namespace: database-team  # Must be in the same namespace as the target Plans
spec:
  # Selector: Apply this notification to Plans matching these labels
  selector:
    matchLabels:
      env: production
      app: database

  # Hook points: When to fire the notification
  onEvents:
    - Failure      # Retries exhausted, permanent error (PostHook on Error when retryCount >= retries)
    - Recovery     # Recovery retry triggered (PreHook on retry)
    - Start        # Right before execution begins (PreHook on Hibernating/WakingUp)
    - Success      # Execution completed successfully (PostHook on Hibernated/Active)
    # - PhaseChange  # Every phase transition (noisy, for debugging/audit)

  # Sinks: Where to send the notification
  sinks:
    - name: dba-slack-channel
      type: slack
      secretRef:
        name: slack-webhook-secret
        key: url
      templateRef:              # Optional: custom message template
        name: slack-notification-templates
        key: default.gotpl

    - name: ops-genie-integration
      type: webhook
      secretRef:
        name: opsgenie-webhook-secret
        key: url
      headers:                  # Optional: extra HTTP headers
        - name: Authorization
          valueFrom:
            secretKeyRef:
              name: opsgenie-api-key
              key: token

    - name: platform-discord
      type: discord
      secretRef:
        name: discord-webhook-secret
        key: url
```

### 4. Message Templating

Users can customize notification message formatting via Go templates stored in ConfigMaps. Templates are referenced per-sink through the `templateRef` field.

**ConfigMap structure:**

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: slack-notification-templates
  namespace: database-team
data:
  default.gotpl: |
    {{ if eq .Event "Failure" -}}
    :red_circle: *Hibernation Failed*
    {{ else if eq .Event "Success" -}}
    :white_check_mark: *Hibernation Succeeded*
    {{ else if eq .Event "Start" -}}
    :arrow_forward: *Execution Starting*
    {{ else if eq .Event "Recovery" -}}
    :recycle: *Recovery Triggered*
    {{ else -}}
    :information_source: *Phase Change*
    {{ end -}}
    *Plan:* {{ .Plan.Name }}
    *Namespace:* {{ .Plan.Namespace }}
    *Phase:* {{ .Phase }}
    *Operation:* {{ .Operation | default "N/A" }}
    {{ if .PreviousPhase -*}}
    *Previous Phase:* {{ .PreviousPhase }}
    {{ end -}}
    {{ if .ErrorMessage -*}}
    *Error:* {{ .ErrorMessage }}
    {{ end -}}
    *Timestamp:* {{ .Timestamp | date "2006-01-02 15:04:05 MST" }}
    {{ if .Targets -*}}
    *Targets:*
    {{ range .Targets -}}
    • {{ .Name }} ({{ .Executor }}): {{ .State }}
    {{ end -}}
    {{ end }}
```

**Template data context (input):**

The template receives a consistent `NotificationContext` struct:

```go
type NotificationContext struct {
    // Event metadata
    Event         string    // "Start", "Success", "Failure", "Recovery", "PhaseChange", "ExecutionProgress"
    Timestamp     time.Time
    Phase         string    // Current phase after transition
    PreviousPhase string    // Phase before transition (empty on Start)
    Operation     string    // "Hibernate" or "WakeUp"

    // Plan metadata
    Plan          PlanInfo  // Name, Namespace, Labels
    CycleID       string    // Current execution cycle ID

    // Execution details (available on Success/Failure)
    Targets       []TargetInfo    // Per-target execution state

    // Target-level progress (ExecutionProgress only)
    TargetExecution *TargetInfo   // The specific target whose state just changed (nil for plan-level events)

    ErrorMessage  string          // Error details (Failure/Recovery only)
    RetryCount    int32           // Current retry attempt (Recovery only)

    // Sink metadata
    SinkName      string    // Name of the sink being dispatched to
    SinkType      string    // "slack", "webhook", "discord", "teams"
}
```

**Template engine:**

- Uses `text/template` (not `html/template` — notification payloads are not HTML)
- Includes the [Sprig](https://masterminds.github.io/sprig/) function library (same as Helm), providing `date`, `default`, `upper`, `lower`, `trim`, `toJson`, `toYaml`, etc.
- If `templateRef` is omitted, a built-in default template is used per sink type (e.g., Slack blocks, Discord embeds, plain JSON for webhooks)
- Template rendering errors are logged and fall back to the default template — never block notification dispatch

**ConfigMap naming convention:** The `templateRef.key` must end in `.gotpl` by convention (enforced by webhook validation) to make the intent clear in the ConfigMap.

### 5. Notification Lifecycle

The notification system has its own dedicated lifecycle within the reconciler, following the same pattern as ScheduleExceptions:

```
┌─────────────────────────────────────────────────────┐
│ Provider (PlanReconciler.Reconcile)                  │
│   1. Fetch HibernateNotification resources           │
│   2. Filter by selector match against plan labels    │
│   3. Store in PlanContext.Notifications               │
│   4. Publish to watchable.Map                        │
└──────────────────────┬──────────────────────────────┘
                       │ PlanContext delivered
                       ▼
┌─────────────────────────────────────────────────────┐
│ Worker (per-plan goroutine)                          │
│   1. Receive PlanContext with Notifications           │
│   2. State handler evaluates phase transition         │
│   3. Constructs statusprocessor.Update with hooks:    │
│      - PreHook: dispatch Start/Recovery notifications │
│      - PostHook: dispatch Success/Failure/PhaseChange │
└──────────────────────┬──────────────────────────────┘
                       │ Update queued
                       ▼
┌─────────────────────────────────────────────────────┐
│ StatusProcessor (UpdateProcessor.apply)               │
│   1. Execute PreHook → fire pre-execution notifs      │
│   2. Mutate + write status to API server              │
│   3. Execute PostHook → fire post-execution notifs    │
└──────────────────────┬──────────────────────────────┘
                       │ Async dispatch
                       ▼
┌─────────────────────────────────────────────────────┐
│ NotificationDispatcher (async, fire-and-forget)       │
│   1. Resolve sink credentials (Secret lookup)         │
│   2. Render template (ConfigMap lookup + Sprig)       │
│   3. Send to sink endpoint (HTTP POST)                │
│   4. Record metrics (success/failure counters)        │
└─────────────────────────────────────────────────────┘
```

**Key design decisions:**

1. **Notifications are loaded once per reconcile** (in the Provider), not per-event. The Worker operates on the cached list in PlanContext.
2. **Hook closures capture the notification list** from the current PlanContext. If notifications change between reconciles, the next PlanContext delivery carries the updated list.
3. **The NotificationDispatcher is non-blocking.** It runs in a separate goroutine with a short HTTP timeout (5s default). Dispatch failures are recorded in metrics but never propagate errors back to the status processor or worker.
4. **Secret and ConfigMap lookups are cached** via the controller-runtime informer cache. No uncached reads for notification delivery.

### 6. Event Payload (Standardized)

The notification payload passed to the template engine and to raw webhook sinks:

```json
{
  "event": "Failure",
  "timestamp": "2026-02-16T10:00:00Z",
  "phase": "Error",
  "previousPhase": "Hibernating",
  "operation": "Hibernate",
  "plan": {
    "name": "backend-db-hibernation",
    "namespace": "prod",
    "labels": {
      "env": "production",
      "app": "database"
    }
  },
  "cycleId": "a1b2c3d4",
  "targets": [
    {
      "name": "prod-rds",
      "executor": "rds",
      "state": "Failed",
      "errorMessage": "RDS snapshot timed out after 10m"
    },
    {
      "name": "prod-ec2",
      "executor": "ec2",
      "state": "Completed"
    }
  ],
  "targetExecution": null,
  "errorMessage": "1 of 2 targets failed",
  "retryCount": 3
}
```

For `ExecutionProgress` events, the `targetExecution` field is populated with the specific target whose state changed:

```json
{
  "event": "ExecutionProgress",
  "timestamp": "2026-02-16T10:00:30Z",
  "phase": "Hibernating",
  "previousPhase": "",
  "operation": "Hibernate",
  "plan": {
    "name": "backend-db-hibernation",
    "namespace": "prod"
  },
  "cycleId": "a1b2c3d4",
  "targets": [],
  "targetExecution": {
    "name": "prod-rds",
    "executor": "rds",
    "state": "Running"
  },
  "retryCount": 0
}
```

For webhook sinks without a custom template, this JSON is sent as-is in the HTTP POST body with `Content-Type: application/json`.

## Challenges & Risks

### 1. Performance Overhead

**Challenge:** Listing `HibernateNotification` objects on every `HibernatePlan` reconcile could add latency, especially with many plans.

**Mitigation:** Notifications are fetched in the Provider using the controller-runtime informer cache (in-memory). A field index on `HibernateNotification` by namespace enables O(1) namespace lookups, followed by an in-memory label selector filter. The same pattern used for ScheduleExceptions.

### 2. Notification Storms

**Challenge:** If a cluster-wide issue causes 100 Plans to fail simultaneously, the system might flood the notification channels (API rate limits).

**Mitigation:**

- **Rate Limiting:** Implement a simple token bucket per Sink or per Notification CR in the NotificationDispatcher.
- **Aggregation (Future):** Grouping events is complex and out of scope, but the decoupled dispatcher design allows adding an Aggregator component later.

### 3. Delivery Reliability vs. Blocking

**Challenge:** If Slack is down, the status processor hooks must not hang.

**Mitigation:** The NotificationDispatcher is fully asynchronous and fire-and-forget. Hook functions submit work to the dispatcher's channel and return immediately. The dispatcher runs its own goroutine pool with a 5-second HTTP timeout per request. Prometheus metrics (`hibernator_notification_sent_total`, `hibernator_notification_errors_total`) track delivery success/failure.

### 4. Security (Cross-Namespace)

**Challenge:** A malicious user in Namespace A might try to watch Plans in Namespace B.

**Mitigation:** `HibernateNotification` is **Namespaced**. It can *only* select `HibernatePlans` in the *same* namespace. This enforces standard Kubernetes multi-tenancy boundaries. Cluster-wide notifications (e.g., for admins) would require a separate `ClusterHibernateNotification` resource, which is out of scope for this RFC.

### 5. Template Injection

**Challenge:** Malicious Go templates could execute harmful operations.

**Mitigation:** Templates are rendered with `text/template` (not `html/template`) using a sandboxed `FuncMap` containing only Sprig functions. No access to the filesystem, environment, or Kubernetes API from within templates. Template execution has a timeout (1 second) to prevent infinite loops. Rendering errors fall back to the default template.

### 6. Recovery vs. Failure — The Alert Pair

**Challenge:** With `maxRetries: 10` and exponential backoff, a failing plan could generate 10+ Recovery notifications over 30+ minutes if `Recovery` is subscribed.

**Design:** Recovery and Failure form a deliberate alert pair with distinct roles:

- **Recovery (early warning):** Fires on each retry attempt. Useful for observability dashboards and teams that want real-time visibility into retry progression. Subscribers opt into this frequency.
- **Failure (escalation):** Fires **once**, only when all retries are exhausted (`retryCount >= behavior.retries`). This is the "wake someone up" signal — all automatic recovery has failed, human intervention is required.

**Practical guidance for users:**
- Want real-time retry visibility? Subscribe to `Recovery`.
- Want only actionable alerts when things are truly broken? Subscribe to `Failure` only.
- Want both? Subscribe to both — Recovery provides the play-by-play, Failure is the final verdict.

**Edge case:** When `behavior.retries: 0`, the Failure hook fires immediately on the first Error transition (since `0 >= 0`), and no Recovery notifications are generated.

## Implementation Plan

### Phase 1: CRD & API Types

- Define `HibernateNotification` CRD types in `api/v1alpha1/`
- Define sink types (Slack, Webhook, Discord, Teams)
- Define hook point enum (`Start`, `Success`, `Failure`, `Recovery`, `PhaseChange`)
- Define `NotificationContext` struct for template data
- Add `Notifications` field to `PlanContext`
- Webhook validation: selector required, at least one sink, at least one event, `templateRef.key` must end in `.gotpl`

### Phase 2: Provider Integration & Notification Loading

- Add `fetchAllNotifications()` in Provider reconciler (field index by namespace, selector match)
- Wire into `PlanContext` construction alongside existing exception loading
- Add controller-runtime watches for `HibernateNotification` changes (trigger plan re-reconcile)
- Add field index setup in `setupIndexes()`

### Phase 3: NotificationDispatcher

- Implement `NotificationDispatcher` as a standalone `Runnable` with:
  - Buffered channel for incoming dispatch requests
  - Goroutine pool for concurrent HTTP delivery
  - Per-sink rate limiting (token bucket)
  - 5-second HTTP timeout per request
  - Prometheus metrics for sent/errors/latency
- Implement sink adapters: Slack (webhook API), Discord (webhook API), Teams (incoming webhook), Generic Webhook (raw JSON POST)
- Register dispatcher in `setup.go`

### Phase 4: Template Engine

- Implement template engine with `text/template` + Sprig function library
- Built-in default templates per sink type (Slack blocks, Discord embeds, plain JSON)
- ConfigMap lookup for custom templates (via `templateRef`)
- 1-second render timeout, fallback to default on error
- Template validation at webhook admission time (parse-only check)

### Phase 5: Hook Wiring in State Handlers

- Wire notification dispatch into status update PreHook/PostHook closures in state handlers:
  - `idleState.transitionToHibernating()` → PreHook: `Start`
  - `idleState.transitionToWakingUp()` → PreHook: `Start`
  - `hibernatingState` completion → PostHook: `Success`
  - `wakingUpState` completion → PostHook: `Success`
  - `→ Error` transition **when `retryCount >= behavior.retries`** → PostHook: `Failure`
  - `recoveryState` retry trigger → PreHook: `Recovery`
  - Any phase change (if subscribed) → PostHook: `PhaseChange`
- Ensure hook closures capture `planCtx.Notifications` and delegate to dispatcher (no blocking)

### Phase 6: Testing

- Unit tests: selector matching, template rendering, hook point mapping, payload construction
- Unit tests: sink adapters (mock HTTP server)
- Unit tests: rate limiting, timeout handling, fallback behavior
- Integration tests (envtest): end-to-end notification flow from phase transition to dispatch
- E2E tests: full cycle with mock webhook endpoint

### Phase 7: Documentation & Samples

- CRD API reference documentation
- User guide: creating notification rules, configuring sinks, writing custom templates
- Sample manifests in `config/samples/`
- Troubleshooting guide for notification delivery issues

## Future Enhancements (Post-RFC)

- **ClusterHibernateNotification:** For cluster-wide admin alerts across all namespaces.
- **Deduplication/Grouping:** Intelligent grouping of alerts during outages (e.g., batch 10 failures into one message).
- **Sink-Specific Rich Formatting:** Native Slack Block Kit builder, Discord embed builder (beyond templates).
- **Delivery Guarantees:** Optional persistent queue (e.g., ConfigMap-backed) for at-least-once delivery.
- **Notification Status Tracking:** Track delivery state per notification in `HibernateNotification.Status`.
