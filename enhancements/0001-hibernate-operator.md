---
rfc: RFC-0001
title: Hibernator Operator - Control Plane & Runner Model
status: In Progress
date: 2026-01-29
---

# RFC 0001 — Hibernator Operator: Control Plane + Runner Model

**Keywords:** Architecture, Control-Plane, Executors, Streaming, Security, Scheduling, Dependency-Resolution, Job-Lifecycle, RBAC, Restore-Metadata

**Status:** Implemented ✅ (MVP Complete)

## Summary

This RFC describes the architecture, CRD model, execution semantics, and operational lifecycle for the Hibernator Operator. The design separates a control plane (apiserver/controller) from isolated runner pods (executors) launched as Kubernetes Jobs. The RFC captures the preferred default (Kubernetes-first) dispatch flow, streaming/logging options, security controls, and required status fields for auditable restore/wakeup flows.

## Motivation

Teams running non-critical clusters (DEV/STG) need a declarative way to suspend and restore cloud resources during off-hours. Hibernation spans heterogeneous systems (EKS, Karpenter, RDS, EC2) and requires coordination, restore metadata, and an auditable execution trail. Existing CronJob-style or ad-hoc scripts do not provide dependency handling, bounded concurrency, nor a single source of truth for restore metadata.

## Goals

- Provide a declarative `HibernatePlan` CRD for scheduling and intent.
- Orchestrate multi-target hibernation and restore with explicit dependencies and bounded concurrency.
- Execute each target in an isolated runner pod (Kubernetes Job) to scope permissions and leverage Kubernetes semantics.
- Persist restore metadata and log artifacts with durable references in `status` to support wake-up.
- Offer a simple, auditable default mode based on Kubernetes primitives; allow more advanced streaming using gRPC when needed.

## Non-Goals

- Replace application-level quiescing (unless extended by the user).
- Provide autoscaling intelligence or cost-optimization beyond suspension/resume intent.

## Proposal

High-level: Keep the control plane separate from runner executors. The control plane handles scheduling, dependency resolution, Job lifecycle, artifact aggregation, and status updates. Executors run inside isolated Kubernetes Jobs and report results via durable artifacts and logs. The default flow uses Kubernetes primitives and object stores for artifacts; an optional gRPC-based streaming transport is supported.

- `HibernatePlan` CRD — expresses schedule, `execution.strategy` (type, placeholders for DAG/staged/parallel), targets, and per-target parameters. Note: explicit dependencies are placed under `spec.execution.strategy.dependencies` and are only valid when `type: DAG`.
- Executors — pluggable implementations registered with the controller. Executors implement Shutdown/WakeUp semantics and produce restore metadata.
- Runner Job — a small container image that executes the selected executor for one target invocation. Jobs run with a fixed ServiceAccount configured at the controller level; restore metadata is persisted via ConfigMap.
- Status ledger — `status.executions[]` in the CR records per-target jobRef, logsRef, restoreRef and helper resource references for wake-up.

### Execution strategies

Supported `execution.strategy.type` values: `Sequential`, `Parallel`, `DAG`, `Staged`. `maxConcurrency` bounds parallelism for `Parallel`, `DAG`, and `Staged` strategies. The controller validates DAGs and rejects cycles.

### Default/simple flow (Kubernetes-first)

1. Controller reconciles `HibernatePlan` and computes an execution plan (stages/DAG nodes) honoring `maxConcurrency`.
2. For each target ready to run, controller creates:

    - A Kubernetes `Job` (runner pod) with annotations `hibernator/plan` and `hibernator/target`.
    - The Job uses a fixed ServiceAccount configured via controller flag (no per-plan ServiceAccount creation).
    - A `ConfigMap` is used to persist restore hints that must survive until wake-up.

3. Runner executes the executor, writes restore metadata to an artifact (object store or ConfigMap) and emits logs to stdout.
4. Controller watches Job completion; on completion it reads pod logs (via Kubernetes API), copies or records artifacts (object-store path or `ConfigMap` name), and updates `status.executions[]`.
5. `ConfigMap` used for restore hints is preserved until wake-up; the controller records its reference in the plan status.

This flow is auditable because all Job/pod operations are performed through the Kubernetes API server and captured by cluster audit logs. Artifacts stored in object stores should have access logs enabled for end-to-end traceability.

### Streaming option (gRPC or webhook)

Runners may optionally stream logs/progress directly to the control plane using:

- gRPC client-streaming to `ExecutionService.StreamLogs(ExecutionId)` (preferred for low-latency), authenticated via short-lived tokens or mTLS.
- Webhook POSTs as a fallback where streaming is undesired.

Streaming is orthogonal to the default flow; the control plane must still create and track Jobs and artifacts to preserve an auditable replayable trail.

## API / CRD summary

`HibernatePlan` (spec highlights):

- `spec.schedule` — timezone-aware offHours definitions.
- `spec.execution.strategy` — `type`, `maxConcurrency`.
- `spec.targets[]` — name, type, connectorRef, parameters.
- `spec.execution.strategy.dependencies` — explicit DAG edges (only valid when `type: DAG`).

Status ledger (`status.executions[]`) fields (per target):

- `target` (name)
- `state` (Pending|Running|Completed|Failed)
- `attempts`, `startedAt`, `finishedAt`
- `jobRef` — namespace/name of Kubernetes Job
- `logsRef` — object-store path or stream id
- `restoreRef` — durable artifact reference (object store path)
- `restoreConfigMapRef` — reference to ConfigMap containing restore data

These status fields allow the wake-up sequence to locate restore artifacts and any ephemeral resources if needed during restore.

## Security

- Kubernetes identity: each runner uses a fixed `ServiceAccount` (configured via controller flag) for Kubernetes API access, RBAC enforcement, and — via IRSA — cloud IAM roles.
- Control-plane streaming auth: the runner's pod spec includes a **projected ServiceAccount token** with a custom audience (`hibernator-control-plane`). The controller injects `HIBERNATOR_EXECUTION_ID` and configures a projected volume; the runner presents this token when opening gRPC/webhook streams.
- Validation: on stream open the control plane calls `TokenReview` with the presented token and verifies the audience and expiry. The validated identity is bound to the execution ledger entry.
- Lifecycle: projected tokens are auto-rotated by kubelet and expire after `expirationSeconds` (default 600 s). No Secret objects to create or clean up.
- Optional stronger auth: short-lived mTLS client certificates (CSR flow) for higher assurance.

Rationale: projected SA tokens leverage Kubernetes-native issuance and rotation, avoid Secret churn, and integrate with `TokenReview` for validation.

### Kubernetes access & AWS/EKS authentication

The runner supports two mutually exclusive Kubernetes access modes for targets:

1. **Generic Kubernetes access (`spec.k8s`)**
   - Uses kubeconfig Secret or in-cluster config as-is.
   - No additional token wrapping is applied.

2. **AWS EKS access (`spec.eks` + `providerRef` with AWS)**
   - The runner builds kubeconfig programmatically from AWS SDK metadata.
   - Kubernetes client transport is wrapped to inject **programmatic EKS tokens** generated in-process (no exec plugins or external binaries).
   - Tokens follow the standard EKS presigned STS `GetCallerIdentity` flow with `x-k8s-aws-id`, and are refreshed automatically before expiry.

If both `spec.k8s` and `spec.eks` are set, the runner rejects the configuration at runtime with a clear error to avoid ambiguous auth behavior.

#### AWS credentials

- Static AWS access keys are supported for AWS executors and EKS token generation.
- Optional `AssumeRoleArn` may be applied on top of static keys when required.
- Session token (`AWS_SESSION_TOKEN`) is intentionally not required and not used.

## Operational / Audit considerations

- Enable Kubernetes API server audit logging to capture Job/pod lifecycle events and controller actions.
- Enable object-store access logs for artifact upload/download auditing.
- Emit Kubernetes `Event` objects and update `status.executions[]` for human-friendly tracing in `kubectl`.

## Implementation plan (phased)

1. CRD & validation: define `HibernatePlan` CRD, implement validation webhooks (DAG acyclicity, maxConcurrency).
2. Controller core: schedule evaluation, plan building, dependency resolution, status ledger mechanics.
3. Runner Job prototype: simple runner image that calls a mock executor and writes restore JSON to object store and stdout logs.
4. Default/simple dispatch: implement Job creation with fixed runner ServiceAccount, controller log collection, artifact persistence, status updates.
5. Tests: unit tests for DAG validation; envtest/integration tests for Job lifecycle, status ledger, and wake-up path.
6. Optional: streaming gRPC auth and server, TokenReview or CSR-based client cert issuance.

## Completion Criteria

**RFC-0001 will move from "In Progress 🚀" to "Implemented ✅" when the following are demonstrated in a real-world scenario:**

### Core Functionality (Must Have)

1. **Hibernation Schedule Works**
   - Schedule evaluation triggers hibernation at configured off-hours
   - Timezone-aware cron conversion produces correct hibernation windows
   - Controller transitions HibernatePlan phase: Active → Hibernating → Hibernated
   - Wake-up triggers automatically at end of off-hours window
   - Controller transitions HibernatePlan phase: Hibernated → WakingUp → Active

2. **Executors Shutdown and Wake Up Services**
   - At least 2 AWS executors demonstrate full cycle:
     - **EKS**: Scale managed node groups to zero, restore to original desired count
     - **RDS**: Stop database instance/cluster, start and verify connectivity
   - Restore metadata captured during shutdown and consumed during wake-up
   - Wake-up restores resources to pre-hibernation state (no data loss)

3. **Monitoring and Observability**
   - **Logs**: Runner job logs appear in Kubernetes (kubectl logs)
   - **Metrics**: Prometheus metrics exported for execution duration, success/failure counts
   - **Status**: HibernatePlan status.executions[] updated with per-target state, timestamps, errors
   - **Streaming**: gRPC or webhook streaming delivers progress updates to control plane

4. **Execution Orchestration**
   - DAG dependency resolution prevents out-of-order execution
   - Bounded concurrency (maxConcurrency) limits parallel job execution
   - Error recovery with exponential backoff retries transient failures
   - Status ledger provides auditable execution history

5. **Security and Isolation**
   - Runner jobs execute with isolated ServiceAccount (RBAC-scoped)
   - IRSA/workload identity authentication works for AWS API calls
   - TokenReview authentication validates streaming connections
   - No credential leakage or privilege escalation

### Validation (Should Have)

6. **End-to-End Test Coverage**
   - E2E test suite passes for full hibernation → wake-up cycle
   - Tests cover: schedule evaluation, DAG ordering, error recovery, restore data
   - Integration tests validate controller reconciliation and job lifecycle

7. **Production Readiness**
   - Helm chart deploys operator with RBAC, webhooks, and monitoring
   - Validation webhook rejects invalid HibernatePlans (DAG cycles, invalid schedules)
   - Documentation includes installation, configuration, and troubleshooting guides

**Acceptance Test**: Deploy operator to staging cluster, create HibernatePlan targeting real EKS cluster + RDS instance, verify full hibernation/wake-up cycle completes successfully with monitoring data visible.

## Implementation Status

Last updated: 2026-02-07

### Completed (MVP Phase 1)

| Component | File(s) | Notes |
|-----------|---------|-------|
| Project scaffolding | `go.mod`, `Makefile`, `Dockerfile` | Kubernetes 1.34, aws-sdk-go-v2 v1.34.0 |
| CRD types | `api/v1alpha1/*.go` | HibernatePlan, CloudProvider, K8SCluster with kubebuilder markers |
| Scheduler/planner | `internal/scheduler/planner.go` | All 4 strategies: Sequential, Parallel, DAG (Kahn's algorithm), Staged |
| Scheduler tests | `internal/scheduler/planner_test.go` | Cycle detection, unknown target validation, diamond DAG |
| EKS executor | `internal/executor/eks/` | ManagedNodeGroups scale to zero, restore state tracking, Karpenter placeholder |
| RDS executor | `internal/executor/rds/` | Instance/cluster stop, optional snapshot before stop, start logic |
| EC2 executor | `internal/executor/ec2/` | Tag-based selector, instance ID support, stop/start running instances |
| HibernatePlan controller | `internal/controller/hibernateplan_controller.go` | Phase state machine, Job dispatch, status ledger, finalizer cleanup |
| Runner binary | `cmd/runner/main.go` | Fat runner, projected SA token auth, connector loading |
| Controller main | `cmd/controller/main.go` | Manager setup, leader election, health probes |
| CRD manifests | `config/crd/bases/` | OpenAPIv3 schema for all 3 CRDs |
| RBAC & deployment | `config/manager/manager.yaml` | ClusterRole, ServiceAccount, Deployment |
| Sample CRs | `config/samples/hibernateplan_samples.yaml` | DAG, Staged, Sequential examples |

### Completed (MVP Phase 2 - P0/P1)

| Component | File(s) | Notes |
|-----------|---------|-------|
| Cron schedule parsing | `internal/scheduler/schedule.go` | Uses robfig/cron/v3, timezone-aware, next requeue calculation |
| Schedule tests | `internal/scheduler/schedule_test.go` | Work hours, night hours, timezone handling |
| Restore data manager | `internal/restore/manager.go` | ConfigMap-based persistence, per-target JSON storage |
| Restore manager tests | `internal/restore/manager_test.go` | Save/Load/LoadAll/Delete operations |
| Validation webhook | `api/v1alpha1/hibernateplan_webhook.go` | DAG cycle detection, cron validation, target uniqueness |
| Webhook tests | `api/v1alpha1/hibernateplan_webhook_test.go` | Full validation coverage |
| Runner SA configuration | `cmd/controller/main.go`, `internal/controller/hibernateplan_controller.go` | Fixed runner ServiceAccount configured via controller flag |
| Integration tests | `internal/controller/hibernateplan_controller_test.go` | envtest-based, schedule evaluation |
| Webhook manifests | `config/webhook/webhook.yaml` | ValidatingWebhookConfiguration, cert-manager integration |
| Runner RBAC | `config/rbac/runner_role.yaml` | Minimal ClusterRole for runner pods (connectors, secrets, ConfigMaps) |

### Completed (MVP Phase 3 - P2 Streaming)

| Component | File(s) | Notes |
|-----------|---------|-------|
| Proto/types definitions | `api/streaming/v1alpha1/execution.proto`, `types.go` | ExecutionService with StreamLogs, ReportProgress, ReportCompletion, Heartbeat |
| TokenReview auth validator | `internal/streaming/auth/validator.go` | Audience check for `hibernator-control-plane`, namespace/SA extraction |
| gRPC auth interceptors | `internal/streaming/auth/interceptor.go` | Unary and streaming interceptors with execution access validation |
| gRPC streaming server | `internal/streaming/server/grpc.go` | ExecutionServiceServer with log storage, progress tracking, completion handling |
| Webhook callback server | `internal/streaming/server/webhook.go` | HTTP fallback with TokenReview auth, unified payload handling |
| gRPC client | `internal/streaming/client/grpc.go` | Log buffering, heartbeat, projected token from `/var/run/secrets/stream/token` |
| Webhook client | `internal/streaming/client/webhook.go` | HTTP fallback with same StreamingClient interface |
| Auto-select client | `internal/streaming/client/client.go` | Factory that tries gRPC first, falls back to webhook |
| Runner streaming integration | `cmd/runner/main.go` | Progress reporting (10%, 20%, 30%, 50%, 90%), completion handling, error streaming, heartbeat |

### Completed (Additional Executors & Features - P2/P3)

| Component | File(s) | Notes |
|-----------|---------|-------|
| Schedule Migration | `internal/scheduler/schedule.go` | RFC-0002 implemented: start/end/daysOfWeek format with cron conversion |
| Karpenter executor | `internal/executor/karpenter/` | NodePool scaling with disruption budget and resource limit management |
| Workload Scaler | `internal/executor/workloadscaler/` | RFC-0004 implemented: Generic scale subresource downscaler for Deployments/StatefulSets/CRDs |
| Restore data quality | `internal/executor/*`, `internal/restore/` | RFC-0001 implemented: IsLive flag, quality-aware preservation, annotation-based locking |
| Prometheus metrics | `internal/metrics/metrics.go` | Execution duration, success/failure counters, reconcile metrics, restore data size |
| Error recovery | `internal/recovery/recovery.go` | Automatic retry with exponential backoff, error classification (transient vs permanent) |

### Future Work

**Future Priorities:**

| Priority | Task | Description |
|----------|------|-------------|
| P3 | Complete GCP API integration | Implement actual google.golang.org/api calls for GKE and Cloud SQL (current implementation is placeholder) |
| P3 | Azure executors | AKS, Azure SQL executors |
| P3 | E2E Tests | Complete full test suite in `test/e2e/` (framework exists) |
| P3 | Helm Chart | Finalize packaging for artifact hub |

## Appendix — examples

- See `config/samples/hibernateplan_samples.yaml` for example `HibernatePlan` configurations.

## Links

- Agent guidelines: `AGENTS.md`
