# Scenarios

Real-world hibernation setups, ordered from the simplest to the most advanced. Each scenario is a self-contained page with complete, copy-pasteable YAML — connectors, plans, exceptions, and notifications — framed around a concrete business situation.

**Assumptions:** you have the Hibernator operator [installed](../getting-started/installation.md) and understand the basics from the [Quick Start](../getting-started/quickstart.md).

## The Scenario Ladder

| Tier | Scenario | What it demonstrates | Link |
|------|----------|----------------------|------|
| Basic | **Nightly Full Shutdown & Wakeup** | One plan stops an entire environment every night and restores it every morning | [Details](nightly-full-shutdown.md) |
| Basic | **Weekend Hibernation** | Keep a dev environment off from Friday evening to Monday morning | [Details](weekend-hibernation.md) |
| Intermediate | **Partial Hibernation** | Keep critical services running while everything else hibernates | [Details](partial-hibernation-critical-services.md) |
| Intermediate | **Tolerating Partial Failures** | Let a large fleet hibernate even when some targets fail | [Details](partial-failure-besteffort.md) |
| Intermediate | **Suspend Scheduled Shutdown** | Temporarily suppress a nightly window for testing or incidents | [Details](suspend-scheduled-shutdown.md) |
| Intermediate | **Selective RDS Hibernation** | Hibernate only non-critical dev/staging databases with tagSelector | [Details](rds-selective-hibernation.md) |
| Advanced | **Dependency-Ordered Shutdown (DAG)** | Databases stop last and wake first via explicit dependencies | [Details](dependency-ordered-shutdown.md) |
| Advanced | **Staged Hibernation by Criticality** | Roll hibernation through tiers: dev → batch → shared | [Details](staged-hibernation-criticality.md) |
| Advanced | **Event-Mode Overrides** | Change which targets hibernate (and how) during a special event | [Details](event-mode-overrides.md) |
| Advanced | **Weekend Subset Wakeup** | Wake only 2 of 10 databases during a suspend window, auto-revert after | [Details](weekend-subset-wakeup.md) |
| Advanced | **Holiday Schedule Replacement** | Replace the entire schedule and execution strategy for a period | [Details](holiday-schedule-replacement.md) |
| Expert | **Dry-Run Rehearsal** | Validate a production-shaped plan with zero cloud impact | [Details](dry-run-rehearsal.md) |
| Expert | **Production-Grade Setup** | Cross-account access, ordered shutdown, Slack alerts, governance | [Details](production-grade-setup.md) |

## Capability Matrix

Use this matrix to find the scenario that exercises a specific feature combination.

| Scenario | Strategy | Behavior | Exception | Executors | Key feature |
|----------|----------|----------|-----------|-----------|-------------|
| Nightly Full Shutdown | Parallel | Strict | — | eks, rds, ec2 | Full lifecycle |
| Weekend Hibernation | Parallel | Strict | — | eks, rds | Schedule windows |
| Partial Hibernation | Parallel | Strict | — | workloadscaler, karpenter, eks | Selector granularity |
| Tolerating Partial Failures | Parallel | BestEffort | — | workloadscaler, ec2 | Failure tolerance |
| Suspend Scheduled Shutdown | Parallel | Strict | suspend | eks | Carve-out suppression |
| Selective RDS Hibernation | Parallel | Strict | — | rds | Expression-based selection |
| Dependency-Ordered Shutdown | DAG | Strict | — | workloadscaler, eks, ec2, rds | Ordering |
| Staged by Criticality | Staged | Strict | — | workloadscaler, karpenter, eks, rds | Stage groups |
| Event-Mode Overrides | Parallel (base) | Strict | extend + targetOverrides | ec2, eks, rds | Per-target overrides |
| Weekend Subset Wakeup | Parallel (base) | Strict | suspend + targetOverrides | rds | Partial wakeup subset |
| Holiday Replacement | Sequential (override) | BestEffort (override) | replace + executionOverride | eks, rds | Schedule + strategy replacement |
| Dry-Run Rehearsal | DAG | BestEffort | — | noop | Failure simulation |
| Production-Grade Setup | DAG | Strict | replace (referenced) | workloadscaler, karpenter, eks, rds | Cross-account + notifications |

## How to Use These Scenarios

- **Copy and adapt.** Every YAML block is complete and internally consistent. Account IDs, regions, cluster names, tags, and labels are illustrative — replace them with your own.
- **Selectors are the contract.** Most scenarios select resources by tags or labels (`Environment`, `workload`, `critical`, ...). Adjust the selector shapes to your tagging/labeling conventions, keeping the structure intact.
- **Start simple, climb the ladder.** Each tier builds on concepts from the previous one. If you are new to Hibernator, start with the Basic tier even if your end goal is an Expert scenario.
- **Concepts are linked, not repeated.** Scenario pages explain the *situation* and the *setup*; they link to Concepts and User Guides for deep dives instead of duplicating them.

!!! note "GCP executors"
    The `gke` and `cloudsql` executors are still pending API integration and are not covered by any scenario. See the [roadmap](../roadmap.md) for their status.
