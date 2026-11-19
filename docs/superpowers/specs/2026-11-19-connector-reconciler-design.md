# Connector Reconciler (CloudProvider / K8SCluster Readiness) — Design

- Date: 2026-11-19
- Status: draft (awaiting approval)
- Component: `api/v1alpha1`, `internal/executor`, `internal/provider`, `cmd/runner`, `charts/hibernator`
- Related: `cmd/runner/metadata/builder.go`, `pkg/awsutil`, `pkg/k8sutil`, `internal/provider/processor/plan/state/state_execution.go`, `cmd/runner/app/factory.go`

## Intent and why

`CloudProvider` and `K8SCluster` (together: *connectors*) are used today only as
metadata that runner Jobs read at execution time. They already carry a status
(`ready`, `message`, `lastValidated`; `clusterType` on K8SCluster) but nothing
writes it, so `kubectl get` shows an empty Ready column and operators only learn a
connector is broken when a plan execution fails.

We want a connector reconciler that **verifies the connector's authentication
works** and **which executors the connector is allowed to run**, and reports both
in status, so an operator can see what is wrong beneath a failing plan.

## Goals

- Each connector's status says whether its configured authentication works, with a
  human-readable reason on failure.
- Each connector's status lists which executors it is allowed to run, and why any
  are not.
- The check is exact for every auth method, including IRSA.
- Re-validate on spec/Secret change and periodically.
- `kubectl get cloudproviders,k8sclusters` shows meaningful Ready state.

## Non-goals (this milestone)

- No effect on `HibernatePlan` or `ScheduleException` behaviour. Nothing reads
  connector status for decisions yet. (Next milestone: hold/pause hibernation when
  a connector is unavailable or its target's executor is not in `allowedExecutors`.)
- No per-target resource-scope checks. A role limited to specific resource ARNs may
  pass the connector-level check and still fail on a given target. Documented as a
  known limit.
- No dry-run based verification (e.g. EC2 `DryRun`). Possible later upgrade.
- No notifications beyond standard K8s events.

## Anti-goals

- A result that disagrees with what a real runner execution would experience
  (false ready / false not-ready).
- Giving the controller credentials or cloud identity it does not have today.
- A misleading "everything denied" when the real cause is a missing simulate
  permission.

## Constraints

- IRSA is bound only to the **runner ServiceAccount**
  (`charts/hibernator/templates/rbac/runner-serviceaccount.yaml`). The controller
  identity cannot validate IRSA, so probing in-controller is rejected.
- The runner SA exists only in the release/controller namespace.
- The runner IRSA role is typically a basic role used mainly for `sts:AssumeRole`
  into the role in `spec.aws.assumeRoleArn`. The runner role itself is not asked for
  any new permission. Policy simulation runs with the credentials the connector
  authenticates as (the assumed role in the target account when `assumeRoleArn` is
  set), and that identity simulates itself (user decision).
- Runner ClusterRole currently grants only `get` on connectors/secrets, plus
  configmaps.
- Reconcilers follow the provider/processor/status-writer pipeline in
  `internal/provider/setup.go`.

## Approaches considered

Where the probe runs:

| | Trade-off |
|---|---|
| A. Probe inside the controller using `pkg/awsutil` / `pkg/k8sutil` | Cheap, fast. Wrong for IRSA (tests the controller identity). **Rejected.** |
| **B. Short-lived probe Job running as the runner SA (chosen)** | Exact for all auth methods; one code path. Costs a pod per probe, a new runner mode, and Job RBAC. |
| C. A for static/kubeconfig, B only for IRSA | Two code paths and inconsistent semantics. Rejected. |

How executor access is verified:

| | Trade-off |
|---|---|
| Per-executor probe method (dry-run / read-only / simulate, chosen by each executor) | Most accurate per API, but every executor writes and tests probe logic; uneven guarantees. Rejected. |
| **Declarative requirements + shared checkers (chosen)** | Executors declare what access they need; shared AWS (policy simulation) and K8s (access review) helpers do the check. Uniform, no per-executor probe code, out-of-tree executors get it free. Costs an extra IAM permission and is less exact than a real dry-run. |

## Design

### Flow

1. A `ConnectorReconciler` (shared logic for both kinds) watches `CloudProvider` and
   `K8SCluster`, and the Secrets they reference.
2. When a probe is due (generation changed, referenced Secret changed, resync
   interval elapsed, or never probed), it creates a probe Job in the controller's
   namespace using the runner image and runner SA.
3. The runner gains a probe operation that validates in two steps:
   1. **Authentication.** Call the existing `ConfigBuilder.BuildConnectorConfig`
      and verify the runner can authenticate as the connector specifies, including
      assuming `assumeRoleArn` if set. This decides `ready`.
   2. **Executor access.** Only if step 1 passed, reuse those credentials (the
      assumed role, in the target account, when there is one) to check the access
      requirements of every enabled executor that targets this connector kind.
      If this step is not possible, the connector is still reported `ready`, with
      no executor entries (see below).

   The runner writes a JSON result to `/dev/termination-log`. It exits 0 when the check
   completed (valid or not) and non-zero only when the probe could not run.
4. The reconciler reads the pod termination message (same mechanism as
   `getTerminationMessageFromPod`) and writes status through the status-writer
   pipeline.

### Authentication check

- **CloudProvider (aws):** build AWS config as the runner does (static, IRSA,
  optional assume-role), call `sts:GetCallerIdentity`, verify the returned account
  equals `spec.aws.accountId`.
- **K8SCluster:**
  - `inCluster`: build clients and call the server version endpoint.
  - `kubeconfigRef`: resolve Secret, build clients, call version endpoint.
  - `eks`: resolve `providerRef` credentials, mint the EKS token via
    `pkg/k8sutil`, call version endpoint. If the referenced `CloudProvider` is not
    Ready, report that as the reason instead of probing (`Ready=Unknown` when the
    provider's own Ready condition is Unknown or unset).
  - `gke`: not probed. `pkg/k8sutil.BuildClients` has no GKE path and would fall
    through to the controller's own cluster, so no Job is created and the status is
    `Ready=Unknown`, reason `ProbeUnsupported`.
- Each probe has a short timeout (default 30s).

### Executor access check

New optional interface in `internal/executor` (not a method on `Executor`, so
existing and out-of-tree executors keep compiling):

```go
type ConnectorRequirer interface {
    // Requires declares the connector kind and the access needed per operation.
    Requires() AccessRequirement
}
// AWS executors list IAM actions (e.g. ec2: DescribeInstances, StopInstances, StartInstances).
// K8s executors list RBAC rules (e.g. workloadscaler: patch deployments/scale).
```

- AWS requirements are checked with one shared `iam:SimulatePrincipalPolicy` helper.
- K8s requirements are checked with one shared `SelfSubjectAccessReview` helper.
- Both **shutdown and wakeup** requirements must pass for an executor to be allowed.
  A failure names which operation is missing.
- Executors that do not implement `ConnectorRequirer` get no entry in
  `allowedExecutors` or `executorAccess`. As with every other case where access
  could not be verified, no entry means unknown, never denied.
- Only executors enabled in the runner (see `resolveEnabledExecutors`) are checked.
- Which connector kind each executor requires (e.g. ec2/rds/eks → CloudProvider;
  karpenter/workloadscaler → K8SCluster) is declared by the executor itself.

### AWS simulation specifics

- The simulating identity is always the identity the connector authenticates as,
  using the credentials from step 1, and it simulates itself:
  - With `assumeRoleArn`: the assumed role, in the target account (so cross-account
    targets work the same as same-account ones).
  - Without it, IRSA: the runner's own role, whose ARN is read from the
    `AWS_ROLE_ARN` environment variable injected by the IRSA webhook. Read it only
    when the effective credentials are the IRSA ones, never when static credentials
    are configured.
  - Without it, static credentials: the IAM user ARN from `sts:GetCallerIdentity`
    (no conversion needed).
  - If the ARN cannot be resolved (e.g. EKS Pod Identity, which does not set
    `AWS_ROLE_ARN`), step 2 is not possible; see below.
- Permissions needed on that identity, in the target account or wherever the
  connector's authentication points: `iam:SimulatePrincipalPolicy` and
  `iam:GetContextKeysForPrincipalPolicy`. Nothing new is required on the runner role
  when `assumeRoleArn` is used.
- If simulation is not possible (the identity lacks these permissions, or the
  principal cannot be resolved): step 2 is skipped. The connector stays `ready`,
  and `allowedExecutors` and `executorAccess` are left empty. No placeholder
  entries and no `allowed: false`, so a missing permission is never read as a
  denial. A genuine policy denial found by a successful simulation is still
  `allowed: false`, `reason: AccessDenied`.
- To keep this discoverable, the reconciler sets an `ExecutorAccessVerified`
  condition to `False` with reason `SimulationUnavailable` and a message telling the
  operator to grant the two permissions above to the connector's identity. This is a
  condition only, not an entry in either executor field.
- Simulation may not reflect every guardrail (e.g. org SCPs); to be confirmed
  against AWS docs. `verification` records how each answer was reached.
- `iam:GetContextKeysForPrincipalPolicy` is used to discover condition keys the
  identity's policies reference, so the simulation does not report a false denial
  for conditions it was not given values for. The exact handling is left to the
  builder.

### Status shape

Keep `ready`, `message`, `lastValidated`, `clusterType`. Add:

- `observedGeneration`
- `lastProbeTime`: when the last probe attempt finished, whatever its outcome
  (including `ProbeFailed`). Drives "is a probe due", so a probe that cannot run is
  retried at the resync interval instead of in a loop. `lastValidated` is the last
  probe that produced a verdict (ready or not) and is kept on `ProbeFailed`.
- `observedSecretVersion`: combined `resourceVersion` of the referenced Secret(s) at
  the last probe, so a Secret change triggers a re-probe without a status-less
  annotation write.
- `conditions[]` (`metav1.Condition`): `Ready` and `AuthValid`, with machine reasons
  such as `SecretNotFound`, `InvalidCredentials`, `AccountMismatch`,
  `ClusterUnreachable`, `ProviderNotReady`, `ProbeFailed`, `ProbeUnsupported`.
  `ExecutorAccessVerified` is `True` (`Verified`, or `NoExecutorsToVerify` when no
  executor required access), `False` (`SimulationUnavailable`, `ProbeError`,
  `Unverified` when executors were eligible but none could be checked) or `Unknown`
  (`AuthFailed`, `ProbeFailed`, `ProviderNotReady`, `ProbeUnsupported`). On
  `ProbeFailed`, `ProviderNotReady` and `ProbeUnsupported` the executor lists are
  cleared.
- `allowedExecutors []string`: executor types verified as allowed. Empty when
  `ready` is false or when access could not be verified.
- `executorAccess []{type, allowed, reason, message, verification}`: per-executor
  detail, only for executors whose access was actually checked. `type` matches
  `target.type`; `verification` is `PolicySimulation` or `AccessReview`; `reason`
  is a short code (`AccessDenied`, `ProbeError`).
- Condition `ExecutorAccessVerified` (see AWS simulation specifics).

```yaml
status:
  ready: true
  allowedExecutors: [ec2, eks]
  executorAccess:
    - {type: ec2, allowed: true, verification: PolicySimulation}
    - type: rds
      allowed: false
      reason: AccessDenied
      message: "rds:StopDBInstance denied (policy simulation)"
      verification: PolicySimulation
```

`ready` mirrors the `Ready` condition and means authentication works regardless of
actions. `lastValidated` is updated whenever a probe produces a verdict, success or
failure, and is kept when the probe could not run.

### Scheduling

- Default resync interval 15 minutes (probe Jobs are not free), configurable via a
  controller flag, with jitter to avoid a thundering herd.
- Cap concurrent probe Jobs (default 5).
- One Job per connector covers authentication and all executor checks.
- Probe Jobs: `backoffLimit: 0`, short `ttlSecondsAfterFinished`, labelled with
  connector kind/name; at most one in-flight probe per connector.
- A probe that cannot run (Job fails to start, pod unschedulable, termination
  message missing) sets `Ready=Unknown`, reason `ProbeFailed`, not `False`.

### Integration points

- `api/v1alpha1`: new status fields, printer columns, CRD regeneration
  (`make generate` and the docs generators).
- `internal/executor`: `ConnectorRequirer` interface and `AccessRequirement` types;
  each in-tree executor implements it.
- `internal/provider/setup.go`: register the connector reconciler and a status
  processor for the two kinds.
- `cmd/runner`: probe operation reusing `ConfigBuilder`, `pkg/awsutil`,
  `pkg/k8sutil`, plus the two shared checkers.
- Helm chart: controller ClusterRole gains create/get/list/watch/delete on `jobs`,
  get/list on `pods` in its namespace (verify against existing rules), and patch on
  connector `/status`. Connector docs gain the two optional simulate permissions for the connector's
  identity (target account). No runner ClusterRole change: the `SelfSubjectAccessReview`
  goes to the target cluster as the connector's identity, and the default
  `system:basic-user` role already allows it. Controller ClusterRole also needs `watch`
  on `secrets` (cluster-wide Secret watch) and `/status` on both connector kinds.
- Website docs: connector status, the required runner-role IAM permission, and
  troubleshooting.

## Decided by the user

- Probe must prove the configured authentication actually works, for both
  CloudProvider and K8SCluster.
- Probe identity must be the runner SA (IRSA is bound there).
- Status is informational only this milestone; no coupling to plans/exceptions.
- Connector status also reports which executors are allowed; executors define what
  connector and access they need.
- Generic mechanism using IAM policy simulation, including for EC2.
- Two-step validation: (1) authenticate as the connector specifies, including
  assuming the role if any; (2) only then simulate workload actions with those
  credentials (target account when assumed).
- If step 2 is not possible, the connector is still marked ready with no entries in
  `allowedExecutors` or `executorAccess`; docs encourage granting
  `iam:SimulatePrincipalPolicy` and `iam:GetContextKeysForPrincipalPolicy` to the
  connector's identity in the target account.

## Calls made by me (please check)

- Executors declare requirements (optional interface) instead of implementing probe
  methods.
- Both shutdown and wakeup must pass for `allowed: true`.
- Status field names: `allowedExecutors` and `executorAccess` with a `verification`
  field.
- When simulation is unavailable, an `ExecutorAccessVerified=False` condition
  (reason `SimulationUnavailable`) carries the hint, since operators have nowhere
  else to look when both executor fields are empty. Easy to drop if unwanted.
- Probe Jobs run in the controller's namespace, since the runner SA exists only there.
- Result returned via termination message, not the streaming channel.
- Default resync 15 min, concurrency cap 5, 30s probe timeout.
- Account-ID mismatch is a failure (`AccountMismatch`).
- An un-runnable probe yields `Ready=Unknown`, not `False`.

## Left to the builder

- Package layout and naming of the reconciler/processor and shared checkers.
- Exact `AccessRequirement` type and probe-result JSON schema.
- Watch/predicate details for Secret-triggered re-probes.
- Test strategy (unit with fake clients, envtest/kind for the Job flow). E2E tests
  must be requested from the user, not run automatically.

## Open items

- Confirm in AWS docs which guardrails (SCPs, permission boundaries, resource
  policies) the simulator does and does not evaluate.
- Confirm during planning that simulation accepts the `AWS_ROLE_ARN` role ARN as
  the policy source for the IRSA-without-`assumeRoleArn` case, and that the same
  role holds the simulate permissions there. No `iam:GetRole` is planned.
- GKE: resolved. Not probed (see Authentication check); revisit when
  `pkg/k8sutil.BuildClients` gains a GKE path.
- Confirm the exact connector kind each in-tree executor requires (e.g. whether the
  eks executor needs both AWS credentials and cluster access).
