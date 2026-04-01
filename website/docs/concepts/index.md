# Concepts

This section explains the core concepts and architecture behind Hibernator.

## Core Resources

Hibernator introduces four Custom Resource Definitions (CRDs):

| Resource | Purpose |
|----------|---------|
| **[HibernatePlan](hibernateplan.md)** | Defines hibernation intent — schedule, targets, and execution strategy |
| **[ScheduleException](schedule-exceptions.md)** | Temporary overrides to a plan's schedule |
| **[HibernateNotification](../user-guides/notifications.md)** | Notification delivery configuration (e.g., Slack, Telegram, Webhook) |
| **[CloudProvider](connectors.md#cloudprovider)** | Cloud credentials and region configuration |
| **[K8SCluster](connectors.md#k8scluster)** | Kubernetes cluster access configuration |

## Key Principles

### Intent-Driven

You declare *what* should be hibernated, not *how*. The operator handles the mechanics of shutting down and restoring each resource type through pluggable executors.

### Dependency-Aware

Resources are orchestrated in the correct order. For example, you can express that Karpenter NodePools must be scaled down before EKS managed node groups, or that application EC2 instances must stop before the database.

### Safe Restoration

During shutdown, each executor captures restore metadata (replica counts, node pool sizes, instance states) into ConfigMaps. During wakeup, this metadata is used to restore resources to their exact pre-hibernation state.

### Isolated Execution

Each target is executed in an isolated Kubernetes Job (a "runner") with its own ServiceAccount and scoped RBAC permissions. No single runner has broad access to all resources.

## Next Steps

- **[Architecture](architecture.md)** — How the control plane and runners interact
- **[HibernatePlan](hibernateplan.md)** — Deep dive into the primary CRD
- **[Schedule Exceptions](schedule-exceptions.md)** — Temporary schedule overrides
- **[Connectors](connectors.md)** — CloudProvider and K8SCluster setup
