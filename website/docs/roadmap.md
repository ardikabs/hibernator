# Roadmap

Current status and future plans for the Hibernator Operator.

For detailed design documents, see the [proposals directory](https://github.com/ardikabs/hibernator/tree/main/docs/proposals).

## Quick Status

| Component | Status | Details |
|-----------|--------|---------|
| **Core Operator** | :white_check_mark: Shipped | v1.x complete |
| **Schedule Exceptions** | :white_check_mark: Implemented | Phases 1–5 complete |
| **Stateless Error Reporting** | :white_check_mark: Implemented | Runner writes to termination-log |
| **E2E Tests** | :white_check_mark: Implemented | Living document, grows over time |
| **Async Reconciler** | :white_check_mark: Implemented | Default; legacy reconciler removed |
| **Helm Chart** | :white_check_mark: Implemented | [Available](getting-started/installation.md#using-helm-recommended) |
| **CI/CD Pipeline** | :white_check_mark: Implemented | GitHub Actions |
| **kubectl hibernator CLI** | :white_check_mark: Implemented | [Available](user-guides/cli.md) |
| **Notification System** | :rocket: In Progress | [RFC-0006](https://github.com/ardikabs/hibernator/tree/main/docs/proposals/0006-notification-system.md) |
| **CLI One-Line Installer** | :hourglass: Pending | curl + bash installer |
| **GCP Executors** | :zzz: On-Demand | Implemented when use case arises |
| **Azure Executors** | :zzz: On-Demand | Implemented when use case arises |

## Completed (v1.x)

### Core Infrastructure

- [x] HibernatePlan CRD with full lifecycle management
- [x] Controller with phase-based state machine
- [x] All 4 execution strategies: Sequential, Parallel, DAG, Staged
- [x] DAG dependency resolver with Kahn's algorithm and cycle detection
- [x] Bounded concurrency control via `maxConcurrency`
- [x] Validation webhook for schedule format and DAG validation

### Executor Ecosystem

- [x] AWS Executors: EKS (node groups + Karpenter), RDS, EC2
- [x] Executor registration and pluggable interface
- [x] Per-executor parameter validation
- [x] Restore metadata capture and persistence

### Scheduling & Time Management

- [x] User-friendly schedule format: `start`/`end` (HH:MM) + `daysOfWeek`
- [x] Cron conversion with timezone support
- [x] Multi-window schedule support (OR-logic evaluation)
- [x] Timezone-aware schedule evaluation

### Schedule Exceptions (Phases 1–5)

- [x] Independent `ScheduleException` CRD
- [x] Three exception types: extend, suspend, replace
- [x] Lead-time configuration for suspensions
- [x] Automatic time-based expiration
- [x] Composable multi-exception semantics (mergeByType)

### Security & Authentication

- [x] Projected ServiceAccount tokens with custom audience
- [x] TokenReview validation for streaming requests
- [x] RBAC enforcement for controller and runner
- [x] IRSA integration for cloud provider credentials

### Observability

- [x] Structured logging with logr
- [x] [Prometheus metrics](reference/metrics.md) for execution, reconciliation, pipeline, and notifications
- [x] Per-target execution ledger in plan status
- [x] Streaming infrastructure: gRPC + HTTP webhook fallback

### Reliability & Operations

- [x] Stateless error reporting via Kubernetes Termination Messages
- [x] Async phase-driven reconciler (Coordinator/Worker actor model)
- [x] E2E test framework (lifecycle, execution strategies, schedule exceptions, error recovery)
- [x] Helm Chart packaging
- [x] CI/CD pipeline (GitHub Actions)
- [x] kubectl hibernator CLI plugin

## Planned

### Near-Term

- **Notification System** — Slack, email, and webhook notifications for hibernation events ([RFC-0006](https://github.com/ardikabs/hibernator/tree/main/docs/proposals/0006-notification-system.md))
- **CLI One-Line Installer** — `curl | bash` style installer for `kubectl-hibernator` via custom URL
- **Lifecycle Processors for Connectors** — Introduce active status monitoring and lifecycle management for `K8SCluster` and `CloudProvider` resources.

### Medium-Term

- **Exception Approval Workflows** — Slack/email-based approvals (Phase 6+)

### Long-Term

- **Multi-Cluster Management** — Cross-cluster hibernation coordination
- **Web Dashboard** — UI for monitoring and managing plans
- **Custom Executor SDK** — Framework for building out-of-tree executors

### On-Demand

The following are not scheduled but will be implemented when a concrete use case is demanded:

- **GCP Executors** — GKE node pool, Cloud SQL, and Compute Engine support
- **Azure Executors** — AKS, Azure SQL, and VM management
