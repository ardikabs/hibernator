# AWS Environment Executor Integration Design

**Date:** 2026-09-08

## Goal

Replace the Floci-named executor integration suite with a provider-neutral
`test/awsenv` suite. The suite provisions AWS-shaped resources, invokes the
real Hibernator executors, and verifies resource and restore-state transitions.
Floci is the primary environment; a future real-AWS backend must not require
rewriting executor lifecycle tests.

## Scope

The first implementation covers:

- Rename `test/floci` to `test/awsenv`.
- Rename its build tag, environment variables, documentation, and Make target.
- Preserve the existing EC2 mixed-state lifecycle test.
- Add provisioned RDS instance lifecycle and snapshot lifecycle cases.
- Add a provisioned EKS managed-node-group lifecycle case.
- Add validation cases for RDS and EKS.
- Adapt `test/kind` to consume the provider-neutral harness while continuing
  to use Floci as its primary AWS environment.
- Probe exact required APIs and explicitly skip lifecycle tests only when the
  selected AWS environment reports an operation as unsupported.

This design does not add a real-AWS provisioner, a full-chain RDS/EKS kind
scenario, or support for cloud resources outside existing AWS executors.

## Naming And Configuration

Provider-neutral names describe the test contract:

| Current | Replacement |
|---|---|
| `test/floci` | `test/awsenv` |
| build tag `floci` | build tag `awsenv` |
| `FLOCI_ENABLED` | `AWSENV_ENABLED` |
| `FLOCI_ENDPOINT` | `AWSENV_ENDPOINT_URL` |
| `make test-floci` | `make test-awsenv` |

`AWSENV_PROVIDER` selects the provisioning backend and initially accepts only
`floci`. The default is `floci` when `AWSENV_ENDPOINT_URL` is the default
`http://localhost:4566`. Unknown providers fail setup rather than silently
falling back.

Floci runtime assets remain provider-specific:

- `test/awsenv/environments/floci/compose.yml`
- `FLOCI_IMAGE` for an explicitly reviewed image override

The Floci image remains pinned by digest because it receives Docker socket
access.

## Architecture

### Environment

`harness.Setup(t)` returns an `Environment`:

```go
type Environment struct {
    Provider     string
    Endpoint     string
    Region       string
    Clients      Clients
    Provisioner  Provisioner
    Capabilities Capabilities
}
```

`Clients` contains AWS SDK clients used both for provisioning and independent
post-operation assertions. It starts with EC2, RDS, EKS, and STS clients.

### Provisioner

Executor tests request AWS-shaped fixtures through a provider-neutral
interface:

```go
type Provisioner interface {
    ProvisionEC2(t *testing.T, ctx context.Context, spec EC2FixtureSpec) EC2Fixture
    ProvisionRDS(t *testing.T, ctx context.Context, spec RDSFixtureSpec) RDSFixture
    ProvisionEKS(t *testing.T, ctx context.Context, spec EKSFixtureSpec) EKSFixture
}
```

The initial `FlociProvisioner` implements this interface with standard AWS SDK
calls. It registers bounded, reverse-order cleanup with `t.Cleanup` and fails
the test if cleanup cannot be confirmed.

A future real-AWS provisioner may implement the same interface. Executor tests
must not contain Floci container names, image names, endpoint paths, or
Floci-specific APIs.

### Capabilities

Service presence is insufficient. A capability represents the exact operation
set required by Hibernator:

```go
type Capability string

const (
    CapabilityEC2Lifecycle       Capability = "ec2.lifecycle"
    CapabilityRDSLifecycle       Capability = "rds.instance-lifecycle"
    CapabilityRDSSnapshot        Capability = "rds.instance-snapshot"
    CapabilityEKSNodeGroupUpdate Capability = "eks.nodegroup-update"
)
```

Capability probes run after fixture provisioning, because the most reliable
probe is an operation against a valid resource. A probe may return:

- supported: continue the lifecycle test;
- unsupported: call `t.Skipf` with provider, capability, AWS operation, and
  normalized API error;
- failed: fail the test because provisioning, authentication, networking,
  permissions, throttling, or an unexpected service error is not an
  unsupported capability.

Unsupported detection is conservative. It accepts explicit operation-not-found
or not-implemented responses (including protocol-level unknown-action errors).
It must not classify access denied, invalid state, invalid parameters,
timeouts, connection failures, or server errors as unsupported.

## Resource Provisioning

### EC2

Provision instances with `RunInstances`, unique names and run tags, then wait
for `running`. Cleanup calls `TerminateInstances` and confirms `terminated`.
The existing mixed-state case stops one fixture before invoking Hibernator.

### RDS

Provision a uniquely named PostgreSQL instance with `CreateDBInstance`, bounded
credentials, and the smallest Floci-compatible instance class. Wait until
`DescribeDBInstances` reports `available`.

Cleanup deletes snapshots created by the test (when the environment supports
snapshot deletion), then deletes the DB instance without a final snapshot and
confirms absence/deleted state. Cleanup remains bounded and failure-visible.

The lifecycle probe invokes `StopDBInstance` on the valid fixture. If Floci
reports that action as unsupported, the lifecycle and snapshot tests skip with
the exact capability reason. The provisioned fixture is still cleaned up.

### EKS

Provision a unique EKS cluster and managed node group using standard AWS SDK
calls. The initial scaling configuration is `{minSize: 1, desiredSize: 1,
maxSize: 2}`. Wait for control-plane and node-group status to become active.

The first executor lifecycle test sets `awaitCompletion.enabled=false`.
Hibernator's EKS shutdown otherwise connects to the returned Kubernetes API
and waits for nodes to disappear; the test's purpose is initially the AWS
managed-node-group scaling contract. The executor still calls
`DescribeCluster`, constructs its Kubernetes client, and invokes
`UpdateNodegroupConfig`, so the Floci EKS fixture must expose a usable cluster
endpoint.

Cleanup deletes the node group before deleting the cluster and confirms both
are absent. Unsupported create/update/delete operations are never treated as a
passing lifecycle test.

## Test Cases

### EC2

`TestEC2LifecyclePreservesInitialState`:

- provision two running instances;
- stop one before Hibernator shutdown;
- validate the executor specification;
- run shutdown and assert both restore entries;
- assert only the initially running instance recorded `wasRunning=true`;
- run wakeup;
- assert the initially running instance is running and the initially stopped
  instance remains stopped.

`TestEC2NoMatchIsNoop` and invalid-selector validation remain.

### RDS

`TestRDSInstanceLifecycle`:

- provision one available PostgreSQL instance;
- probe and require `rds.instance-lifecycle`;
- run executor validation and shutdown;
- assert the resource becomes stopped and restore state identifies the
  instance with `wasRunning=true`;
- run wakeup and assert the resource becomes available.

`TestRDSInstanceSnapshotLifecycle`:

- provision an instance;
- probe lifecycle and snapshot capabilities;
- run shutdown with `snapshotBeforeStop=true`;
- assert a snapshot ID is saved in restore data and returned by
  `DescribeDBSnapshots`;
- run wakeup and assert availability.

`TestRDSValidateRejectsEmptySelector` remains environment-independent.

RDS cluster lifecycle is deferred until instance lifecycle is proven because
it requires a separate fixture family and Floci's cluster snapshot behavior is
independently incomplete.

### EKS

`TestEKSNodeGroupLifecycle`:

- provision an active cluster and node group;
- probe and require `eks.nodegroup-update`;
- validate the executor specification;
- run shutdown with the explicit node-group name and await disabled;
- assert restore data captures `{min:1, desired:1, max:2, wasScaled:true}`;
- assert `DescribeNodegroup` reports `{min:0, desired:0, max:2}`;
- run wakeup;
- assert `DescribeNodegroup` returns `{min:1, desired:1, max:2}`.

`TestEKSValidateRejectsMissingClusterName` remains environment-independent.

## Error Handling And Isolation

- Every suite run has a unique run ID used in resource names and tags.
- Every AWS request has a per-request deadline; every waiter has an overall
  deadline and reports the last state and API error.
- Cleanup is scoped to fixture IDs captured from create responses. There is no
  account-wide delete or tag-only destructive cleanup.
- Cleanup executes even after capability skips because it is registered
  immediately after successful resource creation.
- Tagged suite invocation fails when `AWSENV_ENABLED=1` is absent.
- Provider setup fails on an unknown provider, invalid endpoint, failed STS
  readiness, or credentials/configuration errors.
- Tests do not run in parallel until the selected provider is proven safe for
  concurrent Docker-backed resource operations.

## kind Integration

`test/kind` remains the full control-plane suite and imports
`test/awsenv/harness`. Its test-only runner image receives
`AWS_ENDPOINT_URL` from `AWSENV_ENDPOINT_URL`.

The current in-cluster host route remains a Floci backend detail. Names in
generic kind code use `awsenv` where practical, while the environment adapter
owns Floci service names and compose paths. The existing EC2 full-chain smoke
continues to run after the rename.

RDS and EKS are executor-integration cases first. Full-chain kind scenarios
for those executors are a later step after their exact Floci capabilities pass.

## Verification

Required verification:

1. Pure validation tests pass without provisioning.
2. `make test-awsenv` provisions, tests, and cleans EC2.
3. RDS/EKS tests either pass lifecycle assertions or emit explicit,
   capability-specific skips; provisioning and cleanup must still pass.
4. Floci has no leaked test containers/resources after the suite.
5. `make test-kind` passes after the harness rename.
6. Default build and unit tests remain unaffected by the `awsenv` build tag.
7. No runtime/config/test reference to the old `test/floci`, `test-floci`, or
   `FLOCI_ENABLED` names remains, except historical planning documents.

## Future Real-AWS Backend

A real-AWS backend is deliberately low priority. When added, it must use the
same test cases and `Provisioner` contract. It may provision resources or bind
pre-existing sandbox fixtures, but that decision belongs inside the backend,
not executor lifecycle tests. Real-AWS execution must require explicit account,
region, and destructive-test opt-in guards.
