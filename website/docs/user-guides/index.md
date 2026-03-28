# User Guides

Step-by-step guides for common Hibernator operations.

## Core Operations

| Guide | Description |
|-------|-------------|
| [Hibernation Lifecycle](hibernation-lifecycle.md) | Create, monitor, and manage a full hibernation cycle |
| [Execution Strategies](execution-strategies.md) | Configure Sequential, Parallel, DAG, and Staged strategies |
| [Schedule Exceptions](schedule-exceptions.md) | Create temporary schedule overrides |
| [Multi-Window Schedules](multi-window-schedules.md) | Define multiple off-hour windows |

## Operational Guides

| Guide | Description |
|-------|-------------|
| [Plan Suspension](plan-suspension.md) | Temporarily disable a plan |
| [Manual Actions](override-actions.md) | Override, restart, and retry operations outside the schedule |
| [Error Recovery](error-recovery.md) | Handle and recover from execution failures |
| [Schedule Boundaries](schedule-boundaries.md) | Understand edge cases in schedule evaluation |
| [Composing Multiple Exceptions](composing-multiple-exceptions.md) | Combine extend, suspend, and replace exceptions on the same plan |

## Executor Guides

| Guide | Description |
|-------|-------------|
| [EKS Executor](eks-executor.md) | Hibernate EKS managed node groups |
| [Karpenter Executor](karpenter-executor.md) | Hibernate Karpenter NodePools |
| [EC2 Executor](ec2-executor.md) | Stop and start EC2 instances |
| [RDS Executor](rds-executor.md) | Stop RDS instances and Aurora clusters |
| [WorkloadScaler Executor](workloadscaler-executor.md) | Scale Kubernetes workloads to zero |
| [NoOp Executor](noop-executor.md) | Test plans without real resources |

## Reference

| Guide | Description |
|-------|-------------|
| [CLI Reference](cli.md) | Install and use the `kubectl-hibernator` plugin |
| [Troubleshooting](troubleshooting.md) | Diagnose common issues |
