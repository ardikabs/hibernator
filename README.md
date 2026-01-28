# Hibernator Operator

> Declarative Kubernetes operator for suspending and restoring cloud infrastructure during off-hours

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go)](go.mod)
[![Kubernetes](https://img.shields.io/badge/Kubernetes-1.34+-326CE5?logo=kubernetes)](https://kubernetes.io)

## Overview

Hibernator is a Kubernetes operator that provides centralized, declarative management for suspending and restoring cloud resources during user-defined off-hours. It extends beyond Kubernetes to manage heterogeneous cloud infrastructure (EKS, RDS, EC2, and more) with dependency-aware orchestration and auditable execution.

**Key capabilities:**
- 🕐 **Timezone-aware scheduling** with cron expressions for hibernate/wake cycles
- 🔗 **Dependency orchestration** using DAG, Staged, Parallel, or Sequential execution strategies
- 🔌 **Pluggable executor model** for EKS, RDS, EC2 with extensibility for custom resources
- 🔒 **Isolated runner jobs** with scoped RBAC, IRSA, and projected ServiceAccount tokens
- 📊 **Real-time progress streaming** via gRPC (preferred) or HTTP webhooks (fallback)
- 💾 **Durable restore metadata** persisted in ConfigMaps with object-store integration planned

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

| Executor | Operations | Restore Data |
|----------|-----------|--------------|
| **EKS** | MNG scale-to-zero, Karpenter pause | Desired capacities, NodePool configs |
| **RDS** | Instance/cluster stop (optional snapshot) | Instance IDs, cluster IDs |
| **EC2** | Tag-based or ID-based instance stop | Instance IDs, states |

**Planned:** GKE, AKS, Cloud SQL, Azure SQL, Compute Engine, ASG

### Security & Compliance

- **RBAC-scoped runners**: Each Job uses ephemeral ServiceAccount with minimal permissions
- **IRSA/Workload Identity**: Cloud credentials via Kubernetes ServiceAccount projection
- **TokenReview authentication**: Streaming auth using projected SA tokens with custom audience (`hibernator-control-plane`)
- **Audit trail**: Kubernetes API audit logs + object-store access logs + execution ledger in CR status

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
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]

  execution:
    strategy:
      type: DAG
      maxConcurrency: 3
      dependencies:
        - target: dev-cluster
          dependsOn: ["dev-db"]  # Shutdown cluster after DB

  targets:
    - name: dev-db
      type: rds
      connectorRef:
        name: aws-dev
      parameters:
        instanceIds: ["dev-postgres"]
        snapshotBeforeStop: true

    - name: dev-cluster
      type: eks
      connectorRef:
        name: aws-dev
      parameters:
        clusterName: "dev-eks-1"
        managedNodeGroups: ["ng-1", "ng-2"]
```

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

### ✅ Completed (P0-P2)

- [x] Core controller with phase state machine
- [x] All 4 execution strategies (Sequential, Parallel, DAG, Staged)
- [x] EKS, RDS, EC2 executors
- [x] Cron schedule parsing with timezone support
- [x] Validation webhook with DAG cycle detection
- [x] ConfigMap-based restore data persistence
- [x] gRPC streaming server + HTTP webhook fallback
- [x] Runner streaming integration with progress reporting
- [x] TokenReview authentication

### 🚧 In Progress (P3)

- [ ] Karpenter executor (NodePool management)
- [ ] Restore data consumption in wake-up flow
- [ ] Error recovery and automatic retry logic
- [ ] Object-store artifact persistence (S3/GCS)
- [ ] Helm chart packaging

### 📋 Planned

- [ ] GCP executors (GKE, Cloud SQL, Compute Engine)
- [ ] Azure executors (AKS, Azure SQL, VMs)
- [ ] Prometheus metrics and observability
- [ ] Advanced scheduling (holidays, blackout windows)
- [ ] Multi-cluster federation

See [`WORKPLAN.md`](WORKPLAN.md) for detailed design and [`RFCs/0001-hibernate-operator.md`](RFCs/0001-hibernate-operator.md) for architecture decisions.

## Development

### Build

```bash
# Build controller
make build

# Build runner
make build-runner

# Run tests
make test

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
├── api/                      # API definitions
│   ├── v1alpha1/            # CRD types and webhook
│   └── streaming/           # Streaming API proto/types
├── cmd/
│   ├── controller/          # Controller main
│   └── runner/              # Runner main
├── config/                  # Kubernetes manifests
│   ├── crd/                # CRD definitions
│   ├── manager/            # Deployment manifests
│   ├── rbac/               # RBAC rules
│   ├── samples/            # Example CRs
│   └── webhook/            # Webhook configuration
├── internal/
│   ├── controller/         # Reconciliation logic
│   ├── executor/           # Executor implementations
│   ├── scheduler/          # Schedule evaluation & DAG planner
│   ├── restore/            # Restore data manager
│   └── streaming/          # gRPC/webhook server & client
├── RFCs/                   # Design documents
├── WORKPLAN.md             # Detailed design & milestones
└── AGENTS.md               # Agent onboarding guide
```

## Contributing

Contributions welcome! Please:
1. Read [`AGENTS.md`](AGENTS.md) for repository conventions
2. Check [`WORKPLAN.md`](WORKPLAN.md) for current priorities
3. Open an issue to discuss major changes
4. Follow existing code patterns and add tests

## License

Apache License 2.0 - see [LICENSE](LICENSE) for details.

## Links

- **Design**: [`WORKPLAN.md`](WORKPLAN.md)
- **Architecture RFC**: [`RFCs/0001-hibernate-operator.md`](RFCs/0001-hibernate-operator.md)
- **Agent Guide**: [`AGENTS.md`](AGENTS.md)
