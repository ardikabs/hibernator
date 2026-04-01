# Notification Subsystem — Code Review

**Date**: 2026-07-10
**Branch**: `feat/hibernatenotification`
**Scope**: ~1,800 lines across 16 files (6 implementation, 4 test, 6 supporting)
**Status**: Post-refactoring review (TemplateEngine → sink constructor separation complete)

---

## Table of Contents

- [Overall Assessment](#overall-assessment)
- [What's Done Well](#whats-done-well)
- [Issues & Concerns](#issues--concerns)
  - [Critical (Must Fix)](#critical-must-fix)
  - [High (Should Fix)](#high-should-fix)
  - [Medium (Consider)](#medium-consider)
  - [Low / Nits](#low--nits)
- [Architecture Observations](#architecture-observations)
- [Summary Table](#summary-table)
- [Recommendations](#recommendations)

---

## Overall Assessment

The notification subsystem is architecturally sound. The dispatcher-as-Runnable pattern integrates
naturally with controller-runtime's lifecycle. The recent refactoring (TemplateEngine as a
first-class constructor parameter on sinks) cleanly separates concerns. The async fire-and-forget
model with overflow queue is a pragmatic design for a non-critical notification path.

The main risks are in two areas: **template security** (Sprig function exposure + `text/template`
with Telegram HTML mode) and **unbounded resource growth** (overflow queue has no cap). There are
also several hardening opportunities around graceful shutdown, validation, and stale API contracts.

---

## What's Done Well

### 1. Separation of Concerns (Post-Refactoring)

The dispatcher is now purely a coordination component: resolve Secret/ConfigMap, build `SendOptions`,
call `Sink.Send()`. Template rendering is the sink's responsibility via the injected `Renderer`.
This makes sinks independently testable and templating strategy swappable.

**Reference**: `internal/notification/notification.go` — `New()` wiring.

### 2. Overflow Queue Design

The triple-layer buffering (channel → overflow slice → drainer goroutine) is well-designed.
`Submit()` never blocks the caller. The drainer moves items back into the channel with proper
shutdown coordination (`done` channel, `flushOverflow`). The 1-buffered signal channel is correct
for "wake at least once" semantics.

**Reference**: `internal/notification/dispatcher.go` — `Submit()`, `drainOverflow()`.

### 3. Hook Integration via `state_notify.go`

The `notifyHook()` / `phaseChangePostHook()` factory pattern is placement-agnostic: callers
attach hooks as PreHook or PostHook without the hook knowing where it executes. The nil-guard
pattern (`if s.Notifier == nil { return nil }`) and `chainHooks` combinator are clean.

**Reference**: `internal/provider/processor/plan/state/state_notify.go`.

### 4. Template Fallback Chain

The pipeline (custom template → default template → plain-text fallback) ensures that notification
delivery never silently fails due to a template issue. Every error path logs and falls back.

**Reference**: `internal/notification/template.go` — `Render()`.

### 5. Metrics Coverage

Four Prometheus metrics (sent, errors, latency, drops) with `{sink_type, event}` labels provide
actionable observability without cardinality explosion.

**Reference**: `internal/metrics/metrics.go`.

---

## Issues & Concerns

### Critical (Must Fix)

#### C1: Sprig `env` / `expandenv` Functions Exposed in Templates

**File**: `internal/notification/template.go`, line 89

```go
tmpl, err := template.New("render").Funcs(sprig.TxtFuncMap()).Parse(tmplStr)
```

Sprig v3.3.0's `TxtFuncMap()` includes `env` and `expandenv` functions. Any user who can create
a ConfigMap in the operator namespace (via `templateRef`) can read **all** environment variables
from the operator pod:

```
{{ env "AWS_SECRET_ACCESS_KEY" }}
{{ expandenv "$HOME" }}
```

While Sprig v3 removed the `exec` and `shell` functions present in v1/v2, the `env`/`expandenv`
exposure is still a privilege escalation vector. The operator pod may have cloud credentials,
API tokens, or database connection strings in its environment.

**Impact**: Information disclosure — cloud credentials, tokens, internal configuration.

**Fix**: Create a restricted function map that removes dangerous functions:

```go
func safeFuncMap() template.FuncMap {
    fm := sprig.TxtFuncMap()
    for _, name := range []string{"env", "expandenv"} {
        delete(fm, name)
    }
    return fm
}
```

---

#### C2: Unbounded Overflow Queue — Memory Exhaustion Risk

**File**: `internal/notification/dispatcher.go`, lines 96–99

```go
mu       sync.Mutex
overflow []Request
```

The overflow queue grows without limit. If the downstream sink is slow (e.g., Slack webhook
responding in 5s) and notifications arrive faster than the drain rate, the overflow slice grows
unboundedly. In a sustained spike scenario:

- 4 workers × 1 req/5s = 0.8 req/s throughput
- Channel capacity: 256
- If 1000 notifications arrive in a burst: 256 → channel, 744 → overflow
- Continued pressure: overflow grows at ~(incoming - 0.8) items/s

This can cause operator pod OOM kills, which is especially dangerous since the operator
manages infrastructure hibernation schedules.

**Impact**: Operator pod OOM kill → all hibernation management stops.

**Fix**: Add a maximum overflow size with drop-on-full:

```go
const maxOverflowSize = 4096

func (d *Dispatcher) Submit(req Request) {
    // ... existing fast paths ...

    d.mu.Lock()
    if len(d.overflow) >= maxOverflowSize {
        d.mu.Unlock()
        d.log.V(1).Info("overflow queue full, dropping notification",
            "sink", req.SinkName, "event", req.Payload.Event)
        metrics.NotificationDropTotal.WithLabelValues(req.SinkType, req.Payload.Event).Inc()
        return
    }
    d.overflow = append(d.overflow, req)
    d.mu.Unlock()
    // ... signal drainer ...
}
```

---

### High (Should Fix)

#### H1: Goroutine Leak in `executeWithTimeout`

**File**: `internal/notification/template.go`, lines 143–163

```go
func (e *TemplateEngine) executeWithTimeout(tmpl *template.Template, nc NotificationContext) (string, error) {
    ch := make(chan result, 1)
    go func() {
        var buf bytes.Buffer
        err := tmpl.Execute(&buf, nc)
        ch <- result{msg: buf.String(), err: err}
    }()

    select {
    case r := <-ch:
        // ...
    case <-time.After(renderTimeout):
        return "", fmt.Errorf("template rendering timed out after %s", renderTimeout)
    }
}
```

When the timeout fires, the function returns but the spawned goroutine continues executing
`tmpl.Execute()`. Since `ch` is buffered (cap 1), the goroutine will eventually send and exit
in the normal case. However, if the template contains an expensive or infinite computation
(e.g., pathological Sprig function chains), the goroutine leaks indefinitely.

Go's `text/template.Execute` cannot be cancelled — there is no context parameter. The buffered
channel mitigates the common case (goroutine completes slightly after timeout), but the
pathological case is a genuine resource leak.

**Impact**: Slow memory/goroutine leak under malformed templates. Limited blast radius since
the 1s timeout means at most ~1 leaked goroutine per notification dispatch.

**Mitigation Note**: The buffered channel already prevents the goroutine from blocking on send.
The risk is primarily from templates that trigger very long-running Sprig function evaluation.
With C1 fixed (restricted function map), the attack surface is significantly reduced.

**Fix**: Add a context-aware select in the goroutine:

```go
func (e *TemplateEngine) executeWithTimeout(tmpl *template.Template, nc NotificationContext) (string, error) {
    ctx, cancel := context.WithTimeout(context.Background(), renderTimeout)
    defer cancel()

    ch := make(chan result, 1)
    go func() {
        var buf bytes.Buffer
        err := tmpl.Execute(&buf, nc)
        select {
        case ch <- result{msg: buf.String(), err: err}:
        case <-ctx.Done():
        }
    }()

    select {
    case r := <-ch:
        if r.err != nil {
            return "", fmt.Errorf("execute template: %w", r.err)
        }
        return strings.TrimSpace(r.msg), nil
    case <-ctx.Done():
        return "", fmt.Errorf("template rendering timed out after %s", renderTimeout)
    }
}
```

This does not cancel the `Execute` call itself (that's impossible), but ensures the goroutine
exits promptly once it completes, even if the caller already returned.

---

#### H2: Graceful Shutdown Dispatches Fail — Cancelled Context

**File**: `internal/notification/dispatcher.go`, lines 271–284

```go
func (d *Dispatcher) runWorkers(ctx context.Context) {
    var wg sync.WaitGroup
    for i := 0; i < d.workers; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for req := range d.ch {
                d.dispatch(ctx, req)  // ← ctx is the manager context
            }
        }()
    }
    wg.Wait()
}
```

During shutdown:

1. `ctx` is cancelled (`<-ctx.Done()` fires in `Start()`)
2. `close(d.done)` — drainer exits
3. `flushOverflow()` — remaining overflow items pushed into `ch`
4. `close(d.ch)` — workers drain remaining items

Workers continue to drain `ch` after close, calling `d.dispatch(ctx, req)` where `ctx` is
already cancelled. Inside `dispatch`:

```go
sendCtx, cancel := context.WithTimeout(ctx, d.dispatchTimeout)
```

`sendCtx` is derived from a cancelled parent → **immediately cancelled**. All `Send()` calls
during graceful shutdown will fail with `context.Canceled`.

**Impact**: Notifications queued but not yet dispatched at shutdown time are silently lost.

**Fix**: Use a detached context with a bounded shutdown timeout:

```go
func (d *Dispatcher) runWorkers(ctx context.Context) {
    var wg sync.WaitGroup
    for i := 0; i < d.workers; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for req := range d.ch {
                // Use a detached context for drain-phase dispatches.
                dispCtx := ctx
                select {
                case <-ctx.Done():
                    var cancel context.CancelFunc
                    dispCtx, cancel = context.WithTimeout(context.Background(), d.dispatchTimeout)
                    defer cancel()
                default:
                }
                d.dispatch(dispCtx, req)
            }
        }()
    }
    wg.Wait()
}
```

---

#### H3: Webhook Sink in CRD Enum but Not Implemented

**File**: `api/v1alpha1/hibernatenotification_types.go`, lines 12–22

```go
// +kubebuilder:validation:Enum=slack;telegram;webhook
type NotificationSinkType string

const (
    SinkSlack   NotificationSinkType = "slack"
    SinkTelegram NotificationSinkType = "telegram"
    SinkWebhook NotificationSinkType = "webhook"  // ← No implementation
)
```

The CRD allows `type: webhook` but no webhook sink is registered. A user can create a
HibernateNotification with `type: webhook`, which passes admission validation but fails
silently at dispatch time with `"sink type \"webhook\" not registered"` (logged, not surfaced
to the user).

**Impact**: User-facing confusion — the CRD accepts a value that cannot work.

**Fix**: Either implement the webhook sink or remove it from the enum:

- **Option A**: Remove `webhook` from the kubebuilder enum validation and the `SinkWebhook` constant until implementation is ready.
- **Option B**: Implement a minimal webhook sink (JSON POST to a URL from config).

Option A is preferred for now — shipping an enum value with no backing implementation
violates the principle of least surprise.

---

#### H4: `text/template` with Telegram HTML ParseMode

**File**: `internal/notification/sink/telegram/telegram.go`, lines 133–157

```go
tmpl := ptr.Deref(opts.CustomTemplate, DefaultTemplate)
content := s.renderer.Render(ctx, tmpl, payload)

params := &bot.SendMessageParams{
    ChatID: chatID,
    Text:   content,
}
if cfg.ParseMode != "" {
    params.ParseMode = models.ParseMode(cfg.ParseMode)
}
```

When `ParseMode = "HTML"`, Telegram interprets the message as HTML. But the template engine
uses `text/template`, which does **not** escape HTML entities. Template variables containing
`<`, `>`, or `&` characters will be interpreted as HTML tags.

Fields like `ErrorMessage` or plan labels could contain user-controlled content. An error
message like `scale failed: expected <5 replicas` would break the HTML structure. While
Telegram's client strips most dangerous elements, the rendering will be garbled.

**Impact**: Broken HTML rendering for messages containing special characters. The real-world
XSS risk is low since Telegram clients sanitize heavily, but the garbled output degrades
the user experience.

**Fix**: HTML-escape template output when ParseMode is HTML. The cleanest approach is to
let the Telegram sink apply `html.EscapeString` to the rendered content for non-tag fields,
or document that custom templates using HTML mode must handle escaping themselves.

---

### Medium (Consider)

#### M1: Duplicate Payload / TargetInfo Types

**Files**:
- `internal/notification/types.go` — `Payload`, `TargetInfo`
- `internal/notification/sink/sink.go` — `Payload`, `TargetInfo`, `PlanInfo`

Two near-identical `Payload` structs exist. The `sink.Payload` has two extra fields (`SinkName`,
`SinkType`). The dispatcher manually copies field-by-field between them (~20 lines). An identical
`TargetInfo` type is defined in both packages. `PlanInfo` exists in both `template.go` and
`sink/sink.go`.

This creates a maintenance hazard: adding a field to one `Payload` without updating the other
and the conversion logic introduces silent data loss.

**Fix**: Consolidate to a single canonical type. Either:
- Use `sink.Payload` as the canonical type everywhere (notification package imports sink).
- Or create a shared `notification/types` package.

---

#### M2: Stale Doc Comment — "Renderer from opts"

**File**: `internal/notification/sink/sink.go`, lines 108–112

```go
// Send delivers a notification payload to the external system.
// The payload carries the structured event data. Each sink decides how to
// format the payload — well-established sinks (Slack, Telegram) use the
// Renderer from opts to apply their built-in templates, while generic sinks
// (webhook) may forward the raw payload as JSON.
```

After the refactoring, `SendOptions` no longer contains a `Renderer`. Renderers are injected at
sink construction time. The comment references the old design.

Similarly, `internal/notification/dispatcher.go`, line 287:

```go
// dispatch processes a single DispatchRequest: resolves credentials, builds
// SendOptions with the Renderer and optional custom template, and delegates
```

**Fix**: Update both comments to reflect the current ownership model.

---

#### M3: Stale Mermaid Diagram in `notification-flow.md`

**File**: `docs/plan/notification-flow.md`, section 3

The "Dispatcher Internals" Mermaid diagram shows `Submit()` going directly to channel with
"Drop + record" on channel-full. The actual implementation uses the overflow queue — `Submit()`
never drops during normal operation. The diagram doesn't represent the overflow queue path.

**Fix**: Update the diagram to show the overflow queue path between Submit and the channel.

---

#### M4: No Validation of Sink Name Uniqueness

**File**: `api/v1alpha1/hibernatenotification_types.go`, lines 70–74

The CRD allows duplicate sink names within `spec.sinks`:

```yaml
sinks:
  - name: slack-alerts
    type: slack
    secretRef: { name: secret-a }
  - name: slack-alerts    # ← duplicate ok
    type: telegram
    secretRef: { name: secret-b }
```

Both sinks will be dispatched to. Not a functional bug (duplicate notifications are sent),
but confusing in metrics/logs and potentially indicates a user error.

**Fix**: Add sink name uniqueness validation in the admission webhook.

---

#### M5: No `time.After` Cleanup in `executeWithTimeout`

**File**: `internal/notification/template.go`, line 161

```go
case <-time.After(renderTimeout):
```

`time.After` creates a `time.Timer` that is not garbage-collected until it fires. When the
template renders successfully before the timeout, the timer remains live until `renderTimeout`
elapses. For a 1s timeout this is negligible, but it is a well-known Go anti-pattern.

**Fix**: Use explicit `time.NewTimer` with `Stop()`:

```go
timer := time.NewTimer(renderTimeout)
defer timer.Stop()

select {
case r := <-ch:
    // ...
case <-timer.C:
    // ...
}
```

This is addressed by the broader fix in H1 (context-based timeout).

---

### Low / Nits

#### L1: Template Re-Parsed on Every Render

**File**: `internal/notification/template.go`, `Render()` method

Each call to `Render()` parses the template string from scratch. For the built-in default
templates (which never change at runtime), this is redundant work. However, the parsing cost
is micro-seconds and templates are small, so performance impact is negligible.

**Recommendation**: No change needed. If performance profiling later shows template parsing as
a hotspot, add a sync.Map cache keyed by template string hash.

---

#### L2: Context Parameter Ignored in `notifyHook`

**File**: `internal/provider/processor/plan/state/state_notify.go`, line 36

```go
return func(_ context.Context, plan *hibernatorv1alpha1.HibernatePlan) error {
```

The context is discarded. Since `Submit()` is non-blocking and doesn't use context, this is
harmless. But if `Submit()` ever needs context (e.g., for tracing), this will need updating.

**Recommendation**: Pass context through for future-proofing, or add a comment explaining why
it's intentionally discarded.

---

#### L3: Successful Send Logged at V(1) Only

**File**: `internal/notification/dispatcher.go`, line 350

```go
log.V(1).Info("notification sent successfully")
```

Successful notifications log at debug level only. Operators running at default verbosity
(`--zap-log-level=info`) won't see confirmation that notifications were delivered. The metrics
(`notification_sent_total`) compensate for this, but log-based auditing requires
`--zap-log-level=debug`.

**Recommendation**: This is an acceptable design choice — logging every send at Info level
would be noisy. The metrics provide aggregate visibility. No change needed unless auditing
requirements change.

---

#### L4: Missing Test — Malformed Custom Template

**File**: `internal/notification/dispatcher_test.go`

There's no test verifying the end-to-end path when a ConfigMap contains a syntactically invalid
Go template. The expectation is that `Render()` falls back to plain text, but this path through
the dispatcher → sink → renderer chain is untested.

**Recommendation**: Add a test case with an invalid template to verify the fallback chain.

---

#### L5: `plainFallback` Formatting Uses Hardcoded Layout

**File**: `internal/notification/template.go`, `plainFallback()` method

The fallback format constructs a human-readable message but doesn't include all fields
(e.g., `CycleID`, `RetryCount`, `Targets` are omitted). This is acceptable for a fallback,
but worth noting that error context is reduced in the fallback path.

**Recommendation**: No change needed — fallback is intentionally minimal.

---

## Architecture Observations

### 1. Template Security Boundary

The template engine sits at a trust boundary: the template string comes from a ConfigMap that
any namespace-scoped user can create, but it executes in the operator pod's context. This is
the root cause of C1 (Sprig env exposure) and H4 (HTML injection). Consider treating custom
templates as **untrusted input** and applying the principle of least privilege:

- Restricted function map (no env/expandenv)
- Template string length limit (e.g., 64 KB)
- Output length limit (prevent memory bomb via `{{ repeat 1000000 "x" }}`)

### 2. Fire-and-Forget Guarantees

The current design provides **at-most-once delivery**. Notifications can be lost due to:

- Channel + overflow queue full (C2, once fixed with a cap)
- Sink delivery failure (no retry)
- Operator shutdown (H2)

This is acceptable for a notification (not alerting) system. However, the guarantees should be
documented explicitly so users understand the delivery model.

### 3. Webhook Sink — Design Direction

When implementing the webhook sink (H3), consider:

- JSON payload (not template-rendered) for machine-to-machine integration
- Configurable HTTP method (POST/PUT)
- Custom headers from config
- HMAC signing for payload integrity

The `sink.Payload` struct could be marshalled directly to JSON for the webhook case, making
the webhook sink the only one that doesn't need a `Renderer`.

---

## Summary Table

| # | Issue | Severity | File | Impact |
|---|-------|----------|------|--------|
| C1 | Sprig `env`/`expandenv` exposed | Critical | template.go | Credential disclosure |
| C2 | Unbounded overflow queue | Critical | dispatcher.go | OOM kill |
| H1 | Goroutine leak on template timeout | High | template.go | Resource leak |
| H2 | Shutdown dispatches use cancelled ctx | High | dispatcher.go | Lost notifications |
| H3 | Webhook sink in enum, not implemented | High | hibernatenotification_types.go | User confusion |
| H4 | `text/template` + Telegram HTML mode | High | telegram.go | Broken rendering |
| M1 | Duplicate Payload/TargetInfo types | Medium | types.go, sink.go | Maintenance hazard |
| M2 | Stale "Renderer from opts" comment | Medium | sink.go, dispatcher.go | Documentation debt |
| M3 | Stale Mermaid diagram in notification-flow.md | Medium | notification-flow.md | Documentation debt |
| M4 | No sink name uniqueness validation | Medium | hibernatenotification_types.go | User confusion |
| M5 | `time.After` not cleaned up | Medium | template.go | Minor resource leak |
| L1 | Template re-parsed on every render | Low | template.go | Negligible perf |
| L2 | Context ignored in `notifyHook` | Low | state_notify.go | Future-proofing |
| L3 | Successful send logged at V(1) only | Low | dispatcher.go | Audit visibility |
| L4 | Missing malformed template test | Low | dispatcher_test.go | Coverage gap |
| L5 | `plainFallback` omits some fields | Low | template.go | Reduced error context |

---

## Recommendations

**Immediate (before merge)**:
1. Fix C1 — restrict Sprig function map (security)
2. Fix C2 — cap the overflow queue (stability)
3. Fix H3 — remove `webhook` from CRD enum until implemented (API correctness)
4. Fix M2 — update stale comments (quick win during refactoring)

**Short-term (next iteration)**:
5. Fix H1 — context-aware goroutine cleanup in `executeWithTimeout`
6. Fix H2 — detached context for shutdown-phase dispatches
7. Fix H4 — HTML escaping for Telegram HTML mode
8. Fix M1 — consolidate duplicate Payload types
9. Fix M3 — update notification-flow.md diagrams

**Deferred**:
10. Implement webhook sink (H3 Option B)
11. Add sink name uniqueness validation (M4)
12. Add malformed template test (L4)
