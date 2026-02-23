# kubectl-hibernator CLI User Guide

The `kubectl hibernator` plugin simplifies day-to-day operations with Hibernator. This guide covers installation, setup, and usage.

## Installation

### Prerequisites
- Kubernetes cluster with Hibernator operator deployed
- `kubectl` installed (v1.14+)
- Access to a kubeconfig file

### Install the Plugin

#### From Source (Development)
```bash
# Clone the repository
git clone https://github.com/ardikabs/hibernator.git
cd hibernator

# Build and install the CLI
make build-cli
sudo cp bin/kubectl-hibernator /usr/local/bin/

# Verify installation
kubectl hibernator --version
kubectl plugin list | grep hibernator
```

#### Pre-built Binary (Future: CI/CD Release)
```bash
# Download the latest release (multi-platform support)
wget https://github.com/ardikabs/hibernator/releases/download/v1.0.0/kubectl-hibernator_1.0.0_darwin-arm64.tgz
tar xzf kubectl-hibernator_*.tgz
sudo cp kubectl-hibernator /usr/local/bin/
```

### RBAC Setup

The CLI requires a ClusterRole with minimal permissions. Apply the template:

```bash
# Option 1: Apply the provided RBAC template
kubectl apply -f config/rbac/cli_role.yaml

# Option 2: Create a custom binding for your user
kubectl create clusterrolebinding hibernator-cli-user \
  --clusterrole=hibernator-cli-user \
  --user=<your-email>
```

**Required Permissions:**
- `hibernateplans`: get, list, patch (for annotations)
- `scheduleexceptions`: get, list (read-only)
- `pods/log`: get, list (for debugging)

See [config/rbac/cli_role.yaml](../../config/rbac/cli_role.yaml) for the full template.

## Common Commands

### 1. View Schedule (Validate Before Applying)

Validate your hibernation schedule before applying a plan:

```bash
# Show next 10 scheduled transitions
kubectl hibernator show schedule my-app-hibernation -n production

# Show next 20 events
kubectl hibernator show schedule my-app-hibernation --next 20

# Different namespace
kubectl hibernator show schedule my-app-hibernation -n staging
```

**Output Example:**
```
HibernationPlan: my-app-hibernation (Namespace: production)

Configured Schedule:
  Timezone: America/New_York
  Off-Hours: 20:00 - 06:00 (Monday - Friday)

Current State: Active (Running normally)

Next Schedule Transition:
  Event: Hibernating
  Time: 2026-02-20 20:00 EST (in 3 hours 42 minutes)
  Phase Duration: 10 hours (until 06:00 EST next day)

Next 10 Scheduled Events:
  1. Hibernating  → 2026-02-20 20:00 EST (Fri)
  2. Hibernated   → 2026-02-21 06:00 EST (Fri)
  3. Hibernating  → 2026-02-23 20:00 EST (Mon)
  ...
```

**When to Use:**
- Validating schedule before applying a new plan
- Checking timezone configuration
- Understanding off-hours windows

---

### 2. Check Operational Status

Display current status, phase, and execution history:

```bash
# Show plan status
kubectl hibernator show status my-app-hibernation -n production

# JSON output for scripting
kubectl hibernator show status my-app-hibernation --json

# Watch status in real-time (future enhancement)
kubectl hibernator show status my-app-hibernation --watch
```

**Output Example:**
```
HibernationPlan: my-app-hibernation (Namespace: production)
Status: Hibernating
  Began: 2026-02-20 20:05 EST
  Progress: 18/20 targets completed
  Error Count: 1 (1 failed, 1 pending)

Last Execution (Hibernation Cycle #45):
  Duration: 4 minutes 32 seconds
  Target Results:
    ✓ eks-cluster-prod-1 (completed)
    ✗ rds-database-prod (failed: timeout, will retry)
    ⏳ ec2-bastion (pending: waiting for dependency)
    ✓ karpenter-nodepool-prod (completed)

Retry Policy:
  Max Retries: 3
  Current Retry: 2/3 (Next retry in 2 minutes 18 seconds)
```

**When to Use:**
- Monitoring ongoing hibernation/wakeup execution
- Checking for errors or stuck targets
- Verifying all targets completed successfully

---

### 3. Suspend Hibernation (Emergency Maintenance)

Temporarily suspend hibernation to prevent shutdown during maintenance or incidents:

```bash
# Suspend for 24 hours (default)
kubectl hibernator suspend my-app-hibernation -n production

# Suspend for specific duration
kubectl hibernator suspend my-app-hibernation --hours 2

# Suspend until specific time
kubectl hibernator suspend my-app-hibernation --until "2026-02-21T10:00:00Z"

# Add reason for audit trail
kubectl hibernator suspend my-app-hibernation --hours 24 --reason "Emergency: Database migration in progress"
```

**Output:**
```
✓ Hibernation suspended for my-app-hibernation (production)
  Until: 2026-02-21 10:00 EST (23 hours 45 minutes)
  Reason: Emergency: Database migration in progress
```

**When to Use:**
- Emergency incident response (prevents shutdown during investigation)
- Scheduled maintenance windows (freezes automation temporarily)
- Testing/validation (prevents accidental hibernation)

---

### 4. Resume Hibernation

Resume normal hibernation schedule after suspension:

```bash
# Resume hibernation
kubectl hibernator resume my-app-hibernation -n production
```

**Output:**
```
✓ Hibernation resumed for my-app-hibernation (production)
  Schedule will resume per next planned event (in 2 hours 15 minutes)
```

**When to Use:**
- After incident resolution (restore normal schedule)
- After maintenance completion (resume automation)
- When suspension is no longer needed

---

### 5. Enforce Retry (Failed Execution Recovery)

Trigger immediate retry of a failed hibernation/wakeup without waiting for automatic backoff:

```bash
# Trigger retry (confirms if plan is in Error phase)
kubectl hibernator retry my-app-hibernation -n production

# Force retry without confirmation
kubectl hibernator retry my-app-hibernation --force
```

**Output:**
```
✓ Retry triggered for my-app-hibernation (production)
  Execution ID: exec-20260220-001
  Status: Queued for immediate execution (server will reconcile within 1-2 seconds)
```

**When to Use:**
- After fixing transient errors (DB connectivity, network issues)
- When automatic retry backoff is too slow
- To immediately re-run after confirming fix is in place

---

### 6. View Executor Logs (Debugging)

Stream or tail executor logs for debugging hibernation/wakeup issues. The CLI automatically queries **all controller replicas** to ensure complete log capture:

```bash
# Tail last 100 log lines (from all controller pods)
kubectl hibernator logs my-app-hibernation -n production

# Stream logs in real-time (like tail -f, aggregated from all pods)
kubectl hibernator logs my-app-hibernation --follow

# Filter by executor type
kubectl hibernator logs my-app-hibernation --executor rds

# Filter by target
kubectl hibernator logs my-app-hibernation --target "customer-db"

# Combine multiple filters
kubectl hibernator logs my-app-hibernation --executor rds --target "prod-db" --follow

# Show last 500 lines
kubectl hibernator logs my-app-hibernation --tail 500

# Filter by severity
kubectl hibernator logs my-app-hibernation --severity error
```

**Multi-Replica Support:**
- The CLI discovers **all running controller replicas** (HA setup)
- Logs are aggregated from all pods and sorted by timestamp
- Duplicate logs (if replicated across pods) are automatically deduplicated
- Timestamps preserve chronological order
- In follow mode, the CLI displays which pods are being queried

**Output Example:**
```
Plan: my-app-hibernation (Namespace: production)
Server: hibernator-controller-xyz (hibernator-system)

[2026-02-20 20:05:12 UTC] INFO  executor=eks-executor target=my-cluster: Starting hibernation
[2026-02-20 20:05:18 UTC] DEBUG executor=eks-executor target=my-cluster: Fetched 3 node groups
[2026-02-20 20:05:42 UTC] ERROR executor=rds-executor target=my-database: Connection timeout (will retry)
[2026-02-20 21:35:10 UTC] INFO  executor=rds-executor target=my-database: Retry #2 starting
[2026-02-20 21:35:52 UTC] INFO  executor=rds-executor target=my-database: Database hibernation successful
```

**Supported Filters:**
- `--executor`: Filter by executor type (eks, rds, ec2, karpenter, workloadscaler)
- `--target`: Filter by target name
- `--severity`: Filter by log severity (info, warn, error, debug)
- `--tail N`: Show last N lines (default: 100)
- `--follow`: Stream logs in real-time

**When to Use:**
- Debugging hibernation failures
- Understanding why a target failed
- Monitoring execution progress in real-time
- Investigating transient errors

---

### 7. Version Information

Display CLI and operator version information:

```bash
# Show CLI version
kubectl hibernator version

# JSON output
kubectl hibernator version --json
```

**Output:**
```
kubectl-hibernator v1.0.0
Commit: abc123def456
```

---

## Operational Workflows

### Workflow 1: Validate and Deploy a New Plan

```bash
# Step 1: Create the plan manifest
cat <<EOF > my-plan.yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: my-app-hibernation
spec:
  schedule:
    timezone: "America/New_York"
    offHours:
    - start: "20:00"
      end: "06:00"
      daysOfWeek: ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday"]
  targets:
  - name: eks-prod
    type: eks
    # ... target config ...
EOF

# Step 2: Validate schedule locally
kubectl hibernator show schedule -f my-plan.yaml --next 30

# Step 3: Apply the plan
kubectl apply -f my-plan.yaml

# Step 4: Monitor first execution
kubectl hibernator show status my-app-hibernation --watch
```

### Workflow 2: Handle Hibernation Failure

```bash
# Step 1: Check status
kubectl hibernator show status my-app-hibernation

# Step 2: View error logs
kubectl hibernator logs my-app-hibernation --severity error

# Step 3: Suspend if needed (prevent retry storm)
kubectl hibernator suspend my-app-hibernation --hours 1 --reason "Investigating timeout issue"

# Step 4: Fix the issue (e.g., increase timeout, fix connectivity)

# Step 5: Resume and retry
kubectl hibernator resume my-app-hibernation
kubectl hibernator retry my-app-hibernation --force

# Step 6: Monitor retry
kubectl hibernator logs my-app-hibernation --follow
```

### Workflow 3: Emergency Maintenance

```bash
# Step 1: Immediately suspend hibernation (prevents shutdown during incident)
kubectl hibernator suspend my-app-hibernation --hours 4 --reason "Emergency: Database corruption detected"

# Step 2: Handle incident (manual recovery, cleanup, etc.)

# Step 3: Verify system is healthy
kubectl hibernator show status my-app-hibernation

# Step 4: Resume automation
kubectl hibernator resume my-app-hibernation

# Step 5: Confirm next schedule event is correct
kubectl hibernator show schedule my-app-hibernation --next 3
```

---

## Global Flags

All commands support these global flags:

```bash
# Specify namespace (default: default)
kubectl hibernator <command> -n <namespace>
kubectl hibernator <command> --namespace=<namespace>

# JSON output for scripting/parsing
kubectl hibernator <command> --json

# Verbose output (debug information)
kubectl hibernator <command> -v

# Help for any command
kubectl hibernator <command> --help
kubectl hibernator --help
```

---

## Configuration

### Environment Variables

```bash
# Override controller namespace for log queries (default: hibernator-system)
export HIBERNATOR_CONTROLLER_NAMESPACE=custom-hibernator

# Override kubeconfig
export KUBECONFIG=~/.kube/custom-config

# Set default namespace
export HIBERNATOR_NAMESPACE=production
```

### Future: Config File Support (Phase 2)

```bash
# ~/.hibernator/config
namespace: production
timezone: America/New_York
controller_namespace: hibernator-system
```

---

## Troubleshooting

### "Plan not found"
```bash
# Verify plan exists
kubectl get hibernateplans -n production

# Check namespace
kubectl hibernator show status my-plan -n staging  # Specify correct namespace
```

### "Logs query failed"
```bash
# Verify controller pod exists
kubectl get pods -n hibernator-system -l app=hibernator-controller

# Check RBAC permissions
kubectl auth can-i get pods/log --as=<user> -n hibernator-system
```

### "Schedule calculation wrong"
```bash
# Verify timezone in the plan
kubectl get hibernateplan my-plan -o yaml | grep timezone

# Check for active exceptions that might override schedule
kubectl get scheduleexceptions -n production
```

### "Suspend command failed"
```bash
# Verify you have patch permissions
kubectl auth can-i patch hibernateplans --as=<user>

# Check if plan is already suspended
kubectl hibernator show status my-plan
```

---

## Tips & Best Practices

1. **Validate Before Deploy**: Always run `show schedule` with `--next 30` to verify schedule before applying
2. **Use Reason Annotations**: Include `--reason` when suspending for audit trail and clarity
3. **Monitor Real-Time**: Use `--follow` with logs command during critical migrations
4. **Combine Filters**: Use `--executor` and `--target` together for targeted debugging
5. **Check Status First**: Before running `retry`, always check plan status to understand the error
6. **JSON for Automation**: Use `--json` flag to integrate into scripts or CI/CD pipelines

---

## See Also

- [RFC-0007: kubectl hibernator Plugin](../../enhancements/0007-kubectl-hibernator-cli-plugin.md) — Design document
- [RBAC Configuration](../../config/rbac/cli_role.yaml) — Permission template
- [HibernatePlan API](../../docs/api/hibernateplan.md) — CRD reference (future)
- [Operator Manual](../../docs/operator-manual.md) — Full operator documentation (future)
