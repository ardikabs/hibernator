# Hibernator Roadmap

This document tracks the current status and future plans for the Hibernator Operator. For detailed design and status information, see the [proposals directory](docs/proposals/).

## 🎯 Quick Status

| Component | Status | Details |
|-----------|--------|---------|
| **Core Operator (RFC-0001)** | ✅ Shipped | v1.x complete |
| **Schedule Exceptions (RFC-0003)** | ✅ Implemented | Phases 1-5 complete; approval workflows future |
| **Stateless Error Reporting** | 🔄 In Progress | Kubernetes Termination Messages |
| **E2E Tests** | 🔄 In Progress | Framework established; envtest setup needed |
| **kubectl hibernator CLI (RFC-0007)** | 🔄 In Progress | Client-side complete; server verification pending |
| **Async Reconciler (RFC-0008)** | 🔄 In Progress | Monolith refactor; feature-flagged |
| **Helm Chart** | ⏳ Pending P1 | Deployment packaging |
| **GCP Executors** | ⏳ Pending P3 | GKE, Cloud SQL, Compute Engine |
| **Azure Executors** | ⏳ Pending P3 | AKS, Azure SQL, VMs |
| **Exception Approvals** | 📋 Planned | Phase 6+; Slack/email/SSO integration |

## ✅ Completed (v1.x Shipped)

### Core Infrastructure
- [x] HibernationPlan CRD with full lifecycle management
- [x] Controller with phase-based state machine (Active → Hibernating → Hibernated → WakingUp)
- [x] All 4 execution strategies: Sequential, Parallel, DAG, Staged
- [x] DAG dependency resolver with Kahn's algorithm and cycle detection
- [x] Bounded concurrency control via `maxConcurrency`
- [x] Validation webhook for schedule format and DAG validation

### Executor Ecosystem
- [x] **AWS Executors**: EKS (node groups + Karpenter), RDS, EC2
- [x] Executor registration and pluggable interface
- [x] Per-executor parameter validation and handling
- [x] Restore metadata capture and persistence during shutdown

### Scheduling & Time Management
- [x] User-friendly schedule format: `start`/`end` (HH:MM) + `daysOfWeek`
- [x] Cron conversion with timezone support
- [x] Timezone-aware schedule evaluation
- [x] Off-hours window computation
- [x] **Multi-window schedule support** (RFC-0002 Phase 1)
  - [x] All windows in `offHours[]` array evaluated with OR-logic
  - [x] Each window independently converted to cron expressions
  - [x] Hibernation triggers when **any** window condition is met
  - [x] Next event times computed as earliest across all windows
  - [x] Full test coverage: 3+ multi-window test cases + E2E validation
  - [x] Handles extend windows containing base wakeup times (collision fix)
  - [x] Old MVP limitation (first-window-only) permanently fixed (commit d39782a)

### Schedule Exceptions (RFC-0003 Phases 1-5)
- [x] Independent `ScheduleException` CRD (separate from HibernationPlan)
- [x] Three exception types: `extend` (add windows), `suspend` (carve-out), `replace` (override)
- [x] Lead-time configuration for suspensions (prevents mid-process interruption)
- [x] Automatic time-based expiration (`validFrom`/`validUntil`)
- [x] Temporal overlap prevention (prevents multiple Active exceptions per plan)
- [x] Composable multi-exception semantics (Phase 5: allowed pairs like extend+suspend, mergeByType)
- [x] State tracking: Pending → Active → Expired
- [x] Exception history in plan status ledger

### Execution & Reliability
- [x] Runner Job model with isolated execution context
- [x] Kubernetes Job lifecycle management and monitoring
- [x] Exponential backoff retry with configurable max attempts (0-10, default 3)
- [x] Error classification: Transient vs Permanent
- [x] First-cycle failure resolution protocol (manual annotation-based retry)
- [x] Streaming infrastructure: gRPC + HTTP webhook fallback
- [x] Runner streaming integration for logs and progress reporting
- [x] ConfigMap-based restore metadata persistence
- [x] Status ledger with per-target execution tracking
- [x] Restoration data recovery during wakeup

### Security & Authentication
- [x] Projected ServiceAccount tokens with custom audience (`hibernator-control-plane`)
- [x] TokenReview validation for streaming requests
- [x] Kubelet-managed token rotation and lifecycle
- [x] RBAC enforcement for controller and runner permissions
- [x] IRSA integration for cloud provider credentials
- [x] Ephemeral ServiceAccount per execution (no long-lived secrets)

### Observability & Metrics
- [x] Structured logging with logr key-value pairs
- [x] 8 core Prometheus metrics for execution, reconciliation, and restore operations
- [x] Status tracking in CR with execution timestamps and error messages
- [x] Per-target execution ledger in plan status
- [x] Pod logs aggregation and artifact tracking

## 🔄 In Progress (P1-P2 - Active Development)

### Stateless Error Reporting (RFC-0001 Phase 4)
**Status**: Implementation in progress
**Goal**: Replace annotation-based manual retry with Kubernetes-native platform error reporting

- [x] Runner writes detailed error info to `/dev/termination-log`
- [ ] Controller extracts and parses termination messages
- [ ] Error classification bridge from termination messages
- [ ] Status update with structured error context
- [ ] Manual recovery signal via `hibernator.ardikabs.com/retry-now` annotation (continues to work)

**Files**: `cmd/runner/`, `internal/controller/`

### E2E Test Framework
**Status**: In progress — framework established, needs fixes
**Goal**: Full hibernation cycle validation with envtest

- [x] Test framework structure created in `test/e2e/`
- [x] Hibernation cycle test suite layout
- [x] Wakeup cycle test suite layout
- [x] Schedule evaluation test suite
- [x] Recovery/retry test suite
- [ ] Fix API type mismatches with CRD definitions
- [ ] Complete envtest setup and cluster initialization
- [ ] Run full E2E test suite locally and in CI

**Files**: `test/e2e/`, `Makefile`

### kubectl hibernator CLI Plugin (RFC-0007)
**Status**: In progress — client-side complete, server verification pending
**Goal**: Streamline day-to-day operational commands

**✅ Complete**:
- [x] Client-side CLI implementation in `cmd/kubectl-hibernator/`
- [x] Core commands: `show schedule`, `show status`, `suspend`, `resume`, `retry`, `logs`
- [x] Schedule validation with dry-run output
- [x] RBAC role templates in `config/rbac/cli_role.yaml`
- [x] JSON/YAML output formatting
- [x] Error handling and user guidance

**⏳ Pending**:
- [ ] Server-side verification with live cluster
- [ ] Plugin installation guide and distribution
- [ ] E2E test coverage for plugin commands
- [ ] Integration with kubectl plugin discovery

**Files**: `cmd/kubectl-hibernator/`

### Async Phase-Driven Reconciler (RFC-0008)
**Status**: In progress — architecture + phase processors being implemented
**Goal**: Refactor monolithic controller into async message-driven pipeline

**Phase Status**:
- [x] Phase 1: Foundation — Message bus & utilities (Watchable infrastructure)
- [x] Phase 2: Provider Layer — Reads HibernationPlan, evaluates schedule
- [x] Phase 3: Phase Processors — Business logic for schedule evaluation, target readiness
- [ ] Phase 4: Status Writer — Async status updates to CR
- [ ] Phase 5: Wiring — Composition root and integration
- [ ] Phase 6: Supporting infrastructure — Metrics, logging, error handling

**Feature Flag**: `--legacy-reconciler` (default: false; true = use old code path)
**Strategy**: Parallel implementation with feature flag; no breaking changes during migration

**Files**: `internal/reconciler/`, `internal/message/`, feature flags in controller

## 📋 Planned (P3-P4 - Next Up)

### Helm Chart & Production Deployment
**Priority**: P1
**Status**: Blocked on E2E test fixes

- [ ] Complete operator Helm chart in `charts/hibernator/`
- [ ] CRD installation via `helmMinVersion` requirement
- [ ] RBAC, ServiceAccount, webhook configuration
- [ ] Configurable control plane flags (max retries, backoff strategy, etc.)
- [ ] Release automation and distribution

**Files**: `charts/hibernator/`

### GCP Executor Suite
**Priority**: P3
**Status**: Framework in place; TODOs for API integration

**Planned Executors**:
- [ ] **GKE Executor** — Node pool suspension/restoration
  - Skeleton: `internal/executor/gke/gke.go` (TODO: Google Cloud API calls)
- [ ] **Cloud SQL Executor** — Instance stop/start
  - Skeleton: `internal/executor/cloudsql/cloudsql.go` (TODO: Google Cloud API calls)
- [ ] **Compute Engine Executor** — VM instance control
  - Status: Not started

**Files**: `internal/executor/gke/`, `internal/executor/cloudsql/`, `internal/executor/gce/`

### Azure Executor Suite
**Priority**: P3
**Status**: Not started

**Planned Executors**:
- [ ] **AKS Executor** — Node pool suspension/restoration
- [ ] **Azure SQL Executor** — Database pause/resume
- [ ] **Azure VMs Executor** — VM deallocate/start

**Files**: `internal/executor/aks/`, `internal/executor/azuresql/`, `internal/executor/azurevm/`

### Schedule Exception Approval Workflows (RFC-0003 Phase 6+)
**Priority**: P3
**Status**: Design phase

**Goals**: Add approval/audit workflow to exception lifecycle

**Planned Phases**:
- **Phase 6**: Slack DM-based approvals with email fallback
  - [ ] Slack integration for exception notifications
  - [ ] Approve/reject via Slack reaction or command
  - [ ] Audit trail in CRD status

- **Phase 7**: CLI-based approval (RFC-0007 integration)
  - [ ] `hibernator approve-exception` command
  - [ ] Approval delegation and RBAC

- **Phase 8**: Enterprise SSO/URL-based workflow
  - [ ] Standard OAuth/OIDC integration
  - [ ] Dashboard UI for approval queue
  - [ ] Approval history and compliance reporting

**Files**: `internal/approval/` (future)

### Documentation
**Priority**: P3
**Status**: Partially complete

- [ ] **Operator Manual** — Deployment, configuration, troubleshooting
- [ ] **API Reference** — Full CRD documentation with examples
- [ ] **User Guide** — Common patterns and recipes
- [ ] **Architecture Deep Dive** — Design rationale and internals
- [ ] **Contributing Guide** — Development environment setup

**Partial**: User journeys in [docs/user-journey/](docs/user-journey/) exist for core features

### CI/CD & Automation
**Priority**: P3
**Status**: Basic structure; enhancements needed

- [ ] GitHub Actions for automated builds
- [ ] Automated testing on PR (unit + E2E)
- [ ] Container image building and registry push
- [ ] Release automation with semantic versioning
- [ ] SBOM generation and supply chain security
