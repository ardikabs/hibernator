---
rfc: RFC-0006
title: Notification System via HibernateNotification CRD
status: Proposed
date: 2026-02-16
---

# RFC 0006 — Notification System via HibernateNotification CRD

**Keywords:** Notifications, Alerting, Sinks, Webhooks, Slack, Teams, Discord, HibernateNotification, Observability, Event-Driven

**Status:** Proposed 📋

## Summary

This RFC proposes a decoupled, selector-based notification system for the Hibernator Operator. By introducing a new Custom Resource Definition (CRD) called `HibernateNotification`, users can define notification rules (triggers and sinks) that apply to one or multiple `HibernatePlan` resources based on label selectors. This design keeps the core `HibernatePlan` clean while enabling flexible, multi-channel alerting for platform teams.

## Motivation

As Hibernator manages critical infrastructure resources, visibility into execution status is paramount. Currently, operators must rely on checking `kubectl get hibernateplan` or setting up external monitoring (Prometheus/Grafana) to detect failures. There is no built-in mechanism to actively push alerts to communication channels (Slack, Microsoft Teams, Discord, Email) when a hibernation or wakeup cycle fails or succeeds.

Embedding notification configuration directly into `HibernatePlan` leads to repetition and maintenance overhead, especially for platform teams managing hundreds of plans across multiple Cloud environments. A decoupled, intent-based approach is required to allow centralized management of notification policies.

## Goals

- **Decoupled Configuration:** Separate notification logic from execution intent (`HibernatePlan`).
- **Selector-Based Targeting:** Allow a single notification rule to apply to multiple plans via Kubernetes filtering-style (e.g., `matchLabels` or `matchExpressions`).
- **Multi-Channel Support:** Support pluggable sinks (Slack, Discord, Microsoft Teams, Generic Webhook).
- **Granular Triggers:** Filter events by type (e.g., `OnFailure`, `OnSuccess`, `OnPhaseChange`).
- **Secure Integration:** Reference secrets for sensitive data (webhook URLs, tokens) without exposing them in the CRD status or plain text.
- **Non-Blocking Execution:** Ensure notification failures do not disrupt the core hibernation/wakeup lifecycle.

## Non-Goals

- **Complex Routing Logic:** No advanced conditional routing (e.g., "if time > 5PM send to pagerduty").
- **Rich Templating:** Initial implementation will use standard, informative message formats. Custom templating (Go templates) is a future enhancement.
- **Guaranteed Delivery:** The system is "best-effort". Persistent queues or retries for failed notifications are out of scope for the MVP to keep the controller lightweight.

## Proposal

### 1. New CRD: `HibernateNotification`

The `HibernateNotification` resource defines *what* to watch (via selector), *when* to notify (triggers), and *where* to send it (sinks).

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernateNotification
metadata:
  name: prod-db-alerts
  namespace: database-team  # Must be in the same namespace as the target Plans
spec:
  # Selector: Apply this notification to Plans with these labels
  selector:
    matchLabels:
      env: production
      app: database

  # Triggers: When to fire the notification
  onEvents:
    - Failure      # Hibernation or Wakeup failed
    - Recovery     # A plan recovered from a failed state
    # - Start       (Optional: for estetic purposes, not critical for alerting)
    # - PhaseChange (Optional: noisy, for debugging)
    # - Success     (Optional: for audit trails)

  # Sinks: Where to send the notification
  sinks:
    - type: slack
      name: dba-alerts-channel
      secretRef:
        name: slack-webhook
        key: url
    - type: webhook
      name: ops-genie-integration
      url: "https://api.opsgenie.com/v1/alerts/..."
      # headers: ... (optional)
```

### 2. Controller Integration

The notification logic will be integrated into the `HibernatePlan` controller's reconciliation loop, likely as a dedicated "Notification Reconciler" or a hook within the existing loop.

**Workflow:**

1. **Event Detection:** The controller detects a state transition in a `HibernatePlan` (e.g., `Hibernating` -> `Error`).
2. **Selector Lookup:** The controller queries for all `HibernateNotification` resources in the same namespace.
3. **Matching:** It filters the list to find Notifications whose `planSelector` matches the Plan's labels.
4. **Dispatch:** For each matching Notification and Sink, it asynchronously dispatches the event payload.

### 3. Event Payload (Standardized)

The notification payload will be a structured JSON object, allowing sinks to format it appropriately (e.g., Slack blocks, Discord embeds).

```json
{
  "planName": "backend-db-hibernation",
  "namespace": "prod",
  "phase": "Failed",
  "reason": "SnapshotTimeout",
  "message": "RDS snapshot timed out after 10m",
  "timestamp": "2026-02-16T10:00:00Z",
  "cluster": "eks-useast-1"
}
```

## Challenges & Risks

### 1. Performance Overhead

**Challenge:** Listing `HibernateNotification` objects on every `HibernatePlan` status update could add latency, especially with many plans.
**Mitigation:** Use a controller-runtime `Informer` (cache) to list Notifications. The lookup is in-memory and very fast. We will also debounce rapid status updates if necessary.

### 2. Notification Storms

**Challenge:** If a cluster-wide issue causes 100 Plans to fail simultaneously, the system might flood the notification channels (API rate limits).
**Mitigation:**

- **Rate Limiting:** Implement a simple token bucket per Sink or per Notification CR.
- **Aggregation (Future):** Grouping events is complex and out of scope for MVP, but the decoupled design allows adding an "Aggregator" component later.

### 3. Delivery Reliability vs. Blocking

**Challenge:** If Slack is down, the reconciliation loop must not hang.

**Mitigation:** All notifications must be **asynchronous** (goroutines) and **non-blocking**. The controller will fire-and-forget (with a short timeout). We will expose Prometheus metrics for `hibernator_notification_failed_total` to track delivery issues.

### 4. Security (Cross-Namespace)

**Challenge:** A malicious user in Namespace A might try to watch Plans in Namespace B.

**Mitigation:** `HibernateNotification` is **Namespaced**. It can *only* select `HibernatePlans` in the *same* namespace. This enforces standard Kubernetes multi-tenancy boundaries. Cluster-wide notifications (e.g., for admins) would require a separate `ClusterHibernateNotification` resource, which is out of scope for this RFC.

## Implementation Plan

1. **Phase 1: API & Types:** Define `HibernateNotification` CRD and `Sink` types.
2. **Phase 2: Controller Logic:** Implement the `NotificationManager` interface in the controller to handle lookup and dispatch.
3. **Phase 3: Sinks:** Implement specific providers (Slack, Webhook, Discord, Teams).
4. **Phase 4: Testing:** Unit tests for matching logic and integration tests for dispatch.

## Future Enhancements (Post-RFC)

- **ClusterHibernateNotification:** For cluster-wide admin alerts.
- **Template Support:** Allow users to define custom message formats (e.g., Go templates).
- **Deduplication/Grouping:** Intelligent grouping of alerts during outages.
