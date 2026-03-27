# Roadmap

Current status and future plans for the Hibernator Operator.

For detailed design documents, see the [proposals directory](https://github.com/ardikabs/hibernator/tree/main/docs/proposals).

## Quick Status

| Component | Status | Details |
|-----------|--------|---------|
| **Core Operator** | :white_check_mark: Shipped | v1.x complete |
| **Schedule Exceptions** | :white_check_mark: Implemented | Phases 1–5 complete |
| **Stateless Error Reporting** | :arrows_counterclockwise: In Progress | Kubernetes Termination Messages |
| **E2E Tests** | :arrows_counterclockwise: In Progress | Framework established |
| **kubectl hibernator CLI** | :arrows_counterclockwise: In Progress | Client-side complete |
| **Async Reconciler** | :arrows_counterclockwise: In Progress | Monolith refactor |
| **Helm Chart** | :hourglass: Pending | Deployment packaging |
| **GCP Executors** | :hourglass: Pending | GKE, Cloud SQL |
| **Azure Executors** | :hourglass: Pending | AKS, Azure SQL |

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
- [x] 8 core Prometheus metrics
- [x] Per-target execution ledger in plan status
- [x] Streaming infrastructure: gRPC + HTTP webhook fallback

## In Progress

### Stateless Error Reporting

Runner writes detailed error info to `/dev/termination-log`. Controller extracts termination messages for informed recovery and status updates.

### E2E Test Framework

Full hibernation cycle validation with envtest. Framework established with test suites for lifecycle, execution strategies, schedule exceptions, and error recovery.

### kubectl hibernator CLI Plugin

Client-side implementation complete with commands for `show schedule`, `show status`, `suspend`, `resume`, `retry`, and `logs`. Server-side verification pending.

### Async Phase-Driven Reconciler

Refactoring the monolithic controller into an async message-driven pipeline with Coordinator/Worker actor model. Feature-flagged via `--legacy-reconciler`.

## Planned

### Near-Term

- **Helm Chart** — Production deployment packaging with configurable values
- **CI/CD Pipeline** — GitHub Actions for build, test, and release automation

### Medium-Term

- **GCP Executors** — GKE node pool, Cloud SQL, and Compute Engine support
- **Azure Executors** — AKS, Azure SQL, and VM management
- **Exception Approval Workflows** — Slack/email-based approvals (Phase 6+)

### Long-Term

- **Multi-Cluster Management** — Cross-cluster hibernation coordination
- **Web Dashboard** — UI for monitoring and managing plans
- **Custom Executor SDK** — Framework for building out-of-tree executors
