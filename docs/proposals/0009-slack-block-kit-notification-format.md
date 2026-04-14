---
rfc: RFC-0009
title: Slack Notification Formatting Modes (Text and JSON)
status: Proposed
date: 2026-04-13
last-updated: 2026-04-13
---

# RFC 0009 - Slack Notification Formatting Modes (Text and JSON)

**Keywords:** Notifications, Slack, Blocks, Templates, Simplicity, Backward-Compatibility

## Summary

This RFC proposes a simplified Slack formatting model for `HibernateNotification` sinks:

- One main mode selector: `format`.
- Two values only: `text` and `json`.
- One preset selector for JSON mode: `block_layout`.

The key behavior is:

1. `format=text`: template is interpreted as text.
2. `format=json`: if template exists, template output is interpreted as JSON blocks payload.
3. `format=json` without template: use built-in block preset selected by `block_layout`.

This replaces the more complex matrix (`blocks` vs `hybrid`, plus separate `template_mode`) with a simpler operator mental model.

## Motivation

The previous draft introduced too many knobs (`format`, `template_mode`, `layout`, `hybrid`) and created confusion about expected behavior.

Additionally, incoming webhook fields like `channel`, `username`, and `icon_emoji` may not be reliably honored by Slack workspace/app settings, which can make behavior surprising.

This RFC narrows the surface area to predictable controls.

## Goals

- Keep configuration understandable in one read.
- Keep existing text behavior as default.
- Support rich Slack Block Kit output when needed.
- Keep `templateRef` useful in both modes without extra mode flags.
- Avoid optional fields that may be ignored by Slack incoming webhooks.

## Non-Goals

- No CRD changes.
- No interactive Slack actions in this phase.
- No delivery guarantee changes.

## Final Configuration Model

Slack sink config in Secret `config` JSON:

```json
{
  "webhook_url": "https://hooks.slack.com/services/T00/B00/XXX",
  "format": "text",
  "block_layout": "default",
  "max_targets": 8
}
```

| Field | Type | Required | Default | Description |
|---|---|---:|---|---|
| `webhook_url` | string | Yes | - | Slack incoming webhook URL. |
| `format` | string | No | `text` | `text` or `json`. |
| `block_layout` | string | No | `default` | Preset used only when `format=json` and no template is provided (or JSON template parsing fails). |
| `max_targets` | int | No | `8` | Maximum target lines emitted by preset JSON layouts for multi-target events. |

## Field Clarifications

### `max_targets`

`max_targets` only affects built-in JSON preset layouts where a list of targets may be rendered.

- Applies mainly to events carrying `.Targets` (e.g., `Success`, `Failure`).
- Does not affect `ExecutionProgress` when only `.TargetExecution` is shown.
- Helps avoid oversized/noisy Slack payloads.

### `channel`, `username`, `icon_emoji` (intentionally omitted)

These are intentionally omitted from v1 proposal for clarity and predictability.

Reason:

- Slack incoming webhooks are often bound to app/channel defaults.
- Overrides may be ignored depending on Slack app/workspace policy.
- Including them as first-class fields can imply guarantees we cannot provide.

If needed later, they can be added back as optional best-effort fields with explicit caveats.

## Rendering Behavior

### `format=text`

- If `templateRef` exists: render template as text and send as Slack `text` message.
- If `templateRef` is missing: use built-in default text template.
- No Slack blocks are attached.

### `format=json`

- If `templateRef` exists: render template and treat output as JSON payload.
  - Expected shape:
    - object containing at least `blocks`, and optionally `text`.
  - If parsing or validation fails: fallback to preset builder using `block_layout`.
- If `templateRef` is missing: use preset builder using `block_layout`.

Implementation note: include fallback `text` whenever possible for compatibility and accessibility.

## Preset Layouts (`block_layout`)

Proposed values:

- `default`: balanced summary + detail layout.
- `compact`: short high-signal layout for noisy channels.
- `progress`: special layout optimized for `ExecutionProgress` events.

Why `progress` is clearer:

- The intent is event-centric (`ExecutionProgress`), not data-shape-centric.
- Easier to understand for operators reading sink config.

### Event behavior for `progress`

- For `ExecutionProgress`: highlight `.TargetExecution` as primary content.
- For non-`ExecutionProgress` events: fallback to `default` layout behavior.

## Template Forms by `format`

### A) `format=text` template example

```gotpl
{{- if eq .Event "Failure" -}}
:rotating_light: *Hibernation Failed*
{{- else if eq .Event "Success" -}}
:white_check_mark: *Hibernation Completed*
{{- else -}}
:information_source: *{{ .Event }}*
{{- end }}
*Plan:* {{ .Plan.Namespace }}/{{ .Plan.Name }}
*Phase:* {{ .Phase }}
{{- if .ErrorMessage }}
*Error:* {{ .ErrorMessage }}
{{- end }}
```

### B) `format=json` template example

```gotpl
{{- $fallback := printf "[%s] %s/%s phase=%s" .Event .Plan.Namespace .Plan.Name .Phase -}}
{
  "text": {{ $fallback | toJson }},
  "blocks": [
    {
      "type": "header",
      "text": { "type": "plain_text", "text": "Hibernator Notification" }
    },
    {
      "type": "section",
      "text": {
        "type": "mrkdwn",
        "text": {{ (printf "*Event:* %s\\n*Plan:* `%s/%s`\\n*Phase:* `%s`" .Event .Plan.Namespace .Plan.Name .Phase) | toJson }}
      }
    }
  ]
}
```

## Sample Outputs

Given event:

```json
{
  "event": "Failure",
  "operation": "shutdown",
  "phase": "Error",
  "retryCount": 3,
  "errorMessage": "RDS stop timed out after 300s",
  "plan": { "namespace": "prod", "name": "payroll-nightly" },
  "targets": [
    { "name": "rds-main", "executor": "rds", "state": "Failed" },
    { "name": "eks-app", "executor": "eks", "state": "Completed" }
  ]
}
```

### `format=text`

```text
:rotating_light: Hibernation Failed
Plan: prod/payroll-nightly
Phase: Error
Error: RDS stop timed out after 300s
```

### `format=json` + `block_layout=default`

```text
[Header] Hibernation Failed
[Section] Plan: prod/payroll-nightly | Phase: Error | Operation: shutdown
[Section] Error: RDS stop timed out after 300s
[Section] Targets (up to max_targets)
[Context] Retry: 3
```

### `format=json` + `block_layout=progress` (for `ExecutionProgress`)

```text
[Header] Execution Progress
[Section] Target: rds-main (rds)
[Section] State: Running
[Context] Plan: prod/payroll-nightly | Cycle: abc123
```

## Prototype Flow

```go
rendered := s.renderer.Render(ctx, payload, renderOpts...)

switch cfg.Format {
case "text":
    msg := &slackapi.WebhookMessage{Text: rendered}
    return post(msg)

case "json":
    // If template provided, try parse rendered as JSON payload first.
    if hasCustomTemplate {
        if msg, ok := parseJSONWebhookMessage(rendered); ok {
            return post(msg)
        }
    }

    // Fallback: preset builder from block_layout.
    msg := buildPresetJSONMessage(payload, cfg.BlockLayout, cfg.MaxTargets)
    return post(msg)

default:
    // defensive fallback
    msg := &slackapi.WebhookMessage{Text: rendered}
    return post(msg)
}
```

## Backward Compatibility

- Existing secrets with only `webhook_url` continue to work (`format=text` by default).
- Existing text templates continue to work unchanged.
- JSON mode is opt-in and safe by fallback-to-preset on invalid JSON template output.

## Implementation Plan

1. Extend Slack config parser with `format`, `block_layout`, `max_targets`.
2. Implement preset builders: `default`, `compact`, `progress`.
3. Implement JSON template parse path for `format=json`.
4. Add fallback logic and tests.
5. Update docs and examples.

## Risks and Mitigations

| Risk | Mitigation |
|---|---|
| Invalid JSON template output | Fallback to preset `block_layout`. |
| Oversized block payload | Enforce `max_targets`, truncate long detail lines if needed. |
| Layout confusion across events | Define `progress` as `ExecutionProgress`-optimized with fallback to `default` for other events. |

## References

- Base notification architecture: [RFC-0006](./0006-notification-system.md)
