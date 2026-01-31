# Hibernator Operator - GitHub Copilot Instructions

> **CRITICAL**: Always consult and follow the detailed guidance in `.github/instructions/*` files. These files contain essential principles, patterns, and mandates that govern all development work on this project.

## Quick Reference to Instruction Files

Before implementing any feature or making changes, review the relevant instruction files in `.github/instructions/`:

- **Core Principles**: `core-design-principles.md`, `architectural-pattern.md`
- **Code Quality**: `code-organization-principles.md`, `code-idioms-and-conventions.md`
- **Security**: `security-mandate.md`, `security-principles.md`
- **Observability**: `logging-and-observability-mandate.md`, `logging-and-observability-principles.md`
- **Error Handling**: `error-handling-principles.md`
- **Testing**: `testing-strategy.md`
- **API Design**: `api-design-principles.md`
- **Dependencies**: `avoid-circular-dependencies.md`, `dependency-management-principles.md`
- **Performance**: `performance-optimization-principles.md`, `resources-and-memory-management-principles.md`
- **Concurrency**: `concurrency-and-threading-mandate.md`, `concurrency-and-threading-principles.md`
- **Configuration**: `configuration-management-principles.md`
- **Command Execution**: `command-execution-principles.md`
- **Data**: `data-serialization-and-interchange-principles.md`
- **Documentation**: `documentation-principles.md`
- **Rugged Software**: `rugged-software-constitution.md`

## Project Overview

**Hibernator Operator** is a Kubernetes-native operator that manages time-based hibernation and wakeup of cloud infrastructure resources. It orchestrates coordinated shutdown and restoration of heterogeneous resources (EKS clusters, RDS databases, EC2 instances, Karpenter node pools) based on user-defined schedules.

## Terminology & Naming Conventions

**Critical**: Use correct naming for custom resources and core concepts:

- **`HibernatePlan`**: The primary CRD defining hibernation intent (schedule, targets, execution strategy). Referenced in: `api/v1alpha1/hibernateplan_types.go`, controller, samples. **Always use this exact spelling: Hibernate (not Hibernator)**.
- **`CloudProvider`**: CRD for cloud credentials and configuration
- **`K8SCluster`**: CRD for Kubernetes cluster access
- **Executor**: The component responsible for shutdown/wakeup of a specific resource type (eks, rds, ec2, karpenter, gke, cloudsql, etc.)
- **Runner**: The isolated Kubernetes Job that invokes an executor for a single target
- **Restore Manager**: Component managing restore state persistence in ConfigMaps
- **Restore Data**: JSON-encoded metadata captured during shutdown, used to restore resources during wakeup

Keep this terminology consistent across code comments, documentation, and log messages.

## Architecture Principles

### Core Responsibilities

The **Hibernator Operator Core** (Brain):
- Evaluates time-based schedules with timezone awareness
- Manages hibernation lifecycle state (Active → Hibernating → Hibernated → WakingUp)
- Enforces dependency ordering using DAG-based execution
- Dispatches work to pluggable executors
- Persists restore metadata for wakeup operations
- Surfaces execution status and errors

### Executor Pattern (Hands)

**Key Principle**: Core never knows "how" to shutdown something — it only knows "what intent" to apply.

Each executor:
- Implements a well-defined contract (`Shutdown`, `WakeUp`, `Validate`)
- Owns idempotency and retry logic
- Captures restore state during shutdown
- Can be in-tree (official) or out-of-tree (custom)

### Custom Resources (CRDs)

1. **HibernatePlan**: Intent for coordinated hibernation/wakeup
   - Schedule with timezone and off-hour windows (HH:MM format)
   - Execution strategy (Sequential, Parallel, DAG, Staged)
   - Behavior mode (Strict, BestEffort)
   - List of targets with executor-specific parameters

2. **CloudProvider**: AWS/GCP/Azure credentials and region config
   - IRSA-based authentication (preferred)
   - Static credentials (fallback)

3. **K8SCluster**: Kubernetes cluster access configuration
   - EKS/GKE cluster metadata
   - Kubeconfig reference

## Current Implementation Status

### ✅ Completed (MVP Phase 1-3)

- **Core Infrastructure**: CRDs, controller, scheduler/planner with DAG support
- **AWS Executors**: EKS (node groups + Karpenter), RDS, EC2
- **Streaming Infrastructure**: gRPC + webhook fallback for runner logs/progress
- **Authentication**: Projected ServiceAccount tokens with TokenReview validation
- **Restore System**: ConfigMap-based persistence with RestoreManager
- **Error Recovery**: Automatic retry with exponential backoff
- **Validation Webhook**: Schedule format, DAG cycle detection
- **Schedule Migration**: Start/End/DaysOfWeek format with cron conversion

### 🔄 In Progress

- **E2E Tests**: Full hibernation cycle integration tests
  - Test framework created (`test/e2e/`)
  - Hibernation, wakeup, schedule, recovery test suites
  - Needs API structure fixes and envtest setup

### ⏳ Pending (Priority Order)

1. **P1: E2E Test Completion** - Fix API mismatches and run full test suite
2. **P1: Helm Chart** - Deployment packaging for production use
3. **P3: GCP Executors** - Complete GKE and Cloud SQL API integration
4. **P3: Azure Executors** - AKS and Azure SQL implementations
5. **P3: Documentation** - User guide, operator manual, API reference
6. **P3: CI/CD Pipeline** - GitHub Actions for build, test, release

## Key Implementation Decisions

1. **Schedule Format**: User-friendly `start`/`end` time windows (HH:MM) with `daysOfWeek` array
   - Internally converted to cron expressions for scheduler compatibility
   - Timezone-aware evaluation

2. **Authentication Model**: Projected ServiceAccount tokens
   - Custom audience: `hibernator-control-plane`
   - TokenReview-based validation
   - Auto-rotated by kubelet

3. **Runner Execution**: Isolated Kubernetes Jobs
   - Ephemeral ServiceAccounts with IRSA
   - Executor invocation with streaming progress
   - ConfigMap for restore metadata

4. **Streaming Transport**: Pluggable gRPC + HTTP webhook
   - gRPC preferred for efficiency
   - Webhook fallback for restricted environments
   - Common interface for both transports

5. **Restore Persistence**: ConfigMap-based
   - Namespaced: `restore-data-{plan-name}`
   - Keys: `{executor}_{target-name}`
   - JSON-encoded restore state

6. **DAG Execution**: Kahn's algorithm for topological sort
   - Explicit dependencies in plan spec
   - Cycle detection at admission time
   - Bounded parallelism with `maxConcurrency`

## Development Guidelines

### Code Organization

```
.
├── api/v1alpha1/              # CRD types and webhook
├── cmd/
│   ├── controller/            # Operator main binary
│   └── runner/                # Executor runner binary
├── internal/
│   ├── controller/            # HibernatePlan reconciler
│   ├── executor/              # Executor implementations (eks, rds, ec2, karpenter)
│   ├── scheduler/             # Schedule evaluation and DAG planner
│   ├── restore/               # RestoreManager (ConfigMap ops)
│   ├── recovery/              # Error classification and retry logic
│   ├── streaming/             # gRPC/webhook server + client
│   └── metrics/               # Prometheus metrics
├── config/                    # Kubernetes manifests (CRDs, RBAC, samples)
├── test/e2e/                  # End-to-end tests
└── RFCs/                      # Design documents
```

### Testing Strategy

- **Unit Tests**: All packages with `_test.go` files
- **Controller Tests**: envtest-based integration tests
- **E2E Tests**: Full hibernation/wakeup cycle validation
- **Webhook Tests**: Validation and conversion logic
- **Coverage Requirement**: Maintain at least 50% unit test coverage for all packages

### Error Handling

**Error Recovery System**:
- Error classification: Transient vs Permanent
- Exponential backoff: `min(60s * 2^attempt, 30m)`
- Configurable max retries (0-10, default 3)
- Status tracking: `RetryCount`, `LastRetryTime`, `ErrorMessage`

**Best Practices**:
- Always wrap errors with context
- Use structured logging for debugging
- Record errors in status for observability

### Logging and Observability

- **Structured Logging**: Use logr with key-value pairs
- **Prometheus Metrics**: 8 metrics for execution, reconciliation, restore data
- **Status Tracking**: Per-target execution ledger in `status.executions[]`
- **Streaming Logs**: Runner progress via gRPC/webhook

### Security Considerations

- **Least Privilege**: Ephemeral ServiceAccounts per Job
- **Credential Isolation**: Per-execution Secret mounting
- **Token Validation**: TokenReview for streaming auth
- **RBAC**: Minimal permissions for controller and runner
### Git Workflow (CRITICAL)

**NEVER auto-commit to git.** All git commits must be explicitly requested by the user:

- ✅ **DO**: Use `git add` to stage changes
- ❌ **DON'T**: Run `git commit` automatically
- ⏳ **WAIT**: For explicit user instruction: "commit your changes" or "stage and commit"
- 📝 **FOLLOW**: All git operations require explicit user request

This rule applies to all work on this repository and ensures user retains full control over commit history and messages.


## Common Development Tasks

### Adding a New Executor

1. Create `internal/executor/{type}/{type}.go`
2. Implement `executor.Interface` (Validate, Shutdown, WakeUp)
3. Register in `cmd/runner/main.go`
4. Add tests in `internal/executor/{type}/{type}_test.go`
5. Update documentation

### Modifying CRD Schema

1. Edit `api/v1alpha1/*_types.go`
2. Run `make generate manifests`
3. Update webhook validation if needed
4. Add migration logic if breaking change
5. Update samples in `config/samples/`

### Adding Metrics

1. Define metric in `internal/metrics/metrics.go`
2. Instrument code with metric updates
3. Register metric in init function
4. Document in README or docs

### Running Tests

**IMPORTANT**: Only run tests on specific files/packages involved in the change, not the entire codebase. This improves efficiency and reduces noise from unrelated test failures.

```bash
# Target specific package tests (PREFERRED)
go test ./internal/executor/karpenter/... -v
go test ./pkg/executorparams/... -v

# Multiple related packages
go test ./internal/executor/eks/... ./internal/executor/karpenter/... -v

# Unit tests for all (only when necessary)
go test ./...

# Controller tests (requires envtest - skip unless changes affect controller)
go test ./internal/controller/...

# E2E tests (requires envtest binaries - skip unless testing full integration)
go test ./test/e2e/... -v

# With Ginkgo
ginkgo -v test/e2e/
```

### Building and Running

```bash
# Build binaries (outputs go to bin/ directory)
make build

# Build binaries manually (ensure output goes to bin/)
go build -o bin/controller ./cmd/controller
go build -o bin/runner ./cmd/runner

# Run controller locally
make run

# Build Docker images
make docker-build

# Install CRDs
make install

# Deploy to cluster
make deploy
```

**Build Output Convention**: All Go binaries built from `cmd/*` must be placed in the `bin/` directory. This keeps the repository root clean and provides a consistent location for all executable artifacts.

## API Conventions

### Schedule Format

```yaml
schedule:
  timezone: "America/New_York"
  offHours:
    - start: "20:00"      # HH:MM format (24-hour)
      end: "06:00"        # Next day if < start
      daysOfWeek: ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday"]
```

### Execution Strategies

```yaml
execution:
  strategy:
    type: DAG                    # Sequential | Parallel | DAG | Staged
    maxConcurrency: 3            # Optional parallelism bound
    dependencies:                # Only for DAG
      - from: database
        to: application
```

### Target Parameters

```yaml
targets:
  - name: my-target
    type: eks
    connectorRef:
      kind: CloudProvider
      name: aws-prod
    parameters:                  # Executor-specific config
      computePolicy:
        mode: Both
        order: [karpenter, managedNodeGroups]
```

## Troubleshooting Common Issues

### Schedule Not Triggering

- Check timezone configuration
- Verify cron conversion with logs
- Ensure controller is running
- Check RBAC permissions

### Executor Failures

- Review Job logs: `kubectl logs job/hibernate-runner-*`
- Check executor-specific parameters
- Verify connector credentials
- Review retry count in status

### Restore Data Missing

- Verify ConfigMap exists: `kubectl get cm restore-data-{plan}`
- Check RestoreManager logs
- Ensure ConfigMap not garbage-collected
- Review `status.executions[].restoreConfigMapRef`

### Authentication Errors

- Verify ServiceAccount exists
- Check IRSA annotations
- Review TokenReview validation logs
- Ensure projected token volume mounted

## Contributing Guidelines

1. **Follow Instruction Files**: Always consult `.github/instructions/*` for guidance
2. **Write Tests**: Unit tests for all new code, integration tests for features
3. **Document Changes**: Update README, CHANGELOG, and inline docs
4. **Atomic Commits**: One logical change per commit with clear messages
5. **Code Review**: All changes require review before merge

## RFC Registry & Keyword Index

**Use this index to match user requests to relevant RFCs via keyword detection:**

| RFC | Status | Keywords | Use When |
|-----|--------|----------|----------|
| [RFC-0001](../enhancements/0001-hibernate-operator.md) | In Progress 🚀 | Architecture, Control-Plane, Executors, Streaming, Security, Scheduling, Dependency-Resolution, Job-Lifecycle, RBAC, Restore-Metadata | User asks about operator architecture, execution model, streaming auth, security, or job lifecycle |
| [RFC-0002](../enhancements/0002-schedule-format-migration.md) | Implemented ✅ | Schedule-Format, Time-Windows, Cron-Conversion, API-Design, Timezone-Aware, Validation, User-Experience, Migration, OffHourWindow, Conversion | User asks about schedule validation, time windows, cron conversion, timezone handling, or API changes |
| [RFC-0003](../enhancements/0003-schedule-exceptions.md) | Proposed (Not Yet) | Schedule-Exceptions, Maintenance-Windows, Lead-Time, Time-Bound, Extend, Suspend, Replace, Emergency-Events, Validation, Status-Tracking | User asks about schedule exceptions, emergency overrides, maintenance windows, or time-bound deviations |
| [RFC-0004](../enhancements/0004-scale-subresource-executor.md) | Draft | Executors, Kubernetes, Scale-Subresource, Downscale, Restore-Metadata, RBAC | User asks about workload downscaling, scale subresource usage, workloadscaler executor, or RBAC for scaling |

**Keyword Matching Strategy:**
1. Extract keywords from user request
2. Match against RFC keyword lists above
3. Reference matching RFC(s) only when keywords align with user context
4. Use RFC as "least reference" (cite only when directly applicable)

## References

- **AGENTS.md**: [Agent checklist and handoff procedures](../AGENTS.md)
- **WORKPLAN.md**: [Detailed technical workplan](../enhancements/archived/WORKPLAN.md) (historical reference)

---

**Remember**: This project follows strict principles defined in `.github/instructions/*`. Always review relevant instruction files before implementing features, writing code, or making architectural decisions. **When user keywords match RFC keywords, reference the relevant RFC for context.**
