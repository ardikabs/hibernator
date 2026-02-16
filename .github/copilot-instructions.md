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

### ✅ Completed (v1.x Shipped)

- **Core Infrastructure**: CRDs, controller, scheduler/planner with DAG support.
- **First-Cycle Failure Resolution**: Manual intervention protocol established with `hibernator.io/retry-now` annotation.
- **AWS Executors**: EKS (node groups + Karpenter), RDS, EC2.
- **Streaming Infrastructure**: gRPC + webhook fallback for runner logs/progress.
- **Authentication**: Projected ServiceAccount tokens with TokenReview validation.
- **Restore System**: ConfigMap-based persistence.
- **Error Recovery**: Automatic retry with exponential backoff.
- **Validation Webhook**: Schedule format, DAG cycle detection.
- **Schedule Migration**: Start/End/DaysOfWeek format with cron conversion.

### 🔄 In Progress

- **Stateless Error Reporting (Phase 4)**: Implementation of platform-native error propagation via Kubernetes Termination Messages.
  - Runner writes to `/dev/termination-log`.
  - Controller extracts detailed errors for informed recovery.
  - Manual recovery signal via `hibernator.ardikabs.com/retry-now` annotation.
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
│   ├── restore/               # RestoreManager (used by runner for ConfigMap ops)
│   ├── recovery/              # Error classification and retry logic
│   ├── streaming/             # gRPC/webhook server + client (logs/progress only)
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

### File Operations (CRITICAL)

**NEVER use heredoc syntax for file operations.** Always use `replace_string_in_file` or `multi_replace_string_in_file` tools:

- ✅ **DO**: Use `replace_string_in_file` with 3-5 lines of context before/after the target text
- ✅ **DO**: Use `multi_replace_string_in_file` for multiple independent edits (improves efficiency)
- ✅ **DO**: Read existing file content first to ensure accurate string matching
- ❌ **DON'T**: Use heredoc syntax (`<<EOF`) for any file creation or modification
- ❌ **DON'T**: Use shell redirection (`>`, `>>`) for file operations

**Why**: Heredocs cause whitespace/formatting issues, make diffs unclear, and can accidentally replace unintended content. Tool-based edits provide precise string matching and clear visibility of changes.

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

**⚠️ E2E TESTS REQUIRE EXPLICIT CONFIRMATION**: Never run E2E tests (`test/e2e/...`) automatically, regardless of what packages were changed (including `internal/controller/`). E2E tests:
- Require envtest binaries and special setup
- Have long timeouts (120s+) that can block development
- Should only be run when explicitly requested by the user
- Always ask: "Would you like me to run E2E tests?" before executing

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

# E2E tests (⚠️ REQUIRES EXPLICIT USER CONFIRMATION)
# NEVER run automatically - always ask user first!
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
- Check runner pod logs for save/load errors
- Ensure ConfigMap not garbage-collected
- Runner saves restore data during shutdown, loads during wakeup

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

## User Journey Documentation Standards

**When implementing a new RFC or major feature, corresponding user journey documentation must be created.**

### Purpose: The User-Technical Bridge

User journeys serve as a **critical bridge between user needs and technical implementation**. They translate high-level user intent into actionable technical specifications:

```
User Need / Business Goal
         ↓
    (User Journey)
         ↓
    RFC / Technical Design
         ↓
  Code Implementation
```

**Key Contract:**
- **User Journeys are NOT optional**: Every RFC that reaches "Implemented" status **must have** corresponding journey documentation
- **Journeys come WITH the RFC**: Journey documentation is created **when the RFC is decided to be implemented**, not after code is written
- **Journeys are the interpreter**: They explain user stories and business outcomes in plain language, which technical teams then translate into RFC/code
- **High-level to low-level translation**: Journeys describe "what users need to accomplish" (high-level); RFCs/code describe "how the system implements it" (low-level)

**Why this matters:**
- **Clarity**: Non-technical stakeholders understand user workflows
- **Traceability**: RFC ↔ Journey ↔ Code creates an auditable chain
- **Scope validation**: Journeys prevent feature creep by clearly defining user outcomes
- **Documentation**: When code changes, journeys explain why to new developers

### User Journey Structure

All user journeys are located in `docs/user-journey/` and follow this structure:

```markdown
# [Journey Title]

**Tier:** `[MVP | Enhanced | Advanced]`

**Personas:** [Persona1], [Persona2], [Persona3]

**When:** [Context for when this journey applies]

**Why:** [Business value delivered by this journey]

---

## User Stories

**Story 1:** As a **[Persona]**, I want to **[action]**, so that **[benefit]**.

**Story 2:** As a **[Persona]**, I want to **[action]**, so that **[benefit]**.

---

## When/Context

[Shared context that applies to all stories]

## Business Outcome

[What the user achieves or what problem is solved]

## Step-by-Step Flow

[Numbered steps to accomplish the journey]

## Decision Branches

[Alternative paths or conditional steps]

## Related Journeys

[Links to related user journeys]

## Pain Points Solved

[What problems this journey solves]

## RFC References

- [RFC-XXXX](../enhancements/XXXX-name.md) — Brief description
```

### When to Create User Journeys

User journeys are created **at RFC approval time**, not after implementation:

1. **RFC Status: Proposed** → No journey required yet (design phase)
2. **RFC Status: Approved for Implementation** → Create journeys immediately (alongside RFC approval)
3. **RFC Status: In Progress** → Journeys guide the implementation (user perspective is north star)
4. **RFC Status: Implemented** → Journeys document the final user-facing feature

**Critical Rule**: Do NOT wait until code is written to create journeys. Journeys should be created WHEN the RFC decision is made to implement, ensuring the technical team has user context and clear business outcomes to guide their work.

### RFC to User Journey Mapping

| RFC | Status | Required Journeys | Documentation |
|-----|--------|-------------------|---|
| [RFC-0001](../enhancements/0001-hibernate-operator.md) | In Progress 🚀 | `hibernation-plan-initial-design.md`, `deploy-operator-to-cluster.md`, `monitor-hibernation-execution.md` | Document control-plane architecture, Job execution, and status tracking |
| [RFC-0002](../enhancements/0002-schedule-format-migration.md) | Implemented ✅ | `hibernation-plan-initial-design.md` | Document schedule format with start/end/daysOfWeek syntax |
| [RFC-0003](../enhancements/0003-schedule-exceptions.md) | Implemented ✅ (Core) | `create-emergency-exception.md`, `extend-hibernation-for-event.md`, `suspend-hibernation-during-incident.md` | Document time-bound exception types (extend, suspend, replace). Phases 1-3 complete; approval workflows (Phase 4+) are future enhancements. |
| [RFC-0004](../enhancements/0004-scale-subresource-executor.md) | Implemented ✅ | `scale-workloads-in-cluster.md` | Document workload scale subresource executor |
| [RFC-0005](../enhancements/0005-serviceaccount-semantic-enhancements.md) | Proposed 📋 | TBD (future work) | Advanced ServiceAccount configuration patterns (not yet implemented) |

### User Journey as High-Level Interpretation

**User journeys translate technical RFC decisions into user-facing business language:**

- **RFC perspective (Low-level)**: "Implement gRPC streaming authentication with projected tokens and TokenReview validation"
- **Journey perspective (High-level)**: "As an SRE, I want to see real-time execution logs and progress, so that I can respond quickly to issues"

**The translation chain:**
```
User Goal (Real-world problem)
    ↓
Journey Story (Plain language outcome)
    ↓
RFC (Technical design & architecture)
    ↓
Code Implementation (Actual features)
```

Journeys ensure that **technical teams understand WHY** they're building something, not just WHAT they're building. When a developer reads the journey, they understand the user context that motivated the RFC design.

### User Journey Creation Guidelines

When creating a new user journey:

1. **Choose appropriate tier:**
   - **MVP**: Core functionality (operator basics, core executors, essential features)
   - **Enhanced**: Advanced operational features (exceptions, RBAC, multi-environment)
   - **Advanced**: Enterprise-scale features (cross-account, multi-tenant, audit)

2. **Identify personas:**
   - Use personas from the existing Personas Reference table in `docs/user-journey/README.md`
   - Choose personas directly involved in this journey
   - Add new personas only if they represent a distinct role not in existing set

3. **Write user stories:**
   - Format: "As a **[Persona]**, I want to **[action]**, so that **[benefit]**."
   - Use consistent formatting with bold markers around persona and action
   - One user story per use case; combine related flows
   - If multiple stories exist, use "## User Stories" (plural) section with Story 1, Story 2, etc.

4. **Include step-by-step flow:**
   - Numbered steps showing how to accomplish the journey
   - Include relevant kubectl commands or API calls
   - Show decision branches (if/then scenarios)
   - Link to relevant CRD definitions

5. **Link RFC(s):**
   - Every journey referencing an RFC should include RFC link in "## RFC References" section
   - Format: `- [RFC-XXXX](../enhancements/XXXX-name.md) — Brief description of RFC relevance`

6. **Register in hub README:**
   - Add journey name and link to `docs/user-journey/README.md`
   - Place in correct tier section (MVP, Enhanced, Advanced)
   - Include status badge (✅ Implemented, 🚀 In Progress, 📋 Planned, ⏳ Proposed)

### User Journey Status Badges

- **✅ Implemented**: Feature is complete and tested; journey reflects current state
- **🚀 In Progress**: Feature is actively being developed; journey documents target state
- **📋 Planned**: Feature is planned but not yet started; journey documents intended design
- **⏳ Proposed**: Feature concept exists but not yet approved; journey is speculative
- **🔧 Under Maintenance**: Feature exists but documentation needs updates for recent changes
- **❌ Obsolete**: Feature is deprecated or removed; journey is for historical reference only

## Findings Documentation Standards

**When an operational issue, architectural flaw, or significant technical debt is identified, a "Findings" document must be created.**

### Purpose: The Root Cause & Resolution Trail

Findings documents provide an auditable trail of technical investigations and their outcomes. They are essential for tracking complex bugs or design decisions that require investigation before implementation.

### Findings Structure & Template

All findings are located in `docs/findings/` and **MUST** follow the template at `docs/findings/TEMPLATE.md`.

**Required Statuses**:
- **`investigated`**: Problem explored, options proposed, but no decision or fix yet.
- **`resolved`**: Solution chosen and implemented.
- **`acked`**: Risk accepted with a known workaround; no immediate fix planned.
- **`deferred`**: Investigation or fix postponed to a later date.

**Document Requirements**:
- **Frontmatter**: Must include `date` (format: February 8, 2026), `status`, and `component`.
- **Problem Description**: Detailed impact and conditions under which the issue occurs.
- **Root Cause Analysis**: Technical depth with code/logic traces and snippets.
- **Resolution/Proposed Solutions**: Clear path forward or documentation of the fix.
- **Appendix**: Move historical or rejected options here upon resolution to preserve context.

## RFC Registry & Keyword Index

**Use this index to match user requests to relevant RFCs via keyword detection:**

| RFC | Status | Keywords | Use When |
|-----|--------|----------|----------|
| [RFC-0001](../enhancements/0001-hibernate-operator.md) | Implemented ✅ | Architecture, Control-Plane, Executors, Streaming, Security, Scheduling, Dependency-Resolution, Job-Lifecycle, RBAC, Restore-Metadata | User asks about operator architecture, execution model, streaming auth, security, or job lifecycle |
| [RFC-0002](../enhancements/0002-schedule-format-migration.md) | Implemented ✅ | Schedule-Format, Time-Windows, Cron-Conversion, API-Design, Timezone-Aware, Validation, User-Experience, Migration, OffHourWindow, Conversion | User asks about schedule validation, time windows, cron conversion, timezone handling, or API changes |
| [RFC-0003](../enhancements/0003-schedule-exceptions.md) | Implemented ✅ (Core Implementation) | Schedule-Exceptions, Maintenance-Windows, Lead-Time, Time-Bound, Extend, Suspend, Replace, Emergency-Events, Validation, Status-Tracking, Independent-CRD, GitOps | User asks about schedule exceptions, emergency overrides, maintenance windows, time-bound deviations, or temporary schedule changes |
| [RFC-0004](../enhancements/0004-scale-subresource-executor.md) | Implemented ✅ | Executors, Kubernetes, Scale-Subresource, Downscale, Restore-Metadata, RBAC, WorkloadScaler | User asks about workload downscaling, scale subresource usage, workloadscaler executor, or RBAC for scaling |
| [RFC-0005](../enhancements/0005-serviceaccount-semantic-enhancements.md) | Proposed 📋 | ServiceAccount, IRSA, Workload-Identity, Multi-Cloud, Validation, Credential-Management, Federated-Identity, Audit | User asks about advanced ServiceAccount configurations, multi-cloud identity federation, or explicit pod identity management |

**Keyword Matching Strategy:**
1. Extract keywords from user request
2. Match against RFC keyword lists above
3. Reference matching RFC(s) only when keywords align with user context
4. Use RFC as "least reference" (cite only when directly applicable)

## References

- **RFCs Directory**: [All RFCs](../enhancements/) — Complete list of design documents
- **User Journey Hub**: [User Journeys](../docs/user-journey/README.md) — Index of all user journey documents

---

**Remember**: This project follows strict principles defined in `.github/instructions/*`. Always review relevant instruction files before implementing features, writing code, or making architectural decisions. **When user keywords match RFC keywords, reference the relevant RFC for context.**
