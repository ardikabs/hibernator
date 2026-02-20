# RFC-0007 MVP Implementation Checklist

**Focus**: Lean prototype of `kubectl hibernator` with 6 core commands

**RFC Reference**: [enhancements/0007-kubectl-hibernator-cli-plugin.md](../../enhancements/0007-kubectl-hibernator-cli-plugin.md)

**Last Updated**: 2026-02-20

---

## 📊 Status Summary

| Component | Status | Progress | Notes |
|-----------|--------|----------|-------|
| **Client-Side CLI** | ✅ Complete | 100% | All 6 commands implemented, tested with sample YAML |
| **Server-Side Controller** | ⏳ Pending | 0% | Needs annotation/spec handling logic review & testing |
| **Integration Tests** | ‼️ Blocked | 0% | Requires server-side work to test end-to-end |
| **E2E Tests** | ‼️ Blocked | 0% | Requires server-side work |
| **Documentation** | ✅ Complete | 100% | RFC and user journeys complete |

---

## ✅ Client-Side Commands (6 Total) — COMPLETE

### 1. `show schedule` ✅
- [x] Parse HibernatePlan from cluster OR local YAML (multi-document support)
- [x] Evaluate schedule using controller's scheduler.ScheduleEvaluator logic
- [x] Calculate next transition (time until next phase)
- [x] Display: timezone, off-hours, next N events, active exceptions
- [x] Human-readable output with time deltas ("3h45m", "2d15h")
- [x] JSON output support (`--json`)
- [x] Flags: `-n/--namespace`, `-f/--file` (local YAML), `--events N`, `--json`
- [x] Tested: Works with `config/samples/noop-hibernateplan.yaml` (multi-doc YAML)

### 2. `show status` ✅
- [x] Fetch HibernatePlan from cluster
- [x] Display: current phase, target progress, execution history
- [x] Show retry count and last retry timing
- [x] Show suspend annotations (suspend-until, suspend-reason)
- [x] Show last execution cycle with operation summaries
- [x] Flags: `-n/--namespace`, `--json`
- [x] Icons for target state: ✓ (completed), ✗ (failed), .. (running), -- (pending)

### 3. `suspend` ✅
- [x] Add `suspend-until` annotation (ISO 8601 timestamp, RFC3339)
- [x] Add `suspend-reason` annotation
- [x] Calculate deadline from `--hours` (duration math) or `--until` (parse RFC3339)
- [x] Validate: Either `--hours` or `--until` must be provided (not both)
- [x] Validate: Until timestamp must be in future
- [x] Flags: `-n/--namespace`, `--hours` (float), `--until` (RFC3339), `--reason` (optional)
- [x] Output confirmation with deadline and reason

### 4. `resume` ✅
- [x] Remove `suspend-until` annotation
- [x] Remove `suspend-reason` annotation
- [x] Set `spec.suspend=false`
- [x] Flags: `-n/--namespace`
- [x] Idempotent: No-op if plan not actually suspended (message: "not suspended")

### 5. `retry` ✅
- [x] Add `retry-now=true` annotation
- [x] Validate: Plan is in Error phase (unless `--force`)
- [x] Show previous retry count
- [x] Flags: `-n/--namespace`, `--force`
- [x] Output: Confirmation with execution ID (if available)

### 6. `logs` ✅
- [x] Discover server pod: Label selector `app.kubernetes.io/name=hibernator` in `hibernator-system` namespace
- [x] Fetch logs: `kubectl logs <server-pod>` [--follow]
- [x] Parse structured logs locally (JSON or plain text)
- [x] Filter by execution-id, executor, target if specified
- [x] Extract fields: timestamp, severity, logger, message
- [x] Flags: `-n/--namespace`, `--executor`, `--target`, `--tail N`, `--follow/-f`, `--json`
- [x] Output format: `[timestamp] [LEVEL] [logger]: [msg]` with execution-id if present
- [x] Namespace discovery: Uses `HIBERNATOR_CONTROLLER_NAMESPACE` env var or defaults to `hibernator-system`

---

## ⏳ Server-Side Controller Changes (Minimal) — PENDING

**Status**: Not yet implemented. Server-side logic must be added to controller reconciler to fully support CLI.

### 1. `suspend-until` auto-resume ⏳
- [ ] Controller checks `suspend-until` annotation on each reconcile
  - [ ] Parse ISO 8601 timestamp value
  - [ ] Compare current time against deadline
  - [ ] If deadline has passed: Treat suspension as expired (ignore annotation, allow hibernation)
  - [ ] If deadline is in future: Prevent hibernation start
- [ ] **Behavior**: Auto-resume on deadline expiration (no manual action needed)
- [ ] **Test**: Apply suspend-until in past, verify hibernation allowed; apply future deadline, verify hibernation blocked

### 2. `spec.suspend` field support ⏳
- [ ] Implement in HibernatePlan.spec (already exists in types, needs controller logic):
  ```go
  Suspend bool `json:"suspend,omitempty"`
  ```
- [ ] Controller behavior:
  - [ ] When `spec.suspend=true`: Prevent hibernation start, transition to Suspended phase
  - [ ] When `spec.suspend=false`: Allow hibernation per schedule (reset from Suspended phase)
- [ ] Track suspend state in status.phase

### 3. `retry-now` annotation handling ⏳
- [ ] Controller detects `retry-now=true` annotation
  - [ ] Trigger immediate retry when observed (don't wait for next interval)
  - [ ] Clear annotation after retry is queued (set to empty or remove)
- [ ] **Test**: Add annotation, verify controller reconciles immediately

### 4. Execution-ID persistence ⏳
- [ ] Verify `execution-id` is populated in `status.executions[]` for each target
- [ ] Used by CLI `logs` command to correlate logs with executions
- [ ] **Check**: Ensure runner populates execution-id in status updates

### 5. Structured logging verification ⏳
- [ ] Verify server writes structured logs to stdout with format:
  ```
  [timestamp] [severity] [execution-id] [executor] [target] [message]
  ```
  OR acceptable structured fields in JSON logs with:
  - `ts` (timestamp)
  - `level` (severity: INFO, WARN, ERROR, DEBUG)
  - `execution-id` (or similar field name)
  - `executor` (executor type)
  - `target` (target name)
  - `msg` (message)
- [ ] **Note**: CLI parses whatever format server uses; server logs are existing functionality

---

## Installation & RBAC ✅

- [x] Binary location: `cmd/kubectl-hibernator/main.go` ✅
- [x] Build output: `bin/kubectl-hibernator` ✅
- [x] Build target: `make build-cli` ✅
- [x] Installation: Manual copy to PATH or pkg manager (documented in RFC) ✅
- [x] RBAC template (already in RFC):
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
    verbs: ["get", "list"]
  - apiGroups: [""]
    resources: ["pods", "pods/log"]
    verbs: ["get", "list"]  # Server pod resource discovery + log access
  ```
- [x] RBAC: No runner pod access required (server pod logs only)

---

## Testing (MVP Phase) — PARTIAL

### Unit Tests ⏳ TBD
- [ ] Schedule evaluation with various timezones
- [ ] Cron expression parsing (ConvertOffHoursToCron)
- [ ] Command flag parsing and validation
- [ ] Output formatting (human-readable vs JSON)
- [ ] Time delta calculation (humanDuration function)

### Integration Tests ⏳ TBD
- [ ] Kubeconfig authentication (with real cluster)
- [ ] Cluster resource reads (HibernatePlan get/list)
- [ ] Resource writes (annotation patch)
- [ ] Label-based pod discovery
- [ ] Pod logs streaming

### E2E Tests ⏳ BLOCKED (Server-side Required)
- [ ] Full workflow: `show schedule` → `suspend` → `show status` → `retry` → `logs` → `resume`
- [ ] Verify server respects suspend-until annotation
- [ ] Verify server respects spec.suspend field
- [ ] Verify server accepts and processes retry-now annotation
- [ ] Verify logs contain execution-id for filtering

### Error Cases ⏳ TBD
- [ ] Missing plan: "HibernatePlan not found"
- [ ] Invalid annotations: Malformed RFC3339 timestamp
- [ ] Server pod not found: "No controller pod found"
- [ ] No cluster connection: kubeconfig errors
- [ ] Invalid YAML file: parse errors

---

## ✅ Completed This Session (2026-02-20)

1. ✅ Added cobra dependency (v1.10.2) to go.mod
2. ✅ Added `AnnotationSuspendUntil` and `AnnotationSuspendReason` constants to wellknown/annotations.go
3. ✅ Added exported `NewCronParser()` helper to scheduler/schedule.go for reuse by CLI
4. ✅ Implemented all 6 CLI commands with full functionality
5. ✅ Updated Makefile with `build-cli` target
6. ✅ Verified binary builds and tested with sample YAML
7. ✅ Created RFC and user journey documentation

---

## 🔴 Blocking Issues & Clarifications Needed (For Tomorrow)

### Critical Path Blockers

1. **Server-side suspend-until logic** ⏳
   - Need to implement in controller reconciler
   - Must auto-resume when deadline expires
   - **Question for review**: Should auto-resumption log an event or annotation change?

2. **spec.suspend field controller logic** ⏳
   - Currently exists in types but has no controller behavior
   - Need to track Suspended phase separately
   - **Question for review**: Should Suspended phase prevent ALL operations or just hibernation start?

3. **retry-now annotation handling** ⏳
   - Need controller to detect and process this annotation
   - Should it be cleared automatically after retry is queued?
   - **Question for review**: What's the correct order of operations when both retry-now and suspend-until are present?

4. **Execution-ID tracking in logs** ⏳
   - CLI assumes execution-id is available in status for correlation
   - Need to verify runner populates this correctly
   - **Question for review**: Is execution-id currently being persisted in status.executions[]?

### Design Questions for Review

1. **Suspend behavior with spec.suspend=true**:
   - Should CLI always prefer annotation-based suspend (`suspend-until`) or also use `spec.suspend`?
   - Current implementation: suspend adds `suspend-until` annotation (user-driven), resume sets `spec.suspend=false` + removes annotations
   - **Is this the right balance?**

2. **Auto-resume deadline expiration**:
   - When `suspend-until` deadline passes, should controller automatically remove the annotation?
   - Or just allow hibernation without annotation removal?
   - **What's the preferred approach for audit/cleanup?**

3. **Retry-now annotation clearing**:
   - Should controller automatically clear `retry-now` annotation after detecting it?
   - Or leave it for user to clean up?
   - **Preference for idempotence and debugging?**

4. **Logs filtering with missing execution-id**:
   - What if runner doesn't populate execution-id in status?
   - CLI currently filters by plan name + cycle ID as fallback
   - **Is this acceptable for MVP?**

### Documentation Gaps (For RFC Update)

1. **Server-side reconciliation flow** — Need pseudo-code showing exact order of checks for suspend-until, spec.suspend, retry-now
2. **Phase transition chart** — When does Suspended phase get set/cleared?
3. **Annotation cleanup policy** — Which annotations should controller clean up vs. leave for user?
4. **Error propagation** — How should controller handle invalid RFC3339 in suspend-until annotation?

---

## Summary

**Files Created** (9 total):
- `cmd/kubectl-hibernator/main.go`
- `cmd/kubectl-hibernator/cmd/root.go`
- `cmd/kubectl-hibernator/cmd/show.go`
- `cmd/kubectl-hibernator/cmd/schedule.go`
- `cmd/kubectl-hibernator/cmd/status.go`
- `cmd/kubectl-hibernator/cmd/suspend.go`
- `cmd/kubectl-hibernator/cmd/resume.go`
- `cmd/kubectl-hibernator/cmd/retry.go`
- `cmd/kubectl-hibernator/cmd/logs.go`

**Files Modified** (4 total):
- `go.mod` — Add cobra v1.10.2
- `internal/wellknown/annotations.go` — Add suspend annotations
- `internal/scheduler/schedule.go` — Add NewCronParser helper
- `Makefile` — Add build-cli target

**Dependencies Added**:
- `github.com/spf13/cobra` v1.10.2

**Status**: Client-side MVP ✅ Complete. Server-side pending review & implementation.
