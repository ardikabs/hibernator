
<!--
RFC: 0001
Title: Hibernator Operator - Control Plane & Runner Model
Author: Ardika Saputro (and contributors)
Status: Draft
Date: 2026-01-29
-->

# RFC 0001 — Hibernator Operator: Control Plane + Runner Model

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
- Runner Job — a small container image that executes the selected executor for one target invocation. Jobs have ephemeral helper resources (ServiceAccount/Secret/ConfigMap) scoped to the job.
- Status ledger — `status.executions[]` in the CR records per-target jobRef, logsRef, restoreRef and helper resource references for wake-up.

### Execution strategies

Supported `execution.strategy.type` values: `Sequential`, `Parallel`, `DAG`, `Staged`. `maxConcurrency` bounds parallelism for `Parallel`, `DAG`, and `Staged` strategies. The controller validates DAGs and rejects cycles.

### Default/simple flow (Kubernetes-first)

1. Controller reconciles `HibernatePlan` and computes an execution plan (stages/DAG nodes) honoring `maxConcurrency`.
2. For each target ready to run, controller creates:
   - A Kubernetes `Job` (runner pod) with annotations `hibernator/plan` and `hibernator/target`.
   - A dedicated ephemeral `ServiceAccount` and a `Secret` containing connector credentials (or inject projected token).
   - Optionally a `ConfigMap` to persist small restore hints that must survive until wake-up.
3. Runner executes the executor, writes restore metadata to an artifact (object store or ConfigMap) and emits logs to stdout.
4. Controller watches Job completion; on completion it reads pod logs (via Kubernetes API), copies or records artifacts (object-store path or `ConfigMap` name), and updates `status.executions[]`.
5. Helper resources (ServiceAccount, Secret) are cleaned up after Job completes. `ConfigMap` used for restore hints is preserved until wake-up; the controller records its reference in the plan status.

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

- `target` (type/name)
- `state` (Pending|Running|Completed|Failed)
- `attempts`, `startedAt`, `finishedAt`
- `jobRef` — namespace/name of Kubernetes Job
- `logsRef` — object-store path or stream id
- `restoreRef` — durable artifact reference (object store path)
- `serviceAccountRef`, `connectorSecretRef`, `restoreConfigMapRef` — helper resource refs created per Job

These status fields allow the wake-up sequence to locate restore artifacts and any ephemeral resources if needed during restore.

## Security

- Kubernetes identity: each runner uses a dedicated `ServiceAccount` for Kubernetes API access, RBAC enforcement, and — via IRSA — cloud IAM roles.
- Control-plane streaming auth: the runner's pod spec includes a **projected ServiceAccount token** with a custom audience (`hibernator-control-plane`). The controller injects `HIBERNATOR_EXECUTION_ID` and configures a projected volume; the runner presents this token when opening gRPC/webhook streams.
- Validation: on stream open the control plane calls `TokenReview` with the presented token and verifies the audience and expiry. The validated identity is bound to the execution ledger entry.
- Lifecycle: projected tokens are auto-rotated by kubelet and expire after `expirationSeconds` (default 600 s). No Secret objects to create or clean up.
- Optional stronger auth: short-lived mTLS client certificates (CSR flow) for higher assurance.

Rationale: projected SA tokens leverage Kubernetes-native issuance and rotation, avoid Secret churn, and integrate with `TokenReview` for validation.

## Operational / Audit considerations

- Enable Kubernetes API server audit logging to capture Job/pod lifecycle events and controller actions.
- Enable object-store access logs for artifact upload/download auditing.
- Emit Kubernetes `Event` objects and update `status.executions[]` for human-friendly tracing in `kubectl`.

## Implementation plan (phased)

1. CRD & validation: define `HibernatePlan` CRD, implement validation webhooks (DAG acyclicity, maxConcurrency).
2. Controller core: schedule evaluation, plan building, dependency resolution, status ledger mechanics.
3. Runner Job prototype: simple runner image that calls a mock executor and writes restore JSON to object store and stdout logs.
4. Default/simple dispatch: implement Job creation, ephemeral SA/Secret creation + cleanup, controller log collection, artifact persistence, status updates.
5. Tests: unit tests for DAG validation; envtest/integration tests for Job lifecycle, status ledger, and wake-up path.
6. Optional: streaming gRPC auth and server, TokenReview or CSR-based client cert issuance.

## Implementation Status

Last updated: 2026-01-29

### Completed (MVP Phase 1)

| Component | File(s) | Notes |
|-----------|---------|-------|
| Project scaffolding | `go.mod`, `Makefile`, `Dockerfile` | Kubernetes 1.34, aws-sdk-go-v2 v1.34.0 |
| CRD types | `api/v1alpha1/*.go` | HibernatePlan, CloudProvider, K8SCluster with kubebuilder markers |
| Scheduler/planner | `internal/scheduler/planner.go` | All 4 strategies: Sequential, Parallel, DAG (Kahn's algorithm), Staged |
| Scheduler tests | `internal/scheduler/planner_test.go` | Cycle detection, unknown target validation, diamond DAG |
| EKS executor | `internal/executor/eks/eks.go` | ManagedNodeGroups scale to zero, restore state tracking, Karpenter placeholder |
| RDS executor | `internal/executor/rds/rds.go` | Instance/cluster stop, optional snapshot before stop, start logic |
| EC2 executor | `internal/executor/ec2/ec2.go` | Tag-based selector, instance ID support, stop/start running instances |
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
| Runner SA manager | `internal/controller/serviceaccount.go` | Per-plan ServiceAccount with IRSA/workload identity annotations |
| Integration tests | `internal/controller/hibernateplan_controller_test.go` | envtest-based, restore manager, schedule evaluation |
| Webhook manifests | `config/webhook/webhook.yaml` | ValidatingWebhookConfiguration, cert-manager integration |
| Runner RBAC | `config/rbac/runner_role.yaml` | ClusterRole for runner pods |

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
| Runner streaming integration | `cmd/runner/main.go` | Progress reporting, completion handling, error streaming |

### Next Steps (P2/P3)

| Priority | Task | Description |
|----------|------|-------------|
| P2 | Karpenter executor | NodePool scale to zero, provisioner pause |
| P2 | Consume restore data on wake-up | Load RestoreData in runner during WakeUp operation |
| P3 | GCP/Azure executors | GKE, Cloud SQL, Compute Engine; AKS, Azure SQL |
| P3 | Metrics & observability | Prometheus metrics, structured logging, trace context |
| P3 | Error recovery logic | Automatic retry/recovery from PhaseError state |

### Known Gaps (Further Reduced)

- **Restore flow consumption**: RestoreData saved but wake-up flow doesn't yet load it
- **Error recovery**: PhaseError state exists but no automatic retry/recovery logic
- **Artifact storage**: Only ConfigMap supported; object-store integration pending

## Alternatives considered

- CronJob-like parallelism per target: simpler but insufficient for dependency enforcement, centralised restore metadata, and safe sequencing — not recommended for coordinated multi-target hibernation.
- Fully push-based heavy streaming (gRPC only): more complex auth and cert management; recommended as optional enhancement when low-latency is required.

## Drawbacks

- More moving parts than a single script (controller, runner image, object-store). Requires RBAC and audit configuration.
- Running a Job per target increases resource churn; bounded concurrency mitigates scale.

## Unresolved questions

- Best defaults for artifact retention and garbage collection across cloud providers (S3 retention vs ConfigMap vs PVC).
- Policy for preserving ConfigMaps vs moving large artifacts to object-store automatically.

## Appendix — examples

- See `WORKPLAN.md` for example `HibernatePlan` YAML, Job template, and staged execution samples.

## Links

- Workplan: `WORKPLAN.md`
- Agent guidelines: `AGENTS.md`
