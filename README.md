# Hibernator Operator

> Declarative Kubernetes operator for suspending and restoring cloud infrastructure during off-hours

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go)](go.mod)
[![Kubernetes](https://img.shields.io/badge/Kubernetes-1.34+-326CE5?logo=kubernetes)](https://kubernetes.io)

## Overview

Hibernator is a Kubernetes operator that provides centralized, declarative management for suspending and restoring cloud resources during user-defined off-hours. It extends beyond Kubernetes to manage heterogeneous cloud infrastructure (EKS, RDS, EC2, and more) with dependency-aware orchestration and auditable execution.

**Key capabilities:**
- 🕐 **Timezone-aware scheduling** with start/end times and day-of-week patterns
- ⏸️ **Schedule exceptions** with lead-time grace periods (extend, suspend, replace)
- ✅ **Multi-channel approval workflows** (Slack DM, kubectl, SSO/URL, Dashboard)
- 🔗 **Dependency orchestration** using DAG, Staged, Parallel, or Sequential strategies
- 🔌 **Pluggable executor model** for EKS, RDS, EC2, Karpenter, GKE, Cloud SQL
- 🔒 **Isolated runner jobs** with scoped RBAC, IRSA, and projected ServiceAccount tokens
- 📊 **Real-time progress streaming** via gRPC (preferred) or HTTP webhooks (fallback)
- 💾 **Durable restore metadata** persisted in ConfigMaps for safe recovery

## Why Hibernator?

**Problem:** Teams running non-production environments (DEV/STG) waste resources during off-hours. Ad-hoc scripts lack coordination, auditability, and safe restore semantics when dealing with dependencies across Kubernetes clusters, databases, and compute instances.

**Solution:** Hibernator provides intent-driven infrastructure suspension with:
- Declarative `HibernatePlan` CRDs defining *what* to suspend, not *how*
- Controller-managed dependency resolution preventing race conditions (e.g., snapshot before cluster shutdown)
- Central status ledger with execution history, logs, and restore artifact references
- GitOps-friendly configuration with validation webhooks

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                   Control Plane                          │
│  ┌────────────────────────────────────────────────┐     │
│  │  HibernatePlan Controller                       │     │
│  │  - Schedule evaluation                          │     │
│  │  - Dependency resolution (DAG/Staged/Parallel)  │     │
│  │  - Job lifecycle management                     │     │
│  │  - Status ledger updates                        │     │
│  └────────────────────────────────────────────────┘     │
│                         │                                │
│  ┌────────────────────────────────────────────────┐     │
│  │  Streaming Server (gRPC + Webhook)             │     │
│  │  - TokenReview authentication                   │     │
│  │  - Log aggregation                              │     │
│  │  - Progress tracking                            │     │
│  └────────────────────────────────────────────────┘     │
└─────────────────────────────────────────────────────────┘
                          │
        ┌─────────────────┼─────────────────┐
        ▼                 ▼                 ▼
┌───────────────┐  ┌───────────────┐  ┌───────────────┐
│ Runner Job    │  │ Runner Job    │  │ Runner Job    │
│ (EKS)         │  │ (RDS)         │  │ (EC2)         │
│ - Executor    │  │ - Executor    │  │ - Executor    │
│ - gRPC client │  │ - gRPC client │  │ - gRPC client │
│ - IRSA        │  │ - IRSA        │  │ - IRSA        │
└───────────────┘  └───────────────┘  └───────────────┘
```

The operator separates concerns:
- **Control Plane**: Schedules executions, manages Jobs, aggregates status, serves streaming API
- **Runner Jobs**: Isolated Kubernetes Jobs per target, each with dedicated ServiceAccount and executor
- **Executors**: Pluggable implementations (EKS, RDS, EC2) handling resource-specific shutdown/wakeup logic

## Features

### Execution Strategies

| Strategy | Description | Use Case |
|----------|-------------|----------|
| **Sequential** | Execute targets one by one | Simple ordered operations |
| **Parallel** | Execute all targets concurrently with `maxConcurrency` | Independent resources |
| **DAG** | Explicit dependencies via directed acyclic graph | Database before cluster |
| **Staged** | Grouped parallel execution with stage ordering | Logical phases (storage → compute) |

### Supported Executors

| Executor | Connector | Status | Operations |
|----------|-----------|--------|----------|
| **EKS** | CloudProvider | ✅ Stable | Managed Node Groups scale-to-zero via AWS API |
| **Karpenter** | K8SCluster | ✅ Stable | NodePool scaling and disruption budget management via Kubernetes API |
| **RDS** | CloudProvider | ✅ Stable | Instance/cluster stop with optional snapshot |
| **EC2** | CloudProvider | ✅ Stable | Tag-based or ID-based instance stop |
| **GKE** | K8SCluster | 🏗️ Planned | Node pool scaling (GCP API integration) |
| **Cloud SQL** | CloudProvider | 🏗️ Planned | Instance stop/start (GCP API integration) |
| **AKS** | K8SCluster | 📋 Roadmap | Node pool management (Azure API integration) |
| **Azure SQL** | CloudProvider | 📋 Roadmap | Server pause/resume (Azure API integration) |

### Security & Compliance

- **RBAC-scoped runners**: Each Job uses ephemeral ServiceAccount with minimal permissions
- **IRSA/Workload Identity**: Cloud credentials via Kubernetes ServiceAccount projection
- **TokenReview authentication**: Streaming auth using projected SA tokens with custom audience (`hibernator-control-plane`)
- **Audit trail**: Kubernetes API audit logs + object-store access logs + execution ledger in CR status

### Schedule Exceptions & Approval Workflows

Handle temporary deviations from base schedule:

**Exception Types:**
- **extend**: Add hibernation windows (e.g., weekend event support)
- **suspend**: Prevent hibernation with lead-time buffer (e.g., maintenance window)
- **replace**: Fully override base schedule (e.g., holiday mode)

**Approval Options:**
- **Slack DM**: Direct messages to approvers with [APPROVE] buttons (on-call specifies emails)
- **kubectl plugin**: CLI-based approval for engineering teams
- **SSO/URL**: Enterprise approval links with organization authentication
- **Dashboard UI**: Web-based approval interface with real-time tracking

See [`enhancements/0003-schedule-exceptions.md`](enhancements/0003-schedule-exceptions.md) for complete details.

## Quick Start

### Prerequisites

- Kubernetes 1.34+ cluster
- Go 1.24+ (for development)
- AWS credentials with appropriate IAM permissions for target resources

### Installation

```bash
# Apply CRDs
kubectl apply -f config/crd/bases/

# Deploy the operator
kubectl apply -f config/manager/manager.yaml

# Apply RBAC
kubectl apply -f config/rbac/
```

### Create Your First HibernatePlan

**Basic example with schedule exceptions:**

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: dev-offhours
  namespace: hibernator-system
spec:
  schedule:
    timezone: "Asia/Jakarta"
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday"]

    # NEW: Temporary exceptions for special events
    exceptions:
      - name: "on-site-event"
        type: "extend"  # Add hibernation during event
        validFrom: "2026-02-10T00:00:00Z"
        validUntil: "2026-02-15T23:59:59Z"
        approvalRequired: true
        approverEmails:  # On-call specifies approvers
          - "engineering-head@company.com"
          - "manager@company.com"
        windows:
          - start: "06:00"
            end: "11:00"
            daysOfWeek: ["Saturday", "Sunday"]

  execution:
    strategy:
      type: DAG
      maxConcurrency: 3
      dependencies:
        - from: dev-karpenter
          to: dev-eks-nodegroups  # Karpenter first, then managed node groups
        - from: dev-db
          to: dev-eks-nodegroups  # Shutdown cluster after DB

  targets:
    - name: dev-db
      type: rds
      connectorRef:
        name: aws-dev
      parameters:
        snapshotBeforeStop: true

    # EKS Managed Node Groups (via AWS API)
    - name: dev-eks-nodegroups
      type: eks
      connectorRef:
        kind: CloudProvider
        name: aws-dev
      parameters:
        clusterName: dev-cluster
        nodeGroups: []  # empty means all node groups

    # Karpenter NodePools (via Kubernetes API)
    - name: dev-karpenter
      type: karpenter
      connectorRef:
        kind: K8SCluster
        name: dev-cluster
      parameters:
        nodePools: []  # empty means all NodePools
```

**What happens:**
1. On-call engineer specifies exception with `approverEmails`
2. Controller sends Slack DM to approvers with [APPROVE] button
3. Approvers click button → exception becomes active
4. During event period, services stay awake (exception takes precedence)
5. After event expires, normal hibernation resumes

### Monitor Execution

```bash
# Watch plan status
kubectl get hibernateplan dev-offhours -n hibernator-system -w

# Check execution details
kubectl get hibernateplan dev-offhours -n hibernator-system -o jsonpath='{.status.executions[*]}' | jq

# View runner job logs
kubectl logs -n hibernator-system -l hibernator/plan=dev-offhours
```

## Configuration

### CloudProvider Connector (AWS)

```yaml
apiVersion: connector.hibernator.ardikabs.com/v1alpha1
kind: CloudProvider
metadata:
  name: aws-dev
  namespace: hibernator-system
spec:
  type: aws
  aws:
    accountId: "123456789012"
    region: ap-southeast-3
    auth:
      serviceAccount:
        assumeRoleArn: arn:aws:iam::123456789012:role/hibernator-runner
```

### K8SCluster Connector

```yaml
apiVersion: connector.hibernator.ardikabs.com/v1alpha1
kind: K8SCluster
metadata:
  name: dev-eks
  namespace: hibernator-system
spec:
  providerRef:
    name: aws-dev
  type: eks
  clusterName: dev-cluster
  eks:
    region: ap-southeast-3
```

## Status & Roadmap

### ✅ Completed (P0-P2 MVP)

- [x] Core controller with phase state machine
- [x] All 4 execution strategies (Sequential, Parallel, DAG, Staged)
- [x] EKS, RDS, EC2, Karpenter executors
- [x] Cron schedule parsing with timezone support (start/end/daysOfWeek format)
- [x] Validation webhook with DAG cycle detection
- [x] ConfigMap-based restore data persistence
- [x] gRPC streaming server + HTTP webhook fallback
- [x] Runner streaming integration with progress reporting
- [x] TokenReview authentication with projected SA tokens
- [x] Error recovery with exponential backoff retry logic
- [x] Prometheus metrics for observability
- [x] E2E test suite (hibernation, wakeup, schedule, recovery cycles)
- [x] Production-ready Helm charts with RBAC, webhook, monitoring

### 🚧 In Progress (P3 - RFC-0003 Implementation)

- [ ] **Schedule Exceptions & Approval Workflow** (RFC-0003)
  - [ ] Three exception types: extend, suspend (with lead time), replace
  - [ ] Four approval options: Slack DM, kubectl plugin, SSO/URL, Dashboard UI
  - [ ] On-call engineer workflow with email-based approver notification
  - [ ] Multi-stage approval state machine (Pending → Approved → Active → Expired)
  - [ ] Full audit trail for compliance

### 📋 Planned (P3-P4)

- [ ] GCP executors (GKE, Cloud SQL, Compute Engine)
- [ ] Azure executors (AKS, Azure SQL, VMs)
- [ ] Advanced scheduling (holidays, blackout windows, timezone exceptions)
- [ ] Multi-cluster federation
- [ ] Slack DM approval integration (Phase 2)
- [ ] SSO/URL-based approval workflow (Phase 3)
- [ ] Dashboard UI for exception management (Phase 4)
- [ ] Object-store artifact persistence (S3/GCS)
- [ ] kubectl hibernator plugin for CLI management

### 📚 Reference Documentation

See the following for detailed information:
- **Copilot Instructions**: [`.github/copilot-instructions.md`](.github/copilot-instructions.md) — Project architecture, status, development guidelines
- **Core Principles**: [`.github/instructions/`](.github/instructions/) — Design principles, security, testing, concurrency, API design
- **Architecture RFC**: [`enhancements/0001-hibernate-operator.md`](enhancements/0001-hibernate-operator.md) — Control Plane + Runner Model design
- **Schedule Exceptions RFC**: [`enhancements/0003-schedule-exceptions.md`](enhancements/0003-schedule-exceptions.md) — Approval workflow with multi-channel support
- **Detailed Workplan**: [`enhancements/archived/WORKPLAN.md`](enhancements/archived/WORKPLAN.md) — Historical design decisions and milestones
- **Agent Guide**: [`AGENTS.md`](AGENTS.md) — Repository conventions and development procedures

## Development

### Installation Options

**Option 1: Using Helm (Recommended for production)**

```bash
# Add Hibernator chart repository
helm repo add hibernator https://your-registry/charts
helm repo update

# Install with default values
helm install hibernator hibernator/hibernator -n hibernator-system --create-namespace

# Customize installation
helm install hibernator hibernator/hibernator \
  -n hibernator-system \
  -f values.yaml
```

**Option 2: Using kubectl (For development)**

```bash
# Apply CRDs
kubectl apply -f config/crd/bases/

# Deploy the operator
kubectl apply -f config/manager/manager.yaml

# Apply RBAC
kubectl apply -f config/rbac/
```

### Build & Test

```bash
# Build controller
make build

# Build runner
make build-runner

# Run unit tests
make test

# Run E2E tests (full hibernation cycle)
make e2e

# Run linter
make lint
```

### Local Development

```bash
# Install CRDs
make install

# Run controller locally
make run

# Run tests with coverage
make test-coverage
```

### Project Structure

```
├── .github/
│   ├── copilot-instructions.md        # Project guidance & status
│   └── instructions/                  # Development principles & mandates
├── api/                               # API definitions
│   ├── v1alpha1/                     # CRD types and webhook
│   └── streaming/                    # Streaming API proto/types
├── cmd/
│   ├── controller/                   # Controller main
│   └── runner/                       # Runner main
├── config/                           # Kubernetes manifests
│   ├── crd/bases/                   # CRD definitions
│   ├── manager/                     # Deployment manifests
│   ├── rbac/                        # RBAC rules
│   ├── samples/                     # Example CRs
│   └── webhook/                     # Webhook configuration
├── charts/hibernator/                # Helm chart (production-ready)
│   ├── Chart.yaml
│   ├── values.yaml
│   ├── templates/                   # Deployment, RBAC, webhook, service
│   └── README.md
├── enhancements/                     # Design RFCs
│   ├── 0001-hibernate-operator.md   # Architecture & Control Plane Model
│   ├── 0002-schedule-format-migration.md  # Schedule format evolution
│   ├── 0003-schedule-exceptions.md  # Exceptions & approval workflow
│   └── archived/                    # Historical workplans
├── internal/
│   ├── controller/                 # Reconciliation logic
│   ├── executor/                   # Executor implementations
│   │   ├── eks/                   # EKS Managed Node Groups (AWS API)
│   │   ├── karpenter/             # Karpenter NodePools (Kubernetes API)
│   │   ├── rds/                   # RDS instances/clusters
│   │   ├── ec2/                   # EC2 instances
│   │   ├── gke/                   # GKE node pools (placeholder)
│   │   └── cloudsql/              # Cloud SQL (placeholder)
│   ├── scheduler/                 # Schedule evaluation & DAG planner
│   ├── restore/                   # Restore data manager (ConfigMap)
│   ├── recovery/                  # Error recovery & retry logic
│   ├── metrics/                   # Prometheus metrics
│   └── streaming/                 # gRPC/webhook server & client
├── test/e2e/                        # End-to-end tests
│   ├── hibernation_test.go        # Hibernation cycle
│   ├── wakeup_test.go             # Wake-up cycle
│   ├── schedule_test.go           # Schedule evaluation
│   ├── recovery_test.go           # Error recovery
│   └── README.md                  # Test documentation
├── AGENTS.md                        # Agent onboarding & repository conventions
├── CHANGELOG.md                     # Release notes
└── README.md                        # This file
```

## Contributing

Contributions welcome! Please:

1. **Start with documentation**: Read [`.github/copilot-instructions.md`](.github/copilot-instructions.md) for project overview
2. **Follow principles**: Check [`.github/instructions/`](.github/instructions/) for design and coding guidelines
3. **Review conventions**: See [`AGENTS.md`](AGENTS.md) for repository conventions and development procedures
4. **Check priorities**: See [`.github/copilot-instructions.md`](.github/copilot-instructions.md#current-implementation-status) for current work
5. **Open discussion**: Discuss major changes in issues before implementation
6. **Write tests**: Add unit tests for all new code and integration tests for features
7. **Update docs**: Keep this README and RFCs updated with your changes

## License

Apache License 2.0 - see [LICENSE](LICENSE) for details.

## Quick Links

- **Copilot Instructions**: [`.github/copilot-instructions.md`](.github/copilot-instructions.md) — Project guidance & implementation status
- **Development Principles**: [`.github/instructions/`](.github/instructions/) — Security, testing, concurrency, API design
- **Architecture RFC**: [`enhancements/0001-hibernate-operator.md`](enhancements/0001-hibernate-operator.md) — Control Plane + Runner Model
- **Schedule Exceptions RFC**: [`enhancements/0003-schedule-exceptions.md`](enhancements/0003-schedule-exceptions.md) — Approval workflows
- **Agent Guide**: [`AGENTS.md`](AGENTS.md) — Repository conventions
- **Helm Chart**: [`charts/hibernator/`](charts/hibernator/) — Production deployment
- **E2E Tests**: [`test/e2e/`](test/e2e/) — Integration test suite
