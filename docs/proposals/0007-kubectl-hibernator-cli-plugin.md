---
rfc: RFC-0007
title: kubectl hibernator Plugin for Day-to-Day Operations
status: In Progress 🚀
date: 2026-02-20
last-updated: 2026-02-23
---

# RFC 0007 — kubectl hibernator Plugin for Day-to-Day Operations

**Keywords:** CLI, kubectl-plugin, Operational-Management, Schedule-Validation, Debug, Observability, User-Experience

**Status:** In Progress 🚀 (Client-side: ✅ Complete | Server-side: ✅ Implemented, ⏳ Verification)

## Summary

Introduce a `kubectl` plugin named `hibernator` to simplify day-to-day operational management of Hibernator. This plugin enables users to:

- **Validate schedules** before applying a `HibernatePlan` (dry run)
- **Display operational status** of hibernation plans
- **Suspend/resume hibernation** with a single command
- **Enforce retries** with a single annotation
- **View executor logs** for debugging

The plugin integrates seamlessly with the Kubernetes ecosystem, following standard kubectl conventions and requiring only read/write access to `HibernatePlan` resources.

## Motivation

Current operational workflows require direct manifest editing or kubectl annotation commands, creating friction for daily tasks:

- **Schedule Validation**: Users must apply a plan to see if the schedule is correct; mistakes can cause unintended hibernation windows
- **Manual Annotation Management**: Retrying failed executions requires manual annotation editing
- **Status Visibility**: Checking plan status and execution history requires multiple kubectl commands or direct manifests
- **Log Access**: Debugging executor failures requires querying the server for logs

A dedicated CLI plugin reduces operational overhead and improves user experience by providing quick, discoverable commands for these common tasks.

## Goals

1. Provide schedule validation before plan application (dry run)
2. Enable real-time operational status checks
3. Support suspension/resumption of hibernation with lead-time awareness
4. Simplify retry annotation management
5. Offer log tailing for executor debugging
6. Follow kubectl plugin conventions for seamless integration
7. Require minimal RBAC (read `HibernatePlan`, write annotations)

## Non-Goals

- Deploy or manage the Hibernator operator itself (use Helm charts)
- Replace `kubectl edit` for complex modifications
- Provide UI dashboard (CLI-focused only)
- Support non-standard Kubernetes clusters

## Proposal

### Architecture

The `kubectl hibernator` plugin is a standalone binary that:

1. **Reads `HibernatePlan` resources** from the connected cluster using kubeconfig
2. **Evaluates schedules locally** using the same schedule evaluation logic as the controller
3. **Modifies resource annotations** for suspend/retry operations
4. **Fetches executor logs** from the Hibernator server pod via `kubectl logs`

### Command Structure

All commands follow the verb-focused pattern:

```bash
# Show schedule plan for validation (dry run)
kubectl hibernator show schedule <plan-name> [-n namespace] [--next 10]

# Display hibernateplan operational status
kubectl hibernator show status <plan-name> [-n namespace] [--watch]

# Suspend hibernation with optional lead time
kubectl hibernator suspend <plan-name> [-n namespace] [--hours 24]

# Resume hibernation (remove suspend annotation)
kubectl hibernator resume <plan-name> [-n namespace]

# Enforce immediate retry (add retry-now annotation)
kubectl hibernator retry <plan-name> [-n namespace] [--force]

# Stream executor logs (last 6 hours)
kubectl hibernator logs <plan-name> [-n namespace] [--tail 100] [--follow]
```

### Command Details

#### 1. Show Schedule (`show schedule`)

**Purpose**: Display upcoming hibernation/wakeup events to validate schedule correctness before applying. Shows exactly when the next transition will occur.

**Output**:
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
  4. Hibernated   → 2026-02-24 06:00 EST (Tue)
  ... (next 6 events)

Active Exceptions:
  - Suspend until 2026-02-28 10:00 EST (Lead time: 2 hours, 30 min remaining)
```

**Flags**:
- `--namespace, -n`: Target namespace (default: default)
- `--next N`: Show next N events (default: 10, max: 30)
- `--timezone TZ`: Override schedule timezone for display (for validation in different TZ)

**Exit codes**:
- `0`: Success
- `1`: Plan not found
- `2`: Invalid schedule format

#### 2. Show Status (`show status`)

**Purpose**: Display current operational status, including phase, last execution results, and any errors.

**Output**:
```
HibernatePlan: my-app-hibernation (Namespace: production)
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

Active Exceptions:
  - None
```

**Flags**:
- `--namespace, -n`: Target namespace (default: default)
- `--json`: Output as JSON for scripting

#### 3. Suspend Hibernation (`suspend`)

**Purpose**: Temporarily suspend hibernation to prevent accidental shutdowns during maintenance or incidents.

**Behavior**:
- Adds `hibernator.ardikabs.com/suspend-until=<timestamp>` annotation
- Adds `hibernator.ardikabs.com/suspend-reason=<reason>` annotation
- Server will respect suspend-until and auto-resume when deadline expires
- Controller prevents hibernation start if suspend-until annotation exists

**Output**:
```
✓ Hibernation suspended for my-app-hibernation (production)
  Until: 2026-02-21 10:00 EST (23 hours 45 minutes)
  Reason: Emergency incident - database under investigation
```

**Flags**:
- `--namespace, -n`: Target namespace (default: default)
- `--hours H`: Suspend for H hours (default: 24)
- `--until TIMESTAMP`: Suspend until specific ISO 8601 timestamp
- `--reason STR`: Reason for suspension (stored in annotation)

**Example**:
```bash
kubectl hibernator suspend my-plan -n prod --hours 2 --reason "Hotfix deployment"
```

#### 4. Resume Hibernation (`resume`)

**Purpose**: Resume normal hibernation schedule after suspension.

**Behavior**:
- Removes `hibernator.ardikabs.com/suspend-until` annotation
- Removes `hibernator.ardikabs.com/suspend-reason` annotation
- Sets `spec.suspend` to `false` (allows hibernation to run per schedule)
- Allows hibernation to proceed per normal schedule

**Output**:
```
✓ Hibernation resumed for my-app-hibernation (production)
  Schedule will resume per next planned event (in 2 hours 15 minutes)
```

**Flags**:
- `--namespace, -n`: Target namespace (default: default)

#### 5. Enforce Retry (`retry`)

**Purpose**: Manually trigger retry of a failed execution without waiting for automatic backoff.

**Behavior**:
- Adds `hibernator.ardikabs.com/retry-now=true` annotation
- Server (controller) observes this annotation and immediately reconciles
- Triggers retry regardless of backoff or retry count state

**Output**:
```
✓ Retry triggered for my-app-hibernation (production)
  Execution ID: exec-20260220-001
  Status: Queued for immediate execution (server will reconcile within 1-2 seconds)
```

**Flags**:
- `--namespace, -n`: Target namespace (default: default)
- `--force`: Skip confirmation prompt

**Example**:
```bash
kubectl hibernator retry my-plan -n prod --force --until "2026-02-21T10:00:00Z"
```

#### 6. Tail Logs (`logs`)

**Purpose**: View executor and runner logs from the hibernation execution, filtered by executor and target.

**Behavior**:
1. Discovers ALL running controller pod replicas (HA-aware)
2. Fetches logs from **all controller pods concurrently**
3. Parses logs locally using pattern-based matching to extract relevant fields: execution-id, executor, target, severity
4. Aggregates logs and sorts by timestamp (chronological order)
5. Deduplicates logs (in case a log appears in multiple pods)
6. Filters based on optional `--executor` and `--target` parameters
7. With `--follow`, streams live logs from all pods, merging by timestamp

**Multi-Replica Support**:
- In HA setups with multiple controller replicas, the CLI queries all running pods
- Logs are aggregated from all pods for complete visibility
- Duplicates are automatically removed using content-based deduplication (MD5 hash)
- In follow mode, timestamps are used to preserve chronological order across pods
- User sees a message showing which pods are being queried

**How It Works**:
1. CLI discovers controller pods: `kubectl get pods -l app.kubernetes.io/name=hibernator -n hibernator-system`
2. Fetches logs from **all running pods** concurrently using goroutines
3. Each pod stream is parsed independently with pattern-based matching
4. All log lines collected and sorted by timestamp
5. Deduplication removes identical logs from multiple pods
6. Reformats for human-readable display
7. This approach is resilient to HA deployments and ensures no logs are missed

**Output** (formatted from JSON logs):
```
Following logs from 3 controller pod(s):
  - hibernator-controller-abc
  - hibernator-controller-def
  - hibernator-controller-ghi

[2026-02-20 20:05:12 UTC] INFO  executor=eks-executor target=my-cluster: Starting hibernation
[2026-02-20 20:05:18 UTC] DEBUG executor=eks-executor target=my-cluster: Fetched 3 node groups
[2026-02-20 20:05:42 UTC] ERROR executor=rds-executor target=my-database: Connection timeout (will retry)
[2026-02-20 21:35:10 UTC] INFO  executor=rds-executor target=my-database: Retry #2 starting
[2026-02-20 21:35:52 UTC] INFO  executor=rds-executor target=my-database: Database hibernation successful
```

**Log Format** (Server outputs JSON; CLI parses and filters):
- Server logs are in JSON format with various fields
- CLI uses pattern-based regex matching to extract execution context (executor, target, timestamp, severity)
- Human-readable output shown to user (reformatted from parsed JSON)

**Examples**:
```bash
# View recent logs from last hibernation of plan "production"
kubectl hibernator logs production

# Stream live logs as hibernation happens
kubectl hibernator logs production --follow

# Only show RDS executor logs
kubectl hibernator logs production --executor rds

# Only show logs for specific target
kubectl hibernator logs production --target "customer-db"

# Combine filters
kubectl hibernator logs production --executor rds --target "customer-db"

# Different namespace
kubectl hibernator logs production -n staging
```

**Flags**:
- `--namespace, -n`: Target namespace (default: default)
- `--execution-id ID`: Filter by specific execution (read from status if omitted)
- `--tail N`: Show last N lines (default: 100)
- `--follow, -f`: Stream logs in real time
- `--severity LEVEL`: Filter by log severity (info, warn, error, debug)
- `--executor TYPE`: Filter by executor type (eks, rds, ec2, karpenter, workloadscaler)
- `--target NAME`: Filter by target name
- `--json`: Output as JSON

**Server-Side Requirement**:
- Server pod logs must contain structured executor output with execution-id
- Each log line should include: `[timestamp] [severity] [execution-id] [executor] [target] [message]`
- Logs persist for entire server pod lifecycle (typically 7+ days before pod restart)

### Implementation Details

#### Binary Structure

```
cmd/
  kubectl-hibernator/
    main.go                    # Entry point
    cmd/
      schedule.go              # Show schedule command
      status.go                # Show status command
      suspend.go               # Suspend/resume commands
      retry.go                 # Retry command
      logs.go                  # Logs command (multi-pod aggregation)
    pkg/
      scheduler/               # Local schedule evaluation (reuse from internal/)
      output/                  # Formatting & display utilities
      client/                  # Kubernetes client helpers
```

**Multi-Pod Log Aggregation** (for HA/replica deployments):
- CLI discovers all running controller pod replicas via label selector
- Uses concurrent goroutines to fetch logs from all pods in parallel
- Aggregates log streams and sorts by timestamp for chronological order
- Deduplicates logs using content-based MD5 hash (prevents duplicates from replicas)
- In follow mode: streams from all pods, merging real-time output by timestamp
- User sees which pods are being queried (stderr output)

#### Installation

Users install the plugin via:

```bash
# Manual: Copy binary to PATH
sudo cp bin/kubectl-hibernator /usr/local/bin/

# Or via package manager (brew, apt, yum in future)
brew install hibernator/tap/kubectl-hibernator

# Verify
kubectl hibernator --version
```

After installation, it's automatically discoverable:

```bash
kubectl plugin list | grep hibernator
kubectl hibernator --help
```

### Schedule Evaluation Logic

The `show schedule` command reuses the controller's schedule evaluation to ensure consistency and calculate the next transition:

1. Parse `HibernatePlan.spec.schedule` (same as controller)
2. Evaluate cron expression with timezone awareness
3. Consider active `ScheduleException` resources that reference this plan
4. Generate next N events (intersection of schedule + exceptions)
5. **Calculate next transition**: Find the imminent event and show:
   - Event type (Hibernating or Hibernated)
   - Exact timestamp
   - Time remaining until event
   - Duration of that phase (time until next phase)
6. Display in human-readable format with timeline callouts

**Next Transition Calculation**:
- Current time: 2026-02-20 16:15 EST
- Schedule: 20:00-06:00 weekdays
- Next event: 20:00 = approximately 3h 45m away
- Event output: `Event: Hibernating, Time: 2026-02-20 20:00 EST (in 3 hours 45 minutes), Phase Duration: 10 hours`

### RBAC Requirements

The plugin only requires access to HibernatePlan resources and server pod logs:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: hibernator-cli-user
rules:
- apiGroups: ["hibernator.ardikabs.com"]
  resources: ["hibernateplans"]
  verbs: ["get", "list", "patch"]  # patch for annotations only
- apiGroups: ["hibernator.ardikabs.com"]
  resources: ["scheduleexceptions"]
  verbs: ["get", "list"]            # read-only for context
- apiGroups: [""]
  resources: ["pods", "pods/log"]
  resourceNames: ["hibernator-controller-*"]  # Access server pod logs ONLY
  verbs: ["get", "list"]
```

**Why No Runner Pod Access?**
- Server pod logs are persistent and accessible
- Server captures all runner output during execution
- No need to hunt for ephemeral runner pods
- Simplified RBAC: only server pod access, not runner pods
- Logs persist for entire server pod lifecycle

Users can be assigned the `hibernator-cli-user` role for operational access without full operator permissions.

Users can be assigned the `hibernator-cli-user` role for operational access without full operator permissions.

## Server-Side Changes Required (MVP Phase)

The CLI requires minimal changes to the Hibernator control plane:

### 1. Suspend-Until Annotation Support (Auto-Resume)

**Controller behavior**:
- When `hibernator.ardikabs.com/suspend-until` annotation is present:
  - Parse the ISO 8601 timestamp
  - Prevent hibernation start if current time is before deadline
  - **Auto-resume**: If annotation exists but deadline has passed, treat as not suspended (ignore annotation)
- When annotation is removed by user: Immediately allow hibernation per schedule

**No new fields required** — uses existing annotation mechanism.

### 2. Spec.Suspend Support

**Existing field in `HibernatePlan.spec`**:
```go
type HibernatePlanSpec struct {
  // ... existing fields ...
  Suspend bool `json:"suspend,omitempty"`  // Set to true to suspend, false to resume
}
```

**Controller behavior**:
- When `spec.suspend=true`: Prevent hibernation start
- When `spec.suspend=false`: Allow hibernation per schedule (if no suspend-until annotation)

### 3. Execution-ID Tracking

**Existing in HibernatePlan.status**: Ensure execution metadata includes:
```go
type ExecutionRecord struct {
  ExecutionID string      `json:"executionId"`  // Unique per hibernation/wakeup cycle
  StartTime   metav1.Time `json:"startTime"`
  EndTime     *metav1.Time `json:"endTime,omitempty"`
  Phase       string      `json:"phase"`        // Hibernating, Hibernated, WakingUp, etc.
  // ... existing fields ...
}
```

CLI uses `executionId` to correlate logs from server pod output.

### 4. Server Pod Logging (JSON-based with Pattern Matching)

**Requirement**: Hibernator server pod writes logs in JSON format; CLI parses using pattern-based regex matching.

**Format**: Server logs are JSON (standard logr JSON output); CLI extracts executor context via patterns:
- Pattern matching identifies: executor names, target names, timestamps, severity levels
- CLI handles unstructured JSON gracefully (no strict schema required)
- Examples of extractable patterns: `executor="rds"`, `target="prod-db"`, timestamps in ISO format

**CLI Access**:
1. CLI fetches logs via `kubectl logs <server-pod>`
2. Applies pattern-based filtering locally (executors, targets, severity)
3. Reformats for human-readable display
4. This approach is resilient to log format changes (patterns adapt independently)

### Error Handling & User Feedback

All commands include:

- **Clear error messages**: "Plan 'my-app' not found in namespace 'production'"
- **Suggestions**: "Did you mean 'my-app-hibernation'?" (fuzzy matching)
- **Validation feedback**: "Schedule conflict: Suspend ends at 10:00 but next phase begins at 09:30"
- **Success confirmations**: ✓/✗ icons with clear outcomes
- **Retry window warnings**: "Current time is outside retry-until window (expires at 2026-02-20T20:00Z); retry denied by server"

### API Conventions

**Annotation Keys** (used by CLI to control behavior):
- `hibernator.ardikabs.com/suspend-until`: ISO 8601 timestamp when suspension expires
- `hibernator.ardikabs.com/suspend-reason`: Human-readable reason for suspension
- `hibernator.ardikabs.com/retry-now`: Trigger immediate retry (value: "true")

**Spec Fields**:
- `spec.suspend`: Boolean to suspend (true) or resume (false) hibernation

### Testing Strategy

1. **Unit Tests** (`cmd/kubectl-hibernator/*_test.go`):
   - Schedule evaluation with various timezones
   - Command flag parsing
   - Output formatting

2. **Integration Tests**:
   - Actual kubeconfig authentication
   - Cluster resource reads/writes
   - Annotation updates
   - Server pod log queries via kubectl logs

3. **E2E Tests** (`test/e2e/cli/`):
   - Full workflow: validate schedule → suspend → retry → tail logs
   - Error cases and recovery
   - Server-side retry-until validation
   - Suspend history recording

## Server-to-Client Protocol

The CLI and server follow this contract for all operations:

### Suspend/Resume Contract
- **Client**: Sets `suspend-until` and `suspend-reason` annotations, or sets `spec.suspend=false` to resume
- **Server**: Checks annotations on each reconcile; auto-resumes if deadline has passed
- **Server**: Respects `spec.suspend=true/false` for manual suspend/resume

### Retry Contract
- **Client**: Sets `retry-now=true` annotation to trigger immediate retry
- **Server**: Detects annotation and queues for immediate retry

### Logs Query Contract
- **Client**: Queries server pod logs via `kubectl logs`, parses locally by execution-id, executor, target
- **Server**: Writes structured logs to stdout containing execution-id, executor, target, severity

## Trade-offs

| Decision | Trade-off | Rationale |
|----------|-----------|-----------|
| **Server-side logs via kubectl vs. custom API** | Server writes to stdout; CLI uses standard `kubectl logs` | Standard Kubernetes mechanism; no custom API needed; logs accessible via RBAC; persistent for pod lifecycle |
| **Suspend via annotations vs. CRD status history** | MVP uses annotations only; no status history needed | Simpler initial implementation; status history can be added in Phase 2 |
| **Spec.suspend field vs. annotation-only** | Uses spec field for explicit suspend control | Provides clear intent separate from reason annotations |

## MVP Deliverables (Phase 1)

### ✅ Client-Side (CLI Binary) — COMPLETE (2026-02-20)

**Implementation**: `cmd/kubectl-hibernator/` with 6 commands (show schedule, show status, suspend, resume, retry, logs)

- [x] Core binary with 6 commands
  - [x] `show schedule` — Evaluate plan schedule locally, show next N events, display active exceptions
  - [x] `show status` — Display phase, target progress, execution history, suspend/retry state
  - [x] `suspend` — Add suspend-until + suspend-reason annotations (`--hours` or `--until` + optional `--reason`)
  - [x] `resume` — Remove suspend annotations and set `spec.suspend=false`
  - [x] `retry` — Add retry-now annotation (validates Error phase unless `--force`)
  - [x] `logs` — Discover server pod, stream/tail logs, filter by executor/target
- [x] Schedule evaluation with next transition calculation (using reused scheduler.ScheduleEvaluator)
- [x] Logs discovery via label selector, streaming via `kubectl logs` with local filtering
- [x] Multi-document YAML support for `--file` (auto-detects HibernatePlan document)
- [x] JSON output format (`--json` flag) for all commands
- [x] Help text and examples via cobra auto-help
- [x] RBAC template for plugin user role
- [x] Build target: `make build-cli` → `bin/kubectl-hibernator`
- [x] Dependencies added: cobra v1.10.2
- [x] Tested with sample YAML: schedule evaluation works correctly

**Known Limitations**:
- Logs command requires controller pod(s) in `hibernator-system` namespace (or `HIBERNATOR_CONTROLLER_NAMESPACE` env var)
- Schedule evaluation uses local time (no clock parameter exposed in CLI yet)
- Log filtering uses pattern-based matching on JSON output (resilient but dependent on field presence in logs)
- In follow mode, real-time merging may occasionally show slight timestamp out-of-order if stream latency differs between pods (rare)

### ✅ Server-Side (Control Plane) — MOSTLY COMPLETE

**Completed tasks**:
- [x] Handle `suspend-until` annotation (parse RFC3339 deadline, auto-resume when expired)
- [x] Server logs in JSON format with pattern-based extraction by CLI
- [x] Execution-id tracking in status

**Verification/Integration tasks**:
- [ ] Verify `suspend-reason` annotation is propagated (informational)
- [ ] Verify `spec.suspend=true/false` prevents hibernation start when true
- [ ] Verify `retry-now` annotation triggers immediate retry on controller reconcile
- [ ] Verify `execution-id` is consistently populated in `status.executions[]` for log correlation
- [ ] End-to-end testing: CLI ↔ Server integration validation

**Documentation**:
- [x] RBAC template: [config/rbac/cli_role.yaml](../../config/rbac/cli_role.yaml)
- [x] User guide: [docs/user-guide/cli.md](../../docs/user-guide/cli.md)

## Future Enhancements (Phase 2+)

- [ ] Suspend history tracking in `status.suspendHistory[]` for audit trail
- [ ] Retry-until deadline validation (prevent retries after certain time)
- [ ] `--watch` flag for real-time status polling
- [ ] Configurable log window (`--since` flag)
- [ ] Real-time status watch with TUI (terminal UI)
- [ ] Bash/zsh completion scripts
- [ ] Config file support for default namespace/timezone
- [ ] Fine-grained target-level retry control

## Alternative Designs Considered

### A. Webhook-based HTTP API

**Rejected because:** Requires separate service deployment; kubectl plugin is more accessible & follows ecosystem norms.

### B. CRD-based control via `HibernatorControl` resources

**Rejected because:** More complex for users; imperative CLI commands are more intuitive for operational tasks.

### C. In-process operator sidecar

**Rejected because:** Couples CLI logic to operator; separate binary is cleaner & independent.

## References

- [kubectl plugin documentation](https://kubernetes.io/docs/tasks/extend-kubectl/kubectl-plugins/)
- [RFC-0001: Hibernate Operator](./0001-hibernate-operator.md) — Core architecture
- [RFC-0003: Schedule Exceptions](./0003-schedule-exceptions.md) — Exception system context
- User Journeys: TBD (to be created upon RFC approval)

## Appendix A: Command Aliases (Future)

For power users, consider shorter aliases:

```bash
# Shorter aliases
kubectl hib show schedule my-plan
kubectl hib show status my-plan
kubectl hib suspend|resume my-plan
kubectl hib retry my-plan
kubectl hib logs my-plan
```

These can be Shell functions or implemented via plugin subcommands in Phase 2.
