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

## Configuration Reference

```yaml
# Schedule format
schedule:
  timezone: "America/New_York"
  offHours:
    - start: "20:00"      # HH:MM 24-hour
      end: "06:00"        # Next day if < start
      daysOfWeek: ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday"]

# Execution strategy
execution:
  strategy:
    type: DAG             # Sequential | Parallel | DAG | Staged
    maxConcurrency: 3
    dependencies:         # Only for DAG
      - from: database
        to: application

# Targets
targets:
  - name: my-target
    type: eks
    connectorRef:
      kind: CloudProvider
      name: aws-prod
    parameters:
      computePolicy:
        mode: Both
        order: [karpenter, managedNodeGroups]
```

## Key Implementation Details

| Aspect | Details |
|--------|---------|
| **Restore Persistence** | ConfigMap `restore-data-{plan}`. Key: `{executor}_{target}`. JSON-encoded. |
| **Runner Isolation** | Isolated K8s Jobs. Ephemeral ServiceAccounts + IRSA. Per-execution Secret mounting. |
| **DAG Execution** | Kahn's algorithm topological sort. Cycle detection at admission. |
| **Streaming** | gRPC preferred, webhook fallback. TokenReview auth. |
| **Error Recovery** | Exponential backoff: `min(60s * 2^attempt, 30m)`. Max retries 0-10, default 3. |

## Testing

```bash
# Target specific packages (PREFERRED)
go test ./internal/executor/eks/... -v
go test ./pkg/executorparams/... -v

# Controller tests (requires envtest)
go test ./internal/controller/...

# E2E tests (NEVER run automatically - always ask!)
go test ./test/e2e/... -v
```

## Common Tasks

**Add new executor**:
1. `internal/executor/{type}/{type}.go`
2. Implement `Validate`, `Shutdown`, `WakeUp`
3. Register in `cmd/runner/main.go`
4. Add tests
5. Document

**Modify CRD**:
1. Edit `api/v1alpha1/*_types.go`
2. Run `make generate manifests`
3. Update webhook validation
4. Update samples in `config/samples/`

## Documentation References

| Category | Location |
|----------|----------|
| RFC Proposals | `docs/proposals/` |
| User Journeys | `docs/user-journey/` |
| Findings | `docs/findings/` |
| Code of Conduct | `CONTRIBUTING.md` |

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

## User Journey Standards

User journeys are created **at RFC approval time** (not after implementation).

- **Location**: `docs/user-journey/`
- **Tiers**: MVP (core), Enhanced (operational), Advanced (enterprise)
- **Status badges**: Implemented, In Progress, Planned, Proposed, Maintenance, Obsolete

## Findings Standards

Findings track root cause investigations.

- **Location**: `docs/findings/`
- **Template**: `docs/findings/TEMPLATE.md`
- **Status**: investigated, resolved, acked, deferred
- **Required frontmatter**: `date`, `status`, `component`

---

**See `bd prime` for persistent project memories injected at session start.**