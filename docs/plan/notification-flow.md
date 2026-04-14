# Notification System Flow

This document visualizes how the RFC-0006 Notification System integrates with the existing Hibernator Operator control plane.

---

## 1. Big Picture — Where Notifications Fit

```mermaid
flowchart TB
    subgraph K8S["Kubernetes API Server"]
        HP[HibernatePlan]
        HN[HibernateNotification]
        SE[ScheduleException]
        SEC[Secret\nSink Config]
        CM[ConfigMap\nCustom Template]
    end

    subgraph CTRL["Hibernator Operator (controller-runtime Manager)"]
        subgraph RECONCILER["PlanReconciler (Kubernetes Reconciler)"]
            R1[Fetch HibernatePlan]
            R2[Evaluate Schedule]
            R3[Load Exceptions]
            R4[Match Notifications\nby label selector]
            R5[Check RestoreData]
            R6[Build PlanContext]
        end

        subgraph PIPELINE["Async Phase Pipeline (RFC-0008)"]
            WM[watchable.Map\nPlanContext per plan]
            CO[Coordinator\nRunnable]
            WK[Worker\nper HibernatePlan]
            SP[State Processor\nIdle / Lifecyle / Exec / Recovery]
        end

        subgraph STATUSPROC["Status Processor"]
            UP[UpdateProcessor\nKeyed Worker Pool]
            PH[PreHook Closure\n→ Contains Submit call]
            MU[Mutator\nApply status changes]
            POST[PostHook Closure\n→ Contains Submit call]
        end

        subgraph NOTIF["Notification Dispatcher (Runnable)"]
            CH[Buffered Channel\ncap=256]
            WP[Worker Pool\n4 goroutines]
            TE[Template Engine\nRender message]
            SR[Secret Resolver\nGet sink config]
            SK[Sink\nSlack / Telegram / Webhook]
        end
    end

    HP -- Watch --> R1
    HN -- Watch --> R4
    SE -- Watch --> R3

    R1 --> R2 --> R3 --> R4 --> R5 --> R6
    R6 -- PlanContext --> WM

    WM -- Subscribe --> CO
    CO -- Spawn/remove --> WK
    WK -- Select state --> SP
    SP -- statusprocessor.Update\nw/ Hook closures --> UP

    UP --> PH
    PH -- dispatcher.Submit --> CH
    PH --> MU --> POST
    POST -- dispatcher.Submit --> CH

    CH --> WP
    WP --> SR
    WP --> TE
    SR -- Get --> SEC
    TE -- Get --> CM
    TE --> SK

    SK -- HTTP POST --> SLACK((Slack))
    SK -- HTTP POST --> TG((Telegram))
    SK -- HTTP POST --> WH((Custom\nWebhook))
```

---

## 2. Hook Execution — Status Update Lifecycle

Each state handler sends a `statusprocessor.Update` with optional `PreHook` and `PostHook` closures. The processor executes them in order around the API server write.

```mermaid
sequenceDiagram
    participant W as Worker (per plan)
    participant SP as State Processor
    participant UP as UpdateProcessor
    participant PH as PreHook Closure
    participant API as K8s API Server
    participant POST as PostHook Closure
    participant D as Dispatcher.Submit()
    participant CH as Buffered Channel

    W->>SP: Process(planCtx)
    SP->>UP: Send(Update{ PreHook, Mutator, PostHook })
    note over UP: Runs serial per plan key

    UP->>UP: Fetch fresh object from API
    UP->>PH: Execute PreHook(ctx, obj)
    PH->>D: Submit(DispatchRequest) — non-blocking
    D->>CH: Enqueue (or drop if full)
    D-->>PH: return immediately
    PH-->>UP: return nil

    UP->>UP: Apply Mutator (status change)
    UP->>API: status.Update(obj)
    API-->>UP: 200 OK

    UP->>POST: Execute PostHook(ctx, obj)
    POST->>D: Submit(DispatchRequest) — non-blocking
    D->>CH: Enqueue (or drop if full)
    D-->>POST: return immediately
    POST-->>UP: return nil
```

> **Key property**: `Submit()` is completely non-blocking. Hook closures return immediately regardless of channel state. Notifications never block or fail reconciliation.

---

## 3. Dispatcher Internals — From Channel to Sink

```mermaid
flowchart LR
    subgraph SUBMIT["Submit() — called from hook closure"]
        S1{Channel\nfull?}
        S2[Enqueue\nDispatchRequest]
        S3[Append to\nOverflow Queue]
        S4[Signal Drainer]
    end

    subgraph DRAINER["Drainer Goroutine"]
        DR1[Wait for signal]
        DR2[Transfer overflow\nbatch → channel]
    end

    subgraph POOL["Worker Goroutines × 4"]
        W1[Worker 1]
        W2[Worker 2]
        W3[Worker 3]
        W4[Worker 4]
    end

    subgraph DISPATCH["dispatch(ctx, req)"]
        D1[Lookup Sink\nin Registry]
        D2[Resolve Secret\nfrom informer cache]
        D3[Resolve Custom Template\nfrom ConfigMap — optional]
        D4[sink.Send\nwith 5s timeout]
        D5{Error?}
        D6[Record\nNotificationSentTotal\nNotificationLatency]
        D7[Record\nNotificationErrorsTotal\nNotificationLatency]
    end

    DispatchRequest --> S1
    S1 -- No --> S2
    S1 -- Yes --> S3 --> S4

    S4 -.-> DR1
    DR1 --> DR2
    DR2 --> Channel

    S2 --> Channel[(Channel\ncap 256)]
    Channel --> W1 & W2 & W3 & W4
    W1 & W2 & W3 & W4 --> D1

    D1 --> D2 --> D3 --> D4 --> D5
    D5 -- No error --> D6
    D5 -- Error --> D7
```

---

## 4. Template Rendering Pipeline

```mermaid
flowchart TD
    REQ[DispatchRequest]
    NC[NewNotificationContext\nncEvent, Phase, Plan, Targets...]
    REF{TemplateRef\nprovided?}

    subgraph CUSTOM["Custom Template Path"]
        CM1[Get ConfigMap\nfrom informer cache]
        CM2{Key\nexists?}
        CM3[Parse Go Template\nw/ Sprig functions]
        CM4[Execute w/ 1s timeout]
        CM5{Render\nsucceeded?}
    end

    subgraph DEFAULT["Default Template Path"]
        DT1{Built-in\nfor sink type?}
        DT2[Use sink template\nslack / telegram]
        DT3[Fall back to\nwebhook JSON]
        DT4[Execute w/ 1s timeout]
        DT5{Render\nsucceeded?}
    end

    PF[Plain-text Fallback\nEvent + Operation + Plan + Phase + Error]
    MSG[Rendered Message\n→ sink.Send]

    REQ --> NC --> REF

    REF -- Yes --> CM1
    REF -- No --> DT1

    CM1 --> CM2
    CM2 -- Yes --> CM3 --> CM4 --> CM5
    CM2 -- No --> DT1
    CM5 -- Yes --> MSG
    CM5 -- No / timeout --> DT1

    DT1 -- Known --> DT2 --> DT4
    DT1 -- Unknown --> DT3 --> DT4
    DT4 --> DT5
    DT5 -- Yes --> MSG
    DT5 -- No --> PF --> MSG
```

**Built-in default templates**:

| Sink Type | Format | Event Indicators |
|-----------|--------|-----------------|
| `slack` | Markdown | `:red_circle:` Failure · `:white_check_mark:` Success · `:arrow_forward:` Start · `:recycle:` Recovery |
| `telegram` | HTML (`<b>`, `<i>`) | 🔴 🟢 ▶️ ♻️ ℹ️ Unicode emoji |
| `webhook` | Raw JSON | Machine-readable flat envelope |

---

## 5. Event Trigger Points in the State Machine

```mermaid
stateDiagram-v2
    [*] --> Active : Plan created

    Active --> Hibernating : Off-hours schedule triggers
    note right of Active
        Phase 5 (pending):
        hook → Start event
    end note

    Hibernating --> Hibernated : All targets shut down
    note right of Hibernating
        Phase 5 (pending):
        hook → Success event
    end note

    Hibernating --> Error : Target failure
    note right of Hibernating
        Phase 5 (pending):
        hook → Failure event
    end note

    Error --> Hibernating : RetryCount < maxRetries
    note right of Error
        Phase 5 (pending):
        hook → Recovery event
    end note

    Error --> [*] : RetryCount >= maxRetries

    Hibernated --> WakingUp : On-hours schedule triggers
    note right of Hibernated
        Phase 5 (pending):
        hook → Start event
    end note

    WakingUp --> Active : All targets restored
    note right of WakingUp
        Phase 5 (pending):
        hook → Success event
    end note

    WakingUp --> Error : Restore failure

    Active --> Active : PhaseChange
    note right of Active
        Phase 5 (pending):
        hook → PhaseChange event
    end note
```

> **Note**: Hook wiring into state handlers (Phase 5) is the next implementation step. The dispatcher, template engine, and CRD are already live.

---

## 6. Notification Resource Matching

A `HibernateNotification` attaches to plans via label selectors, parallel to how exceptions work.

```mermaid
flowchart LR
    subgraph PLANS["HibernatePlan resources"]
        P1["plan-prod\nlabels: env=prod, tier=critical"]
        P2["plan-staging\nlabels: env=staging, tier=standard"]
        P3["plan-dev\nlabels: env=dev"]
    end

    subgraph NOTIFS["HibernateNotification resources"]
        N1["notify-critical\nselector: tier=critical\nevents: Failure, Recovery\nsink: PagerDuty-like webhook"]
        N2["notify-all-envs\nselector: env in [prod,staging]\nevents: Start, Success\nsink: Slack"]
        N3["notify-prod-slack\nselector: env=prod\nevents: ALL\nsink: Slack #prod-alerts"]
    end

    P1 -. matched .-> N1
    P1 -. matched .-> N2
    P1 -. matched .-> N3

    P2 -. matched .-> N2

    subgraph FETCH["PlanReconciler.fetchAllNotifications()"]
        direction TB
        F1[List ALL HibernateNotifications\nin same namespace]
        F2[For each: check selector\nagainst plan Labels]
        F3[Store matched list in\nPlanContext.Notifications]
        F1 --> F2 --> F3
    end
```

---

## 7. End-to-End Message Journey

```mermaid
sequenceDiagram
    participant KW as K8s Watch
    participant PR as PlanReconciler
    participant WM as watchable.Map
    participant WK as Worker (plan-prod)
    participant SH as StateHandler
    participant UP as UpdateProcessor
    participant D as Dispatcher
    participant TE as TemplateEngine
    participant SL as Slack Sink

    KW->>PR: HibernatePlan reconcile triggered
    PR->>PR: scheduleEval.IsOffHours() → true
    PR->>PR: fetchAllNotifications() → [notify-prod-slack]
    PR->>WM: Store PlanContext{Plan, Notifs, ...}

    WM->>WK: PlanContext updated
    WK->>SH: Handle(ctx, planCtx)
    SH->>UP: Send(Update{Mutator: setHibernating, PostHook: submitStart})

    UP->>UP: Fetch fresh plan
    UP->>UP: Apply Mutator → phase = Hibernating
    UP->>KW: status.Update(plan)
    UP->>SH: Execute PostHook
    SH->>D: Submit(DispatchRequest{Event:"Start", SinkType:"slack",...})
    D-->>SH: return immediately (non-blocking)

    Note over D: async, decoupled from reconciliation

    D->>D: resolveSecret("slack-secret")
    D->>TE: Render(NotificationContext, nil)
    TE->>TE: renderDefault(sinkType="slack")
    TE-->>D: ":arrow_forward: *Execution Starting*\n*Plan:* plan-prod..."
    D->>SL: Send(ctx, message, config)
    SL->>SL: HTTP POST to Slack webhook URL
```

---

## Component Registry (Quick Reference)

| Component | Location | Role |
|-----------|----------|------|
| `HibernateNotification` CRD | `api/v1alpha1/hibernatenotification_types.go` | User-facing intent: which events → which sinks |
| `PlanContext.Notifications` | `internal/message/types.go` | Carries matched notifications into worker pipeline |
| `fetchAllNotifications()` | `internal/provider/provider.go` | Matches notifications to plan at reconcile time |
| `statusprocessor.Update` | `internal/provider/processor/status/processor.go` | Carries Pre/PostHook closures around status writes |
| `notification.Dispatcher` | `internal/notification/dispatcher.go` | Async worker pool runnable |
| `notification.TemplateEngine` | `internal/notification/template.go` | Go template rendering with Sprig, built-in defaults |
| `notification.Registry` | `internal/notification/sink.go` | Sink implementation lookup by type |
| `sink/slack` | `internal/notification/sink/slack/slack.go` | Slack Incoming Webhook delivery |
| `sink/telegram` | `internal/notification/sink/telegram/telegram.go` | Telegram Bot API delivery |
| `setup.go` wiring | `internal/provider/setup.go` | Creates TemplateEngine + Dispatcher, registers as Runnable |

---

## Known Architectural Findings

> Full analysis in [notification-review.md](notification-review.md).

### Security: Template Trust Boundary

The template engine sits at a **trust boundary**: template strings come from ConfigMaps that
namespace-scoped users can create, but they execute in the operator pod's process. Sprig v3's
`TxtFuncMap()` includes `env` and `expandenv`, which can read all operator pod environment
variables. Custom templates should be treated as **untrusted input**.

**Required hardening**:
- Restricted Sprig function map (remove `env`, `expandenv`)
- Template string length limit
- Output length limit (prevent memory amplification via Sprig `repeat`)

### Delivery Guarantees

The system provides **at-most-once delivery**:

- Notifications can be lost during operator shutdown (workers use a cancelled parent context)
- Sink delivery failures are logged and metered but not retried at the application level
- At sustained overflow, items may be dropped (once overflow cap is added)

This is acceptable for a notification (not alerting) system but should be documented for users.

### Overflow Queue Backpressure

The overflow queue is currently unbounded. Under sustained pressure from a slow downstream
sink, it will grow without limit and potentially cause OOM. A configurable cap with
drop-on-full and `NotificationDropTotal` metrics is required before production use.

### Graceful Shutdown Context Propagation

During shutdown, the manager context is cancelled before workers finish draining the channel.
Workers calling `dispatch(ctx, req)` with the cancelled context create a child timeout context
that is immediately cancelled, causing all remaining dispatches to fail. A detached context
with a bounded shutdown timeout is needed for the drain phase.
