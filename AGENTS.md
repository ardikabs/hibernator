# Hibernator Operator - AI Agent Instructions

This project uses **bd (beads)** for issue tracking and persistent project memories.

## Quick Start

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work atomically
bd close <id>         # Complete work
bd prime              # Get workflow context + persistent memories
```

## Critical Rules

- **File ops**: NEVER use heredoc/redirect. Always use `edit`/`write` tools with 3-5 lines context.
- **Git**: NEVER auto-commit. All commits require explicit user request.
- **E2E tests**: NEVER run automatically (`test/e2e/...`). Always ask first.
- **Build**: All Go binaries go to `bin/`. `make build` or `go build -o bin/{name}`.

## Project Overview

**Hibernator Operator** is a Kubernetes-native operator that manages time-based hibernation and wakeup of cloud infrastructure resources. It orchestrates coordinated shutdown and restoration of heterogeneous resources (EKS, RDS, EC2, Karpenter) based on user-defined schedules.

## Terminology (Critical)

- **`HibernatePlan`**: Primary CRD (NOT "Hibernator"). Defines schedule, targets, execution strategy.
- **`CloudProvider`**: CRD for cloud credentials (IRSA preferred, static fallback)
- **`K8SCluster`**: CRD for Kubernetes cluster access configuration
- **Executor**: Component implementing Shutdown/WakeUp/Validate for a resource type
- **Runner**: Isolated K8s Job invoking an executor for a single target
- **RestoreManager**: Manages restore state persistence in ConfigMaps
- **RestoreData**: JSON-encoded metadata captured during shutdown for wakeup

## Architecture

**Core (Brain)**: Evaluates schedules, manages lifecycle, dispatches to executors.
**Executors (Hands)**: Own implementation. Core never knows "how" to shutdown—only "what intent" to apply.

Lifecycle: `Active → Hibernating → Hibernated → WakingUp`

## RFC Registry

| RFC | Status | Keywords |
|-----|--------|----------|
| [RFC-0001](docs/proposals/0001-hibernate-operator.md) | Implemented | Architecture, Executors, Streaming, Job-Lifecycle |
| [RFC-0002](docs/proposals/0002-schedule-format-migration.md) | Implemented | Schedule-Format, Cron-Conversion, Timezone-Aware |
| [RFC-0003](docs/proposals/0003-schedule-exceptions.md) | Implemented | Schedule-Exceptions, Extend, Suspend, Replace |
| [RFC-0004](docs/proposals/0004-scale-subresource-executor.md) | Implemented | Scale-Subresource, Downscale, WorkloadScaler |
| [RFC-0005](docs/proposals/0005-serviceaccount-semantic-enhancements.md) | Proposed | ServiceAccount, IRSA, Multi-Cloud |
| [RFC-0006](docs/proposals/0006-notification-system.md) | Implemented | Notifications, Slack, Webhook |
| [RFC-0007](docs/proposals/0007-kubectl-hibernator-cli-plugin.md) | Implemented | kubectl plugin, CLI |
| [RFC-0008](docs/proposals/0008-async-phase-driven-reconciler.md) | Implemented | AsyncReconciler, WatchablePipeline, Coordinator |
| [RFC-0009](docs/proposals/0009-slack-block-kit-notification-format.md) | Proposed | Slack, Block-Kit, Formatting |

---

**See `bd prime` for persistent project memories injected at session start.**

---

## Workflow Pattern

1. **Triage**: Run `bd ready` to find available work
2. **Claim**: Use `bd update <id> --claim` and set assignee based on git user:
   ```bash
   bd update <id> --claim --assignee="$(git config user.name)"
   ```
3. **Work**: Implement the task
4. **Complete**: Use `bd close <id> --reason="..."` with a detailed explanation of what was done, why, and any relevant context. The reason becomes the permanent record of the work.
5. **Capture Findings**: During work, if you discover interesting findings, bugs, or potential follow-ups, convert them into beads issues with `discovered-from` dependency on the current work:
   ```bash
   bd create --title="..." --description="..." --type=task
   bd dep add <new-id> <current-work-id> --type discovered-from
   ```
   This ensures discussion context is preserved and nothing falls through the cracks.
