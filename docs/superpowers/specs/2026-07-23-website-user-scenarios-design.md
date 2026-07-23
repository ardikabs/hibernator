# Design: Website "Scenarios" Section (User Scenario Ladder)

**Date:** 2026-07-23
**Status:** Approved
**Audience:** End users reading the Hibernator documentation website

---

## 1. Problem / Goal

The documentation website explains Hibernator's building blocks well (Concepts, per-executor guides, per-feature user guides, Quick Start), but it does not answer the question new users actually ask first: **"What real-world situations can this tool handle, from the simplest to the most advanced?"**

We will add a **Scenarios** section: a progressive ladder of complete, business-framed, copy-pasteable setups — from a nightly full shutdown of one environment to a production-grade multi-account composition.

## 2. Approved Decisions

| Decision | Choice |
|---|---|
| Destination | Documentation website (`website/docs/`), NOT `docs/user-journey/` |
| Structure | New top-level nav section "Scenarios": index page + one page per scenario, grouped by tier |
| Progression | Basic → Intermediate → Advanced → Expert |
| Executor scope | Production-ready executors only: `eks`, `rds`, `ec2`, `karpenter`, `workloadscaler`, `noop`. `gke`/`cloudsql` excluded (disabled, pending API integration) |
| Voice/format | Match existing website style (quickstart.md): YAML + bash verification + admonitions + Next Steps; mermaid where it clarifies |
| Concept overlap | Never re-explain concepts — link to Concepts/User Guides pages instead |

## 3. Deliverables

### 3.1 New directory `website/docs/scenarios/` — 11 pages

| File | Tier | Scenario | Capabilities demonstrated |
|---|---|---|---|
| `index.md` | — | Ladder overview + capability matrix | Navigation, scenario × feature matrix |
| `nightly-full-shutdown.md` | Basic | Nightly full shutdown & wakeup of a whole non-prod environment | Multi-target single plan (eks + rds + ec2), `Parallel` + `maxConcurrency`, full lifecycle |
| `weekend-hibernation.md` | Basic | Off Friday evening → Monday morning | `end < start` (next-day) semantics, weekday/weekend window behavior |
| `partial-hibernation-critical-services.md` | Intermediate | Partial hibernation: keep critical services at minimal footprint, stop everything else | Mixed treatment in one plan (`workloadscaler` min replicas + eks/ec2 full stop), per-target connectorRef kinds |
| `partial-failure-besteffort.md` | Intermediate | Large fleet where some targets may fail without blocking the rest | `behavior.mode: BestEffort` vs `Strict`, status ledger reading, retries |
| `dependency-ordered-shutdown.md` | Advanced | Databases stop last, wake first | `DAG` strategy, `dependencies`, wakeup reversal semantics |
| `staged-hibernation-criticality.md` | Advanced | Roll hibernation by criticality: dev → batch → shared | `Staged` strategy, per-stage `parallel`/`maxConcurrency` |
| `event-mode-overrides.md` | Advanced | Event window: keep DB up, retarget compute to event fleet | `ScheduleException` type `extend` + `targetOverrides` (disable + parameter replacement), PlanSnapshot behavior |
| `holiday-schedule-replacement.md` | Advanced | Holiday period with entirely different schedule and strategy | type `replace` + `executionOverride` |
| `dry-run-rehearsal.md` | Expert | Rehearse a production-shaped plan with zero cloud impact | `noop` executor, strategy/behavior validation, promote-to-prod checklist |
| `production-grade-setup.md` | Expert | Capstone: full production setup | Cross-account connectors, DAG, notifications (Slack), monitoring/verification, exceptions |

### 3.2 Nav edit in `website/mkdocs.yml`

Insert `Scenarios:` section between `User Guides` and `Reference`:

```yaml
  - Scenarios:
      - Overview: scenarios/index.md
      - Basic:
        - Nightly Full Shutdown: scenarios/nightly-full-shutdown.md
        - Weekend Hibernation: scenarios/weekend-hibernation.md
      - Intermediate:
        - Partial Hibernation (Keep Critical Services): scenarios/partial-hibernation-critical-services.md
        - Tolerating Partial Failures: scenarios/partial-failure-besteffort.md
      - Advanced:
        - Dependency-Ordered Shutdown (DAG): scenarios/dependency-ordered-shutdown.md
        - Staged by Criticality: scenarios/staged-hibernation-criticality.md
        - Event-Mode Overrides: scenarios/event-mode-overrides.md
        - Holiday Schedule Replacement: scenarios/holiday-schedule-replacement.md
      - Expert:
        - Dry-Run Rehearsal: scenarios/dry-run-rehearsal.md
        - Production-Grade Setup: scenarios/production-grade-setup.md
```

## 4. Page Anatomy (all scenario pages)

1. **Business context** — one-paragraph "you are a ... who needs ..." framing
2. **Goal** — what the end state looks like
3. **Resources involved** — table or small mermaid diagram
4. **Complete YAML** — connectors + plan (+ exceptions), copy-pasteable, internally consistent names/regions
5. **How it executes** — timeline walkthrough (what happens at window start/end, per target)
6. **Verification** — kubectl commands and expected states
7. **Variations / decision branches** — 1–3 common adjustments
8. **Next Steps / related guides** — links to Concepts, User Guides, Reference

## 5. Non-Goals

- No `gke`/`cloudsql` scenarios (executors disabled, pending API integration)
- No new CRDs, executors, or code changes — documentation only
- No new `config/samples/` files (YAML is embedded in pages per website convention)
- No re-explanation of concepts already covered in Concepts/User Guides
- No changes to `docs/user-journey/` (separate system, RFC-traceable byproducts)

## 6. Grounding & Accuracy Requirements

- Executor `parameters:` blocks must match `website/docs/reference/executor-parameters.md` and existing executor guides exactly
- Exception semantics (`extend`/`suspend`/`replace`, `targetOverrides`, `executionOverride`) must match `api/v1alpha1/scheduleexception_types.go` and `user-guides/schedule-exceptions.md`
- Strategy YAML must match `user-guides/execution-strategies.md`
- Notification YAML (capstone) must match `user-guides/notifications.md`
- Cross-account connector YAML (capstone) must match `concepts/connectors.md`
- Every internal link must resolve (verified via `make docs-build`)

## 7. Verification

- `make docs-build` succeeds with zero warnings/errors (nav, links, mermaid, admonitions all render)
- Optional: `make docs-serve` visual spot-check
- No git commits without explicit user request

## 8. Risks / Mitigations

| Risk | Mitigation |
|---|---|
| YAML drifts from real executor params | Ground every page in `reference/executor-parameters.md` + executor guides before writing |
| Duplicating concept docs | Rule: explain scenario, link for concepts |
| 11 pages of inconsistent voice | Single page anatomy (§4), quickstart.md as style reference |
| Capstone documents non-existent features | Verify cross-account + notification content against existing docs during implementation |
