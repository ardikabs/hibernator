# Connector Reconciler Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reconcile `CloudProvider` and `K8SCluster` status by running a probe Job as the runner identity that verifies authentication and, via IAM policy simulation / Kubernetes access review, which executors the connector may run.

**Architecture:** A `ConnectorReconciler` in the controller creates one short-lived probe Job per connector (runner image and ServiceAccount, controller namespace). The runner gains a `probe` operation that calls a new `internal/connectorprobe` package and writes a JSON `Result` to `/dev/termination-log`. The reconciler reads it and writes status through the existing status-writer pipeline. Executors declare access needs through an optional `ConnectorRequirer` interface; shared AWS and K8s checkers do the verification.

**Tech Stack:** Go, controller-runtime, aws-sdk-go-v2 (sts, iam), client-go (`SelfSubjectAccessReview`), existing `pkg/awsutil`, `pkg/k8sutil`, `internal/provider/processor/status`.

**Spec:** `docs/superpowers/specs/2026-11-19-connector-reconciler-design.md`

## Global Constraints

- Never commit, and never run `test/e2e/...`; both need an explicit user request (`AGENTS.md`). Each task ends at a checkpoint, not a commit.
- Go binaries build to `bin/`; use `make build` or `go build -o bin/{name}`.
- Run `make generate` after any `api/` change; it syncs CRDs to `charts/hibernator/crds/`.
- Probe Job: namespace = controller namespace (`ControlPlaneNamespace`), `serviceAccountName` = runner SA, `backoffLimit: 0`, short `ttlSecondsAfterFinished`, `restartPolicy: Never`, no stream-token volume.
- Defaults: resync `15m` (with jitter), max concurrent probe Jobs `5`, probe timeout `30s`.
- Executor access: both shutdown and wakeup requirements must pass for `allowed: true`.
- When simulation is not possible: connector stays `ready`; `allowedExecutors` and `executorAccess` stay empty; condition `ExecutorAccessVerified=False`, reason `SimulationUnavailable`. Never write `allowed: false` for it.
- Status never gains an entry for an executor that was not actually checked.
- `AWS_ROLE_ARN` is read only when the effective credentials are IRSA, never when static credentials are configured.
- `iam:GetRole` is not used.
- Un-runnable probe: `Ready=Unknown`, reason `ProbeFailed`.
- Account mismatch between STS identity and `spec.aws.accountId`: `Ready=False`, reason `AccountMismatch`.
- Match surrounding code style: Apache header block, `hibernatorv1alpha1` import alias, table tests.

## Review Focus

- Probe result larger than the 4096-byte termination-message limit (many executors, long messages): output must still be valid JSON, truncated, never cut mid-document.
- Static credentials configured while the runner pod has `AWS_ROLE_ARN` set: the principal must come from `GetCallerIdentity`, not the env var.
- Probe Job unschedulable, evicted, or finished with an empty termination message: `Ready=Unknown`, retried only after the resync interval, no Job-creation hot loop.
- Connector deleted (or never found) while a probe Job exists: the Job is cleaned up by label, since a cross-namespace owner reference is not possible.
- `K8SCluster` of type eks whose `providerRef` is missing, points at a missing `CloudProvider`, or at one that is not `Ready`: no Job is created, and the reason is `ProviderNotReady`.

---

## File Structure

| File | Responsibility |
|---|---|
| `api/v1alpha1/shared.go` (modify) | `KindOf` knows the two connector kinds |
| `api/v1alpha1/connector_status.go` (create) | `ExecutorAccess`, condition/reason/verification constants |
| `api/v1alpha1/cloudprovider_types.go`, `k8scluster_types.go` (modify) | new status fields |
| `internal/executor/access.go` (create) | `AccessRequirement`, `ConnectorRequirer` |
| `internal/executor/{ec2,rds,eks,karpenter,workloadscaler}/*.go` (modify) | `Requires()` |
| `internal/connectorprobe/result.go` (create) | `Result`, bounded marshal |
| `internal/connectorprobe/aws_auth.go`, `aws_access.go` (create) | STS auth, principal resolution, simulation |
| `internal/connectorprobe/k8s.go` (create) | version check, `SelfSubjectAccessReview` |
| `internal/connectorprobe/prober.go` (create) | orchestrates the two steps |
| `cmd/runner/app/probe.go` (create), `app.go` (modify) | `probe` operation |
| `internal/provider/connector/` (create) | probe Job builder, status mapping, reconciler |
| `internal/provider/setup.go`, `status/types.go`, `cmd/controller/app/app.go` (modify) | wiring, flags |
| `charts/hibernator/templates/rbac/clusterrole.yaml`, `website/docs/...` (modify) | RBAC and docs |

---

### Task 1: Connector status API types

**Files:**
- Create: `api/v1alpha1/connector_status.go`
- Modify: `api/v1alpha1/cloudprovider_types.go`, `api/v1alpha1/k8scluster_types.go`, `api/v1alpha1/shared.go` (`KindOf`)
- Test: `api/v1alpha1/types_test.go`, `api/v1alpha1/deepcopy_test.go`

**Interfaces:**
- Produces:
  - `type ExecutorAccess struct { Type string; Allowed bool; Reason string; Message string; Verification ExecutorVerification }` (json: `type`, `allowed`, `reason,omitempty`, `message,omitempty`, `verification,omitempty`)
  - `type ExecutorVerification string` with consts `VerificationPolicySimulation = "PolicySimulation"`, `VerificationAccessReview = "AccessReview"`
  - Condition types: `ConditionReady = "Ready"`, `ConditionAuthValid = "AuthValid"`, `ConditionExecutorAccessVerified = "ExecutorAccessVerified"`
  - Reason consts: `ReasonProbeFailed`, `ReasonSecretNotFound`, `ReasonInvalidCredentials`, `ReasonAccountMismatch`, `ReasonClusterUnreachable`, `ReasonProviderNotReady`, `ReasonAccessDenied`, `ReasonSimulationUnavailable`, `ReasonProbeError` (string values equal the names without `Reason`)
  - On both `CloudProviderStatus` and `K8SClusterStatus`: `ObservedGeneration int64`, `LastProbeTime *metav1.Time`, `ObservedSecretVersion string`, `Conditions []metav1.Condition` (`+listType=map`, `+listMapKey=type`), `AllowedExecutors []string`, `ExecutorAccess []ExecutorAccess`
  - `KindOf(*CloudProvider)` returns `"CloudProvider"`, `KindOf(*K8SCluster)` returns `"K8SCluster"`

- [ ] **Step 1: Write failing tests** in `types_test.go` (JSON round trip of both statuses with every new field, asserting keys `allowedExecutors`, `executorAccess`, `observedGeneration`, `lastProbeTime`, `observedSecretVersion`, `conditions`) and in `deepcopy_test.go` (mutating a copy's `AllowedExecutors`/`ExecutorAccess`/`Conditions` does not touch the original). Add a `KindOf` test for both new kinds.
- [ ] **Step 2: Run** `make test-pkg PKG=./api/...`. Expected: FAIL (undefined fields/types).
- [ ] **Step 3: Implement** the types above. Add kubebuilder print columns `Executors` (`.status.allowedExecutors`) is not required; keep existing `Ready` column. Extend `KindOf`.
- [ ] **Step 4: Run** `make generate` then `make test-pkg PKG=./api/...`. Expected: PASS; `git status` shows regenerated `zz_generated.deepcopy.go`, `config/crd/bases/*`, `charts/hibernator/crds/*`.
- [ ] **Step 5: Checkpoint** for user review (no commit).

---

### Task 2: Executor access declarations

**Files:**
- Create: `internal/executor/access.go`
- Modify: `internal/executor/ec2/ec2.go`, `rds/rds.go`, `eks/eks.go`, `karpenter/karpenter.go`, `workloadscaler/workloadscaler.go`
- Test: `internal/executor/access_test.go`, one `Requires()` test per executor package

**Interfaces:**
- Produces:
  - `const ConnectorKindCloudProvider = "CloudProvider"`, `ConnectorKindK8SCluster = "K8SCluster"`
  - `type K8SRule struct { Group, Resource, Subresource string; Verbs []string }`
  - `type OperationAccess struct { AWSActions []string; K8SRules []K8SRule }`
  - `type AccessRequirement struct { ConnectorKind string; Shutdown, WakeUp OperationAccess }`
  - `type ConnectorRequirer interface { Requires() AccessRequirement }`
  - Helper `func (r AccessRequirement) Validate() error`: errors on empty `ConnectorKind`, or on AWS actions given for a K8SCluster kind or K8S rules for a CloudProvider kind.

- [ ] **Step 1: Write failing tests.** `TestAccessRequirementValidate` (table: valid AWS, valid K8s, empty kind, mixed). Per executor, `TestExecutor_Requires` asserting: `ec2` kind CloudProvider, Shutdown contains `ec2:DescribeInstances` and `ec2:StopInstances`, WakeUp contains `ec2:StartInstances`; `rds` Shutdown contains `rds:StopDBInstance` and `rds:StopDBCluster`, WakeUp contains `rds:StartDBInstance` and `rds:StartDBCluster`; `eks` kind CloudProvider, Shutdown contains `eks:UpdateNodegroupConfig`; `karpenter` kind K8SCluster with a rule on group `karpenter.sh` resource `nodepools`; `workloadscaler` kind K8SCluster with a rule on resource `deployments` subresource `scale`. Every executor's `Requires().Validate()` returns nil.
- [ ] **Step 2: Run** `make test-pkg PKG=./internal/executor/...`. Expected: FAIL.
- [ ] **Step 3: Implement** `access.go`, then `func (e *Executor) Requires() executor.AccessRequirement` on each executor above. The asserted entries are minimums; derive the complete lists from the API calls each executor's client actually makes (read the executor's `client.go`), and keep Shutdown/WakeUp separate. Add the compile-time assertion `var _ executor.ConnectorRequirer = (*Executor)(nil)` in each.
- [ ] **Step 4: Run** `make test-pkg PKG=./internal/executor/...`. Expected: PASS.
- [ ] **Step 5: Checkpoint.** `noop`, `gke`, `cloudsql` intentionally do not implement it.

---

### Task 3: Probe result and bounded termination message

**Files:**
- Create: `internal/connectorprobe/result.go`
- Test: `internal/connectorprobe/result_test.go`

**Interfaces:**
- Consumes: `hibernatorv1alpha1.ExecutorAccess` (Task 1)
- Produces:
  - `const MaxTerminationBytes = 4096`
  - `type Result struct { Ready bool; Reason string; Message string; AccessVerified bool; AccessReason string; AccessMessage string; ExecutorAccess []hibernatorv1alpha1.ExecutorAccess }` (json tags: lower camel case, `omitempty` on the strings and slice)
  - `func (r Result) Marshal() string` returns valid JSON of at most `MaxTerminationBytes`
  - `func Unmarshal(s string) (Result, error)` returns an error on empty or invalid input

- [ ] **Step 1: Write failing tests.** `TestResultMarshal_RoundTrip`; `TestResultMarshal_TruncatesOversize` (60 executor entries with 200-char messages: `len(Marshal()) <= 4096`, output is valid JSON, `Ready` and `Reason` survive); `TestUnmarshal_Empty` returns an error.
- [ ] **Step 2: Run** `make test-pkg PKG=./internal/connectorprobe/...`. Expected: FAIL.
- [ ] **Step 3: Implement.** To fit the limit, shorten each `Message` first, then drop trailing `ExecutorAccess` entries; set `AccessMessage` to note the truncation. Never slice the encoded bytes.
- [ ] **Step 4: Run** the same command. Expected: PASS.
- [ ] **Step 5: Checkpoint.**

---

### Task 4: AWS authentication check

**Files:**
- Create: `internal/connectorprobe/aws_auth.go`
- Test: `internal/connectorprobe/aws_auth_test.go`

**Interfaces:**
- Consumes: `awsutil.BuildAWSConfig(ctx, *AWSConnectorConfig) (aws.Config, error)`
- Produces:
  - `type STSAPI interface { GetCallerIdentity(ctx, *sts.GetCallerIdentityInput, ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) }`
  - `type AWSChecker struct { BuildConfig func(ctx, *awsutil.AWSConnectorConfig) (aws.Config, error); NewSTS func(aws.Config) STSAPI; NewIAM func(aws.Config) IAMAPI; Getenv func(string) string }` with a `NewAWSChecker()` that wires the real SDK and `os.Getenv`
  - `type AWSIdentity struct { AccountID, ARN string; Config aws.Config }`
  - `func (c *AWSChecker) CheckAuth(ctx, cfg *awsutil.AWSConnectorConfig) (*AWSIdentity, *AuthError)`
  - `type AuthError struct { Reason, Message string }` (`Reason` is one of the Task 1 reason strings)

- [ ] **Step 1: Write failing tests** with a fake `STSAPI`: success returns identity; STS error returns `AuthError{Reason: "InvalidCredentials"}`; returned account differing from `cfg.AccountID` returns `Reason: "AccountMismatch"` and the message names both accounts; empty `cfg.AccountID` skips the comparison.
- [ ] **Step 2: Run** `make test-pkg PKG=./internal/connectorprobe/...`. Expected: FAIL.
- [ ] **Step 3: Implement** `CheckAuth` using `BuildConfig` then `GetCallerIdentity` (this is step 1 of validation; assume-role is already applied by `BuildAWSConfig`).
- [ ] **Step 4: Run** the same command. Expected: PASS.
- [ ] **Step 5: Checkpoint.**

---

### Task 5: AWS access check (policy simulation)

**Files:**
- Create: `internal/connectorprobe/aws_access.go`
- Test: `internal/connectorprobe/aws_access_test.go`

**Interfaces:**
- Consumes: `AWSChecker`, `AWSIdentity` (Task 4); `executor.AccessRequirement` (Task 2)
- Produces:
  - `type IAMAPI interface { SimulatePrincipalPolicy(...); GetContextKeysForPrincipalPolicy(...) }` (SDK signatures)
  - `func ResolvePrincipalARN(cfg *awsutil.AWSConnectorConfig, identityARN string, getenv func(string) string) (string, error)`:
    - `cfg.AssumeRoleArn != ""` returns it.
    - Static credentials (`cfg.AccessKeyID != ""`) return `identityARN`, never reading the env var.
    - Otherwise return `getenv("AWS_ROLE_ARN")`, or an error if empty (EKS Pod Identity).
  - `var ErrSimulationUnavailable = errors.New(...)`
  - `func (c *AWSChecker) CheckAccess(ctx, id *AWSIdentity, principalARN string, execs map[string]executor.AccessRequirement) ([]hibernatorv1alpha1.ExecutorAccess, error)`: returns `ErrSimulationUnavailable` (wrapped) when the first simulate call is denied or the principal is unresolved.

- [ ] **Step 1: Write failing tests** with a fake `IAMAPI`:
  - `TestResolvePrincipalARN` table: assume role wins over everything; static credentials ignore `AWS_ROLE_ARN`; IRSA reads `AWS_ROLE_ARN`; empty env returns an error.
  - `TestCheckAccess_AllAllowed`: executor allowed, `Verification == "PolicySimulation"`.
  - `TestCheckAccess_ShutdownOnlyDenied`: `Allowed == false`, `Reason == "AccessDenied"`, message names the denied action and says `shutdown`.
  - `TestCheckAccess_SimulateAccessDenied`: returns an error matching `errors.Is(err, ErrSimulationUnavailable)` and no entries.
  - `TestCheckAccess_UsesContextKeys`: `GetContextKeysForPrincipalPolicy` is called once per principal.
- [ ] **Step 2: Run** `make test-pkg PKG=./internal/connectorprobe/...`. Expected: FAIL.
- [ ] **Step 3: Implement.** One simulate call per executor per operation, `ResourceArns: ["*"]`. Both operations must be allowed for `Allowed: true`. Condition-context-key handling is the builder's call; the minimum is to avoid reporting a denial caused only by missing context values (treat those results as not verified, with no entry).
- [ ] **Step 4: Run** the same command. Expected: PASS.
- [ ] **Step 5: Checkpoint.**

---

### Task 6: Kubernetes authentication and access check

**Files:**
- Create: `internal/connectorprobe/k8s.go`
- Test: `internal/connectorprobe/k8s_test.go`

**Interfaces:**
- Consumes: `k8sutil.BuildClients(ctx, *K8SConnectorConfig) (dynamic.Interface, kubernetes.Interface, error)`
- Produces:
  - `type K8SChecker struct { BuildClients func(ctx, *k8sutil.K8SConnectorConfig) (dynamic.Interface, kubernetes.Interface, error) }`, with `NewK8SChecker()`
  - `func (c *K8SChecker) CheckAuth(ctx, cfg *k8sutil.K8SConnectorConfig) (kubernetes.Interface, *AuthError)`: builds clients, calls `Discovery().ServerVersion()`, and returns `Reason: "ClusterUnreachable"` on failure
  - `func (c *K8SChecker) CheckAccess(ctx, cs kubernetes.Interface, execs map[string]executor.AccessRequirement) []hibernatorv1alpha1.ExecutorAccess`: uses `SelfSubjectAccessReview` per rule and verb, `Verification == "AccessReview"`

- [ ] **Step 1: Write failing tests** with `fake.NewSimpleClientset` and a `PrependReactor("create", "selfsubjectaccessreviews", ...)`: all allowed returns `Allowed: true`; one denied verb returns `Allowed: false`, `Reason: "AccessDenied"`, and the message names the group, resource and verb. `TestCheckAuth_Unreachable` uses a `BuildClients` stub that errors and expects `ClusterUnreachable`.
- [ ] **Step 2: Run** `make test-pkg PKG=./internal/connectorprobe/...`. Expected: FAIL.
- [ ] **Step 3: Implement.** A failed SSAR call itself (not a denial) yields a `ProbeError` entry only if it is clearly a transport failure; otherwise skip the entry.
- [ ] **Step 4: Run** the same command. Expected: PASS.
- [ ] **Step 5: Checkpoint.**

---

### Task 7: Prober orchestration

**Files:**
- Create: `internal/connectorprobe/prober.go`
- Test: `internal/connectorprobe/prober_test.go`

**Interfaces:**
- Consumes: `AWSChecker`, `K8SChecker`, `Result`, `executor.Executor`, `executor.ConnectorRequirer`, `executor.ConnectorConfig`
- Produces:
  - `type Prober struct { AWS *AWSChecker; K8S *K8SChecker }`
  - `func (p *Prober) Probe(ctx context.Context, kind string, cfg executor.ConnectorConfig, execs []executor.Executor) Result`

- [ ] **Step 1: Write failing tests** with stub checkers:
  - Step-1 failure returns `Ready: false`, the `AuthError` reason, and does not call access checks.
  - Step-1 success with `ErrSimulationUnavailable` returns `Ready: true`, `AccessVerified: false`, `AccessReason: "SimulationUnavailable"`, and an empty `ExecutorAccess`.
  - Success returns entries for only the executors that implement `ConnectorRequirer` and match `kind`; an executor without `Requires()` and one with the other kind are absent.
  - An invalid `AccessRequirement` (fails `Validate`) is skipped with no entry.
- [ ] **Step 2: Run** `make test-pkg PKG=./internal/connectorprobe/...`. Expected: FAIL.
- [ ] **Step 3: Implement.** CloudProvider: `CheckAuth`, then `ResolvePrincipalARN`, then `CheckAccess`. K8SCluster: `CheckAuth`, then `CheckAccess`. The message for the not-verified case says to grant `iam:SimulatePrincipalPolicy` and `iam:GetContextKeysForPrincipalPolicy` to the connector's identity (AWS only).
- [ ] **Step 4: Run** the same command. Expected: PASS.
- [ ] **Step 5: Checkpoint.**

---

### Task 8: Runner probe operation

**Files:**
- Create: `cmd/runner/app/probe.go`
- Modify: `cmd/runner/app/app.go` (`Run` branch on `cfg.Operation == "probe"`; update the flag help text)
- Test: `cmd/runner/app/probe_test.go`

**Interfaces:**
- Consumes: `metadata.ConfigBuilder.BuildConnectorConfig`, `connectorprobe.Prober.Probe`, `connectorprobe.Result.Marshal`, the executor factory registry
- Produces:
  - `func runProbe(ctx, log logr.Logger, cfg *Config, builder connectorConfigBuilder, prober connectorProber, execs []executor.Executor) (connectorprobe.Result, error)` where the two small interfaces wrap the concrete types for testing
  - `func writeProbeTerminationLog(log logr.Logger, res connectorprobe.Result)`: writes `res.Marshal()` to the same termination path used by `writeTerminationLog`

- [ ] **Step 1: Write failing tests.**
  - `TestRunProbe_BuildConfigNotFound`: the builder returns a not-found error, so `Ready: false`, `Reason: "SecretNotFound"` for a Secret miss (or the matching reason for a missing connector), and a nil error.
  - `TestRunProbe_PassesEnabledExecutors`: the prober receives only executors in the registry.
  - `TestRunProbe_UnexpectedFailureReturnsError`: when the result cannot be produced, the error is non-nil, so the process exits non-zero.
  - `TestParseFlags_ProbeNeedsNoTarget` or equivalent: probe mode does not require `--target`, `--plan`, or a stream token.
- [ ] **Step 2: Run** `make test-pkg PKG=./cmd/runner/...`. Expected: FAIL.
- [ ] **Step 3: Implement.** Probe mode skips telemetry and streaming entirely; it still builds the k8s client and executor registry the way `newRunner` does, but stops before the execute path. A completed check (valid or not) exits 0.
- [ ] **Step 4: Run** `make test-pkg PKG=./cmd/runner/...`. Expected: PASS. Also run `go build -o bin/runner ./cmd/runner`.
- [ ] **Step 5: Checkpoint.**

---

### Task 9: Probe Job builder and status mapping

**Files:**
- Create: `internal/provider/connector/job.go`, `internal/provider/connector/status.go`
- Modify: `internal/wellknown/labels.go` (add `LabelConnectorKind`, `LabelConnectorName`, `LabelConnectorProbe`, all under the `hibernator.ardikabs.com/` prefix)
- Test: `internal/provider/connector/job_test.go`, `status_test.go`

**Interfaces:**
- Consumes: `connectorprobe.Result`, `connectorprobe.Unmarshal`, Task 1 types, `status.Mutator`
- Produces:
  - `type JobConfig struct { Namespace, RunnerImage, RunnerServiceAccount string; Timeout time.Duration }`
  - `func BuildProbeJob(cfg JobConfig, kind, namespace, name string) *batchv1.Job`
  - `func ProbeJobSelector(kind, namespace, name string) client.MatchingLabels`
  - `func ApplyResult(res connectorprobe.Result, now metav1.Time, generation int64, secretVersion string) func(*hibernatorv1alpha1.CloudProvider)` and the equivalent for `K8SCluster` (`clusterType` is derived from spec, not the result); both set `ready`, `message`, `lastValidated`, `lastProbeTime`, `observedGeneration`, `observedSecretVersion`, `Ready`/`AuthValid`/`ExecutorAccessVerified` conditions, `allowedExecutors`, `executorAccess`
  - `func ApplyProbeFailed(now metav1.Time, generation int64, msg string) ...`: `Ready=Unknown`, reason `ProbeFailed`, sets `lastProbeTime`, leaves `lastValidated` and the executor fields untouched
  - `func ProbeDue(lastProbe *metav1.Time, observedGen, gen int64, observedSecret, secret string, resync time.Duration, now time.Time, jitter func() time.Duration) bool`

- [ ] **Step 1: Write failing tests.**
  - Job: args are `--operation probe` and `--timeout <timeout>`; env has the three `HIBERNATOR_CONNECTOR_*` vars and `POD_NAMESPACE`; SA, image and namespace come from `JobConfig`; `BackoffLimit == 0`; labels include kind and name; no volumes.
  - Status: a Ready result with entries populates `allowedExecutors` with only `Allowed` ones and keeps all entries in `executorAccess`; a not-verified result leaves both empty and sets `ExecutorAccessVerified=False`/`SimulationUnavailable`; `Ready: false` clears `allowedExecutors`; `ApplyProbeFailed` sets `Unknown` and preserves the previous `lastValidated`.
  - `ProbeDue` table: never probed; generation changed; secret version changed; within resync (false); past resync plus jitter (true).
- [ ] **Step 2: Run** `make test-pkg PKG=./internal/provider/connector/...`. Expected: FAIL.
- [ ] **Step 3: Implement** the signatures above. Job names use `GenerateName: "connector-probe-"` with `ShortenName` from `pkg/k8sutil`.
- [ ] **Step 4: Run** the same command. Expected: PASS.
- [ ] **Step 5: Checkpoint.**

---

### Task 10: Connector reconciler, wiring and flags

**Files:**
- Create: `internal/provider/connector/reconciler.go`
- Modify: `internal/provider/processor/status/types.go` (add `CloudProviderStatuses`, `K8SClusterStatuses`), `internal/provider/setup.go` (add `ProbeOptions` to `ProviderOptions`, register two status processors and the reconcilers), `cmd/controller/app/app.go` (flags, plus `ControlPlaneNamespace` passed through)
- Test: `internal/provider/connector/reconciler_test.go`

**Interfaces:**
- Consumes: everything from Task 9; `statusprocessor.Updater[T]`
- Produces:
  - `type Reconciler struct { Client, APIReader client.Reader/Client; Clock clock.Clock; Log logr.Logger; Statuses statusprocessor.ControllerStatuses; Job JobConfig; Resync time.Duration; MaxConcurrentProbes int }`
  - `func (r *Reconciler) SetupWithManager(mgr ctrl.Manager, workers int) error` registers one controller per kind with watches on referenced Secrets (mapping function to connectors) and, for `K8SCluster`, on `CloudProvider` (mapping to referencing clusters)
  - `type ProbeOptions struct { Resync time.Duration; MaxConcurrent int; Timeout time.Duration }`
  - Flags: `--connector-resync-interval` (default `15m`, env `CONNECTOR_RESYNC_INTERVAL`), `--connector-max-concurrent-probes` (default `5`), `--connector-probe-timeout` (default `30s`)

- [ ] **Step 1: Write failing tests** with a fake client and a recording `Updater`:
  - Due connector with no Job creates exactly one Job; a second reconcile creates none while it runs.
  - Finished Job with a valid termination message results in `ApplyResult` being sent and the Job deleted.
  - Finished Job with an empty termination message sends `ApplyProbeFailed`; the next reconcile within `Resync` creates no Job.
  - Running Jobs at the cap result in no new Job and a short requeue.
  - Connector NotFound deletes its probe Jobs by label.
  - eks `K8SCluster` with a missing or not-Ready `CloudProvider` sends `Ready=False`/`ProviderNotReady` and creates no Job.
  - `clusterType` on `K8SCluster` status is derived from `spec.eks`/`spec.gke`/`spec.k8s`.
  - Secret-change mapping function returns the connectors referencing that Secret.
- [ ] **Step 2: Run** `make test-pkg PKG=./internal/provider/...`. Expected: FAIL.
- [ ] **Step 3: Implement.** Reconcile flow: get connector, then clean up on NotFound, then provider precondition, then find the latest probe Job by `ProbeJobSelector`, then consume a finished Job, else create one if `ProbeDue` and under the cap, else `RequeueAfter` the remaining time. Reading the termination message reuses the logic of `getTerminationMessageFromPod` in `plan/state/state_execution.go`; extract a shared helper only if that stays a small, safe change, otherwise duplicate it narrowly. Probe Jobs do not use owner references.
- [ ] **Step 4: Run** `make test-pkg PKG=./internal/provider/...` and `go build -o bin/controller ./cmd/controller`. Expected: PASS and a clean build.
- [ ] **Step 5: Checkpoint.** Ask the user before any e2e or kind run.

---

### Task 11: Chart RBAC, docs and verification

**Files:**
- Modify: `charts/hibernator/templates/rbac/clusterrole.yaml`, `charts/hibernator/values.yaml` (only if a new flag needs a value), `website/docs/user-guides/troubleshooting.md`, plus a new `website/docs/user-guides/connectors.md` (and the nav entry)
- Test: `make verify`

**Interfaces:**
- Consumes: the flags from Task 10, the status shape from Task 1

- [ ] **Step 1: Add RBAC.** Controller ClusterRole gains `cloudproviders/status` and `k8sclusters/status` with `get`, `patch`, `update`. Jobs and pods rules already exist; do not duplicate them. Runner needs no new rule: `SelfSubjectAccessReview` is covered by the default `system:basic-user` binding; confirm with the user's kind cluster before adding one.
- [ ] **Step 2: Write docs.** The connectors page covers: what `ready`, `allowedExecutors` and `executorAccess` mean; `reason` values; the `ExecutorAccessVerified` condition; the two optional IAM permissions on the identity the connector authenticates as (the assumed role when `assumeRoleArn` is set; the runner IRSA role or static user otherwise); the EKS Pod Identity limitation; and the per-target resource-scope limit. Add a troubleshooting entry linking to it.
- [ ] **Step 3: Run** `make generate` then `make verify` then `make test-unit`. Expected: no diff after generate (apart from the intended changes already staged for review), and all unit tests pass.
- [ ] **Step 4: Checkpoint.** Hand the branch to the user for review; convert tasks to a Beads epic with child tasks (`epic:connector-reconciler` label, acceptance criteria via `--acceptance`) per `AGENTS.md`.
