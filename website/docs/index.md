---
hide:
  - navigation
---

# Hibernator

<div align="center">
  <img src="assets/img/hibernator-full.png" alt="Hibernator Logo">
</div>

**Declarative Kubernetes operator for suspending and restoring cloud infrastructure during off-hours.**

---

## What is Hibernator?

Hibernator is a Kubernetes operator that provides centralized, declarative management for suspending and restoring cloud resources during user-defined off-hours. It extends beyond Kubernetes to manage heterogeneous cloud infrastructure (EKS, RDS, EC2, and more) with dependency-aware orchestration and auditable execution.

## Key Capabilities

- :material-clock-outline: **Timezone-aware scheduling** with start/end times and day-of-week patterns
- :material-pause-circle-outline: **Schedule exceptions** with an on-demand schedule override (extend, suspend, replace)
- :material-graph-outline: **Dependency orchestration** using DAG, Staged, Parallel, or Sequential strategies
- :material-power-plug-outline: **Pluggable executor model** for AWS (EKS, RDS, EC2, Karpenter)
- :material-lock-outline: **Isolated runner jobs** with scoped RBAC, IRSA, and projected ServiceAccount tokens
- :material-chart-line: **Real-time progress streaming** via gRPC or HTTP webhooks between runners and control plane
- :material-database-outline: **Durable restore metadata** persisted in ConfigMaps for safe recovery

## Why Hibernator?

**Problem:** Teams running non-production environments (DEV/STG) waste resources during off-hours. Ad-hoc scripts lack coordination, auditability, and safe restore semantics when dealing with dependencies across Kubernetes clusters, databases, and compute instances.

**Solution:** Hibernator provides intent-driven infrastructure suspension with:

- Declarative `HibernatePlan` CRDs defining *what* to suspend, not *how*
- Controller-managed dependency resolution preventing race conditions
- Central status ledger with execution history, logs, and restore artifact references
- GitOps-friendly configuration with validation webhooks

## Quick Links

<div class="grid cards" markdown>

-   :material-rocket-launch-outline: **[Getting Started](getting-started/index.md)**

    Install and configure Hibernator in minutes.

-   :material-lightbulb-outline: **[Concepts](concepts/index.md)**

    Understand the architecture and core resources.

-   :material-book-open-outline: **[User Guides](user-guides/index.md)**

    Step-by-step guides for common operations.

-   :material-file-document-outline: **[API Reference](api-reference/index.md)**

    Complete CRD field documentation.

</div>
