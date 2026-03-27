# FAQ

## General

### What is Hibernator?

Hibernator is a Kubernetes operator that automates the suspension and restoration of cloud infrastructure during off-hours. It uses declarative `HibernatePlan` CRDs to define what to hibernate, when, and in what order.

### What cloud providers are supported?

Currently, **AWS** is fully supported with executors for EKS (managed node groups and Karpenter), RDS, and EC2. GCP and Azure executors are planned.

### What Kubernetes version is required?

Kubernetes **1.34+** is required.

### Does Hibernator support multi-cluster setups?

Yes. Each `K8SCluster` resource can point to a different Kubernetes cluster. The operator manages resources across clusters from a single control plane.

## Scheduling

### What timezone should I use?

Use the IANA timezone of the team working with the environment (e.g., `Asia/Jakarta`, `US/Eastern`). If the environment spans multiple timezones, use `UTC` to avoid ambiguity.

### Can I define multiple off-hour windows?

Yes. The `offHours` field accepts an array of windows evaluated with OR-logic. Hibernation triggers when any window matches. See [Multi-Window Schedules](user-guides/multi-window-schedules.md).

### What happens if the controller restarts during an off-hours window?

The controller re-evaluates the schedule on startup and takes the appropriate action based on the current time and plan state. If the time is within an off-hours window and the plan is in `Active` phase, it triggers hibernation.

### How do overnight windows work?

When `end` is earlier than `start` (e.g., 20:00–06:00), the window spans midnight into the next day. The `daysOfWeek` refers to the day the window starts.

## Execution

### What's the difference between DAG and Staged strategies?

**DAG** resolves execution order from explicit dependency edges between individual targets. **Staged** groups targets into ordered stages with optional parallelism within each stage. DAG is more flexible for complex dependencies; Staged is easier to reason about for tiered architectures.

### What happens if a target fails?

With `behavior.mode: Strict`, the entire plan fails. With `behavior.mode: BestEffort`, other targets continue. In both cases, the controller retries the failed target up to `behavior.retries` times with exponential backoff.

### How do I manually retry a failed plan?

Apply the retry annotation:

```bash
kubectl annotate hibernateplan <name> -n hibernator-system \
  hibernator.ardikabs.com/retry-now=true
```

### Can I manually trigger a hibernation or wakeup outside the schedule?

Yes. Use **Override Action** to persistently drive a plan toward a target phase, or **Restart** to re-trigger the last operation as a one-shot action.

```bash
# Override: force the plan to hibernate (persistent until disabled)
kubectl hibernator override <name> --to hibernate

# Override: force the plan to wake up (persistent until disabled)
kubectl hibernator override <name> --to wakeup

# Disable override and restore normal schedule control
kubectl hibernator override <name> --disable

# Restart: re-trigger last operation (one-shot, consumed by controller)
kubectl hibernator restart <name>
```

See [Manual Actions](user-guides/override-actions.md) for details on the differences.

## Security

### How are cloud credentials managed?

Hibernator supports **IRSA** (IAM Roles for Service Accounts) as the preferred method. The runner pod's ServiceAccount is annotated with an IAM role ARN. Static credentials (via Kubernetes Secrets) are available as a fallback.

### Are runner jobs isolated?

Yes. Each target execution runs in a separate Kubernetes Job with an ephemeral ServiceAccount. Jobs have scoped RBAC permissions and no access to other targets' credentials.

### How does streaming authentication work?

Runners use **projected ServiceAccount tokens** with a custom audience (`hibernator-control-plane`). The streaming server validates tokens via the Kubernetes TokenReview API. Tokens are automatically rotated by kubelet.

## Operations

### How do I temporarily pause hibernation?

Two options:

1. **Manual suspension**: Set `spec.suspend: true` on the plan. See [Plan Suspension](user-guides/plan-suspension.md).
2. **Schedule exception**: Create a `ScheduleException` of type `suspend` for a time-bounded pause. See [Schedule Exceptions](user-guides/schedule-exceptions.md).

### Where is restore data stored?

In a ConfigMap named `restore-data-{plan-name}` in the plan's namespace. Keys follow the format `{executor}_{target-name}` with JSON-encoded restore state.

### How many execution cycles are retained in history?

Up to **5** recent cycles are retained in `status.executionHistory`. Up to **10** exception references are retained in `status.exceptionReferences`.

### How do I delete a plan safely?

```bash
kubectl delete hibernateplan <name> -n hibernator-system
```

!!! warning
    Deleting a plan does **not** automatically wake up hibernated resources. If resources are currently hibernated, wake them up first or manually restore them.
