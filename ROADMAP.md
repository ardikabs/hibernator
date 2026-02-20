# Hibernator Roadmap

This document tracks the current status and future plans for the Hibernator Operator.

## ✅ Completed (P0-P2 MVP)

- [x] Core controller with phase state machine
- [x] All 4 execution strategies (Sequential, Parallel, DAG, Staged)
- [x] EKS, RDS, EC2, Karpenter executors
- [x] Cron schedule parsing with timezone support (start/end/daysOfWeek format)
- [x] Validation webhook with DAG cycle detection
- [x] ConfigMap-based restore data persistence
- [x] gRPC streaming server + HTTP webhook fallback
- [x] Runner streaming integration with progress reporting
- [x] TokenReview authentication with projected SA tokens
- [x] Error recovery with exponential backoff retry logic
- [x] Prometheus metrics for observability
- [x] E2E test suite (hibernation, wakeup, schedule, recovery cycles)
- [x] Production-ready Helm charts with RBAC, webhook, monitoring
- [x] Schedule Exceptions (RFC-0003 Core Implementation)
  - [x] Independent ScheduleException CRD with planRef
  - [x] Three exception types: extend, suspend (with lead time), replace
  - [x] Automatic expiration and state management
  - [x] Exception history tracking in plan status
  - [x] Temporal overlap prevention (Active & Pending)
  - [x] Deterministic newest-wins selection
  - [x] DAG-aware dependency validation (failed target dependencies block execution)
  - [x] Lead-time prevention of new hibernation starts
  - [x] Behavior mode integration (Strict vs BestEffort) with bounded concurrency

## � In Progress (P2)

- [ ] **kubectl hibernator CLI Plugin for Day-to-Day Operations** (RFC-0007)
  - [ ] `show schedule` — Validate schedules before deployment
  - [ ] `show status` — Display operational status and progress
  - [ ] `suspend/resume` — Temporarily disable hibernation
  - [ ] `retry` — Enforce immediate retry of failed targets
  - [ ] `logs` — Stream executor logs for debugging
  - [ ] Plugin installation guide and RBAC templates
  - [ ] E2E test coverage for all commands

## 📋 Planned (P3-P4)

- [ ] **Schedule Exception Approval Workflows** (RFC-0003 Future Enhancement)
  - [ ] Slack DM approval integration with email-based approver notification
  - [ ] CLI-based approvals (RFC-0007 Phase 2)
  - [ ] SSO/URL-based approval workflow for enterprise
  - [ ] Dashboard UI for exception management
- [ ] GCP executors (GKE, Cloud SQL, Compute Engine)
- [ ] Azure executors (AKS, Azure SQL, VMs)
- [ ] Advanced scheduling (holidays, blackout windows, timezone exceptions)
- [ ] Multi-cluster federation
- [ ] Object-store artifact persistence (S3/GCS)

## ⚠️ Known Limitations

### Multiple OffHours Windows (MVP Constraint)

**Current Behavior:** Only the **first** `OffHourWindow` in the `spec.schedule.offHours[]` array is evaluated.

```yaml
spec:
  schedule:
    offHours:
      - start: "20:00"        # ✅ This window is processed
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
      - start: "00:00"        # ⚠️ This window is silently ignored (MVP constraint)
        end: "23:59"
        daysOfWeek: ["SAT", "SUN"]
```

**Impact:** Multi-window schedules (e.g., different hibernation rules for weekdays vs weekends) require workarounds:

#### **Workaround 1: Create Multiple HibernationPlans** (Recommended)

```yaml
# Plan 1: Weekday hibernation
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: weekday-offhours
spec:
  schedule:
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
  targets: []
---
# Plan 2: Weekend hibernation
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: weekend-offhours
spec:
  schedule:
    offHours:
      - start: "00:00"
        end: "23:59"
        daysOfWeek: ["SAT", "SUN"]
  targets: []
```

#### **Workaround 2: Use ScheduleException to Add Windows**

```yaml
# Base plan with primary window
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: dev-offhours
spec:
  schedule:
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
---
# Exception to extend hibernation on weekends
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: ScheduleException
metadata:
  name: weekend-extension
spec:
  planRef:
    name: dev-offhours
  type: extend  # Add additional windows
  validFrom: "2026-01-01T00:00:00Z"
  validUntil: "2026-12-31T23:59:59Z"
  windows:
    - start: "00:00"
      end: "23:59"
      daysOfWeek: ["SAT", "SUN"]
```

**Future Enhancement:** Multi-window support is planned for Phase 4 (see [RFC-0002 Future Enhancements](enhancements/0002-schedule-format-migration.md#future-enhancements)). No breaking changes needed; existing single-window configs will continue working unchanged.
