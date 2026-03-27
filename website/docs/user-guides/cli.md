# CLI Reference

`kubectl-hibernator` is a kubectl plugin for managing HibernatePlan resources. It provides purpose-built commands for inspecting plans, controlling hibernation operations, managing restore data, and viewing executor logs.

## Installation

Download the binary for your platform from the [GitHub Releases](https://github.com/ardikabs/hibernator/releases) page and place it on your `PATH`.

=== "Linux (amd64)"

    ```bash
    curl -Lo kubectl-hibernator \
      https://github.com/ardikabs/hibernator/releases/download/v1.5.0/kubectl-hibernator-linux-amd64
    chmod +x kubectl-hibernator
    sudo mv kubectl-hibernator /usr/local/bin/
    ```

=== "Linux (arm64)"

    ```bash
    curl -Lo kubectl-hibernator \
      https://github.com/ardikabs/hibernator/releases/download/v1.5.0/kubectl-hibernator-linux-arm64
    chmod +x kubectl-hibernator
    sudo mv kubectl-hibernator /usr/local/bin/
    ```

=== "macOS (amd64)"

    ```bash
    curl -Lo kubectl-hibernator \
      https://github.com/ardikabs/hibernator/releases/download/v1.5.0/kubectl-hibernator-darwin-amd64
    chmod +x kubectl-hibernator
    sudo mv kubectl-hibernator /usr/local/bin/
    ```

=== "macOS (arm64)"

    ```bash
    curl -Lo kubectl-hibernator \
      https://github.com/ardikabs/hibernator/releases/download/v1.5.0/kubectl-hibernator-darwin-arm64
    chmod +x kubectl-hibernator
    sudo mv kubectl-hibernator /usr/local/bin/
    ```

Verify the installation:

```bash
kubectl hibernator version
```

## Global Flags

These flags are available on every subcommand:

| Flag | Description |
|------|-------------|
| `--kubeconfig` | Path to the kubeconfig file. Defaults to `$KUBECONFIG` or `~/.kube/config`. |
| `-n, --namespace` | Kubernetes namespace. Defaults to the current context namespace. |
| `--json` | Output in JSON format. |

---

## Commands

### `list`

List all HibernatePlan resources with their current phase and next scheduled event.

**Aliases:** `ls`

```bash
kubectl hibernator list
kubectl hibernator list -A
kubectl hibernator ls -n production
```

| Flag | Description |
|------|-------------|
| `-A, --all-namespaces` | List plans from all namespaces. |

---

### `describe`

Display comprehensive details about a HibernatePlan including schedule, execution strategy, targets, and status history.

```bash
kubectl hibernator describe my-plan
kubectl hibernator describe my-plan --json
```

---

### `preview`

Preview the schedule and upcoming hibernation/wakeup events for a plan. Useful for validating schedule configuration before or after applying.

**Aliases:** `schedule`

```bash
kubectl hibernator preview my-plan
kubectl hibernator preview my-plan --events 10
kubectl hibernator preview --file plan.yaml
```

| Flag | Description |
|------|-------------|
| `-f, --file` | Path to a local HibernatePlan YAML file. Loads from cluster if not provided. |
| `--events` | Number of upcoming events to display (default: `5`). |

---

### `override`

Manually override the schedule of a HibernatePlan, forcing it toward a target phase (hibernate or wakeup). The override is **persistent** — the plan stays locked until explicitly deactivated. See [Manual Actions](override-actions.md) for full details.

```bash
# Force hibernation
kubectl hibernator override my-plan --to hibernate

# Force wakeup
kubectl hibernator override my-plan --to wakeup

# Deactivate override and restore schedule control
kubectl hibernator override my-plan --disable
```

| Flag | Description |
|------|-------------|
| `--to` | Target phase: `hibernate` or `wakeup`. Required when activating. |
| `--disable` | Deactivate the override and restore normal schedule control. Mutually exclusive with `--to`. |

!!! warning
    This is **not** a one-shot action. The plan will stay locked at the target phase until you explicitly run `kubectl hibernator override <plan> --disable`.

---

### `restart`

Re-trigger the last executor operation on a plan that is already at a stable phase (Active or Hibernated). This is a **one-shot** action — the annotation is consumed atomically by the controller. See [Manual Actions](override-actions.md) for full details.

```bash
kubectl hibernator restart my-plan
kubectl hibernator restart my-plan -n production
```

The plan must have completed at least one full hibernation cycle (`.status.currentOperation` must be recorded).

---

### `retry`

Trigger a manual retry of a plan stuck in the `Error` phase. The controller clears the error state and re-attempts the failed operation.

```bash
kubectl hibernator retry my-plan
```

!!! note
    `retry` only applies to plans in `Error` phase. For plans in Active or Hibernated phase, use `restart` instead.

---

### `suspend`

Suspend a HibernatePlan for a specified duration, preventing all hibernation operations until the deadline expires. See [Plan Suspension](plan-suspension.md) for details on the suspension mechanism.

```bash
# Suspend for 2 hours
kubectl hibernator suspend my-plan --seconds 7200 --reason "production deployment"

# Suspend until a specific time
kubectl hibernator suspend my-plan --until "2026-01-15T06:00:00Z" --reason "maintenance window"
```

| Flag | Description |
|------|-------------|
| `--seconds` | Duration in seconds to suspend. Mutually exclusive with `--until`. |
| `--until` | Deadline in RFC3339 UTC format (e.g., `2026-01-15T06:00:00Z`). Mutually exclusive with `--seconds`. |
| `--reason` | Reason for suspension (default: `"User initiated"`). |

---

### `resume`

Resume a suspended HibernatePlan, restoring normal schedule evaluation immediately.

```bash
kubectl hibernator resume my-plan
```

---

### `logs`

View controller logs filtered by plan context. Automatically discovers the controller pod and filters log entries relevant to the specified plan and its executions.

```bash
kubectl hibernator logs my-plan
kubectl hibernator logs my-plan --follow
kubectl hibernator logs my-plan --target my-cluster
kubectl hibernator logs my-plan --tail 100
kubectl hibernator logs my-plan --level error
```

| Flag | Description |
|------|-------------|
| `--target` | Filter logs by target name. |
| `--level` | Filter by level: `error` (logs with error field) or `info` (logs without errors). |
| `--tail` | Number of recent log lines to fetch (default: `500`). |
| `-f, --follow` | Stream logs continuously until interrupted. |

---

### `restore`

Manage restore points — the captured resource state during hibernation that is used to restore resources during wakeup. Restore data is stored in a ConfigMap named `restore-data-<plan-name>`.

#### `restore list`

Display restore point summary or detailed resource list for a plan.

```bash
kubectl hibernator restore list my-plan
kubectl hibernator restore list my-plan -o wide
kubectl hibernator restore list my-plan --target eks-cluster -o wide
kubectl hibernator restore list my-plan -o json
```

| Flag | Description |
|------|-------------|
| `-o, --output` | Output format: _(empty)_ for summary, `wide` for detailed list, `json` for JSON. |
| `-t, --target` | Filter by a specific target name. |

#### `restore inspect`

Display detailed restore point information for a specific resource within a target.

```bash
kubectl hibernator restore inspect my-plan --target eks-cluster --resource-id ng-main
```

| Flag | Description |
|------|-------------|
| `-t, --target` | Target name (required). |
| `-r, --resource-id` | Resource identifier (required). |

#### `restore init`

Initialize an empty restore point entry for a target. Useful for bootstrapping restore data before the first hibernation cycle.

```bash
kubectl hibernator restore init my-plan --target eks-cluster --executor eks
kubectl hibernator restore init my-plan -t db-prod -x rds --force
```

| Flag | Description |
|------|-------------|
| `-t, --target` | Target name (required). |
| `-x, --executor` | Executor type: `eks`, `rds`, `ec2`, `karpenter`, etc. (required). |
| `--force` | Overwrite an existing restore point entry for the target. |

#### `restore patch`

Update specific fields in a resource's restore state using field operations or JSON merge patch.

```bash
# Field-level updates (dot notation)
kubectl hibernator restore patch my-plan -t eks -r ng-main \
  --set desiredCapacity=10

# Remove fields
kubectl hibernator restore patch my-plan -t eks -r ng-main \
  --remove config.deprecated

# JSON merge patch
kubectl hibernator restore patch my-plan -t eks -r ng-main \
  --patch '{"desiredCapacity": 10}'

# Preview changes without applying
kubectl hibernator restore patch my-plan -t eks -r ng-main \
  --set desiredCapacity=10 --dry-run
```

| Flag | Description |
|------|-------------|
| `-t, --target` | Target name (required). |
| `-r, --resource-id` | Resource identifier (required). |
| `--set` | Set field value using dot notation (repeatable). |
| `--remove` | Remove field using dot notation (repeatable). |
| `--patch` | Inline JSON merge patch (RFC 7386). |
| `--patch-file` | Path to a JSON merge patch file. |
| `--dry-run` | Preview changes without applying. |

!!! note
    Field operations (`--set`/`--remove`) and JSON merge patch (`--patch`/`--patch-file`) are mutually exclusive — use one mode per invocation.

#### `restore drop`

Remove a specific resource from a restore point. The next hibernation cycle will capture fresh restore data.

```bash
kubectl hibernator restore drop my-plan --target eks-cluster --resource-id ng-main
```

| Flag | Description |
|------|-------------|
| `-t, --target` | Target name (required). |
| `-r, --resource-id` | Resource identifier (required). |

---

### `version`

Print the CLI plugin version.

```bash
kubectl hibernator version
```
