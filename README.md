# Hibernator Operator

> Declarative Kubernetes operator for suspending and restoring cloud infrastructure during off-hours

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go)](go.mod)
[![Kubernetes](https://img.shields.io/badge/Kubernetes-1.34+-326CE5?logo=kubernetes)](https://kubernetes.io)

<div align="center">
  <img src="website/docs/assets/img/hibernator-full.png" alt="Hibernator Logo">
</div>

## Overview

Hibernator is a Kubernetes operator that provides centralized, declarative management for suspending and restoring cloud resources during user-defined off-hours. It extends beyond Kubernetes to manage heterogeneous cloud infrastructure (EKS, RDS, EC2, and more) with dependency-aware orchestration and auditable execution.

**Key capabilities:**

- 🕐 **Timezone-aware scheduling** with start/end times and day-of-week patterns
- ⏸️ **Schedule exceptions** with lead-time grace periods (extend, suspend, replace)
- 🔗 **Dependency orchestration** using DAG, Staged, Parallel, or Sequential strategies
- 🔌 **Pluggable executor model** for AWS (EKS, RDS, EC2, Karpenter)
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

```mermaid
graph TD
    subgraph CP [Control Plane]
        direction TB
        Reconciler["<b>Unified Reconciler (HibernatePlan Controller)</b><br/>• Schedule evaluation<br/>• Dependency resolution<br/>• Job lifecycle management"]
        
        subgraph Processors [Resource Processors]
            direction LR
            P1[EKS]
            P2[RDS]
            P3[EC2]
            P4[...]
        end
        
        Connectors[("<b>Connectors (Metadata)</b><br/>• CloudProvider<br/>• K8SCluster")]
        
        Streaming["<b>Streaming Server</b><br/>• Log aggregation<br/>• Progress tracking"]
        
        Reconciler --> Processors
        Processors -.-> Connectors
        Reconciler --- Streaming
    end

    Processors --> RJ1["<b>Runner Job (EKS)</b><br/>• Executor<br/>• gRPC client"]
    Processors --> RJ2["<b>Runner Job (RDS)</b><br/>• Executor<br/>• gRPC client"]
    Processors --> RJ3["<b>Runner Job (EC2)</b><br/>• Executor<br/>• gRPC client"]

    style CP fill:#f5f5f5,stroke:#333,stroke-width:2px
    style Connectors fill:#fff,stroke:#333,stroke-dasharray: 5 5
    style RJ1 fill:#fff,stroke:#333,stroke-width:1px
    style RJ2 fill:#fff,stroke:#333,stroke-width:1px
    style RJ3 fill:#fff,stroke:#333,stroke-width:1px
```

The operator adopts a **unified reconciler pattern**:

- **Control Plane**: The `HibernatePlan` controller acts as the central brain, orchestrating the lifecycle through resource-specific **processors**.
- **Connectors**: Resources like `CloudProvider` and `K8SCluster` currently serve as metadata-only containers for credentials and connectivity details.
- **Runner Jobs**: Isolated Kubernetes Jobs per target, performing the actual shutdown/wakeup operations while streaming logs back to the control plane.
- **Executors**: Pluggable implementations within the runners (EKS, RDS, EC2) handling resource-specific logic.

## Features

- **Execution Strategies**: Sequential, Parallel, DAG, and Staged orchestration.
- **Supported Executors**: EKS, RDS, EC2, Karpenter, and generic WorkloadScaler.
- **Security**: RBAC-scoped runners, IRSA support, and TokenReview authentication.
- **Schedule Exceptions**: Temporary overrides for emergency events or maintenance.

## Documentation

- 🚀 **[Usage Guide](https://ardikabs.github.io/hibernator/getting-started/)**: Installation, Quick Start, and Configuration.
- 🗺️ **[Roadmap](https://ardikabs.github.io/hibernator/roadmap/)**: Current status, planned features, and known limitations.
- 🤝 **[Contributing](CONTRIBUTING.md)**: How to get involved and development guidelines.
- 📚 **[Reference Documentation](docs/proposals/)**: Detailed design RFCs and architecture principles.