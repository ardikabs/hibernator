# Connector Status and Executor Access

`CloudProvider` and `K8SCluster` resources report whether their authentication works and which executors they are allowed to run. This guide explains what the status means, how it is produced, and how to fix a connector that is not ready.

For how to define connectors, see [Connectors](../concepts/connectors.md).

!!! note
    Connector status is informational in this release. It does not pause, hold or fail any `HibernatePlan` or `ScheduleException`. A connector that is not ready, or an executor that is not allowed, shows up in status only.

## Reading the Status

```bash
kubectl get cloudproviders,k8sclusters -n hibernator-system
```

```
NAME                                           TYPE   READY   AGE
cloudprovider.hibernator.ardikabs.com/aws-prod aws    True    3d

NAME                                          TYPE   READY     AGE
k8scluster.hibernator.ardikabs.com/prod-eks   eks    True      3d
k8scluster.hibernator.ardikabs.com/prod-gke   gke    Unknown   3d
```

`READY` is the status of the `Ready` condition: `True`, `False` (a verdict says it does not work), `Unknown` (no verdict: the probe failed, the provider state is unknown, or the cluster type is not probed), or empty (not probed yet). Add `-o wide` for the `REASON` column.

Full status of a `CloudProvider`:

```yaml
status:
  ready: true
  message: ""
  observedGeneration: 2
  lastValidated: "2026-11-19T08:15:02Z"
  lastProbeTime: "2026-11-19T08:15:02Z"
  conditions:
    - type: Ready
      status: "True"
      reason: Ready
    - type: AuthValid
      status: "True"
      reason: Ready
    - type: ExecutorAccessVerified
      status: "True"
      reason: Verified
  allowedExecutors: [ec2, eks]
  executorAccess:
    - type: ec2
      allowed: true
      verification: PolicySimulation
    - type: eks
      allowed: true
      verification: PolicySimulation
    - type: rds
      allowed: false
      reason: AccessDenied
      message: "rds:StopDBInstance denied for shutdown (policy simulation)"
      verification: PolicySimulation
```

| Field | Meaning |
|-------|---------|
| `ready` | Authentication works. It says nothing about which actions are permitted. |
| `message` | Human-readable reason when `ready` is false. |
| `lastValidated` | When the last probe that produced a verdict (ready or not) finished. Kept when a later probe fails. |
| `lastProbeTime` | When the last probe attempt finished, including attempts that could not run. Drives when the next probe is due. |
| `observedGeneration` | The `metadata.generation` the status was computed for. |
| `conditions` | `Ready`, `AuthValid` and `ExecutorAccessVerified`, each with a machine-readable `reason`. |
| `allowedExecutors` | Executor types verified as allowed. Empty whenever `ready` is false. |
| `executorAccess` | Per-executor detail: `type` (matches `target.type`), `allowed`, `reason`, `message`, and `verification` (`PolicySimulation` for AWS, `AccessReview` for Kubernetes). |
| `clusterType` | `K8SCluster` only. `eks`, `gke` or `k8s`, derived from the spec. |

An executor listed in `executorAccess` was actually checked. An executor with no entry is unknown, never denied.

`Ready=Unknown` with reason `ProbeFailed` means the probe Job could not run or produced no result. `ready` is then false, `allowedExecutors` and `executorAccess` are cleared, `ExecutorAccessVerified` is `Unknown`, and `lastValidated` keeps the time of the last verdict. Check the Job and pod in the connector's namespace (labels `hibernator.ardikabs.com/connector-*`).

## How the Probe Works

The controller does not test credentials itself. IRSA is bound to the runner ServiceAccount, so a check from the controller identity would not match what an execution sees. Instead the controller creates a short-lived Job in the connector's namespace, using the runner image and the runner ServiceAccount, and reads the result from the pod termination message.

Each probe has two steps:

1. **Authentication.** Decides `ready`.
    - `CloudProvider` (aws): build the AWS config as a runner would, including `assumeRoleArn`, call `sts:GetCallerIdentity`, and require that the account equals `spec.aws.accountId`.
    - `K8SCluster`: build clients for the configured type and call the server version endpoint. For an `eks` cluster the referenced `CloudProvider` must itself be ready. `gke` clusters are not probed, see [Known Limits](#known-limits).
2. **Executor access.** Runs only if step 1 passed. Using the same credentials, it checks the shutdown and wakeup requirements of each executor that targets this connector kind. Both must pass for `allowed: true`; a denial names the missing operation.

A connector is probed when it is new, when its spec changes, when a referenced Secret changes, and then every 15 minutes (with jitter). A probe that cannot run is retried at the same interval, not in a loop. A verdict is recorded for the spec generation and Secret version the Job was created with, so a change made while a probe is running triggers another probe.

The probe Job's `activeDeadlineSeconds` is the probe timeout plus 90 seconds, to allow for image pulls on cold nodes.

Controller flags (also exposed in the Helm chart under `operator.connector`):

| Flag | Default | Chart value |
|------|---------|-------------|
| `--connector-resync-interval` | `15m` | `operator.connector.resyncInterval` |
| `--connector-max-concurrent-probes` | `5` | `operator.connector.maxConcurrentProbes` |
| `--connector-probe-timeout` | `30s` | `operator.connector.probeTimeout` |

The Job runs with the runner ServiceAccount, which already can read `CloudProvider`, `K8SCluster` and Secrets. No extra runner RBAC is needed. The Kubernetes access check sends `SelfSubjectAccessReview` to the target cluster as the connector's identity; the default `system:basic-user` ClusterRole grants it to every authenticated identity.

A probe Job runs in the namespace of the connector it probes, the same namespace a HibernatePlan referencing that connector runs its runner Jobs in. The runner ServiceAccount must exist there, as it must for plan execution. The Job is owned by the connector, so deleting the connector garbage collects its probe Jobs.

## Upgrading

The controller now needs `watch` on `secrets` and `get`, `update` and `patch` on `cloudproviders/status` and `k8sclusters/status`. The Helm chart and `config/rbac` include them. A custom ClusterRole that lacks them stops the whole manager from starting, not only connector probing. Adding `watch` on `secrets` also lets the existing cached Secret reads in notifications sync.

## Executor Access on AWS

For `CloudProvider`, access is checked with IAM policy simulation (`iam:SimulatePrincipalPolicy`), run with the credentials the connector authenticates as. The identity simulates itself:

| Connector setup | Identity that is simulated and needs the permissions |
|-----------------|-------------------------------------------------------|
| `assumeRoleArn` set | The assumed role, in the target account |
| No `assumeRoleArn`, IRSA | The runner IRSA role (from `AWS_ROLE_ARN`) |
| No `assumeRoleArn`, static keys | The IAM user |

Nothing new is required on the runner role when `assumeRoleArn` is used. On the identity above, grant one optional permission:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": "iam:SimulatePrincipalPolicy",
      "Resource": "*"
    }
  ]
}
```

The probe always supplies the condition values it knows (`aws:RequestedRegion`, `aws:PrincipalAccount`, `aws:PrincipalArn`, `aws:SecureTransport`), so conditions in the identity's policies and in Organizations SCPs, such as a region restriction, are evaluated against the connector's region.

Without these permissions the connector is still `ready`. `allowedExecutors` and `executorAccess` stay empty, and the `ExecutorAccessVerified` condition is `False` with reason `SimulationUnavailable` and a message naming the permission. `SimulationUnavailable` is reported only when the first IAM call is denied; an error on a later call is `ProbeError`. A missing permission is never reported as a denial.

Two rules for reading results:

- No entry means unknown, never denied.
- If a simulated decision depends on condition keys the probe could not supply, the executor is treated as unverified and gets no entry, instead of being reported denied.
- If some executors got no entry, the `ExecutorAccessVerified` message says how many of the eligible executors were verified. If none did, the condition is `False` with reason `Unverified`.

A real policy denial found by a successful simulation shows as `allowed: false`, `reason: AccessDenied`.

## Executor Access on Kubernetes

For `K8SCluster`, access is checked with `SelfSubjectAccessReview`, using the identity the connector authenticates as. Each rule the executor declares (for example `get` and `update` on `deployments/scale` for `workloadscaler`) is reviewed once.

A review call that fails, or returns an evaluation error without a denial, leaves the executor with no entry.

The review is cluster-wide, with no namespace. An identity that only has namespaced RoleBindings shows `AccessDenied` even though it may work in the namespaces it is bound in. If that is how you grant access, treat `AccessDenied` here as expected.

## Known Limits

- Per-target resource scoping is not checked. The simulation uses `*` as the resource, so a role limited to specific resource ARNs can pass and still fail on a given target.
- `workloadscaler` targets that use custom CRD kinds (group/version/resource) are not covered by the declared rules. `allowed` is not a guarantee for them.
- The `eks` executor waits for node deletion, which needs in-cluster RBAC (`nodes` list through the EKS access entry). An IAM-only check cannot see that.
- EKS Pod Identity is not supported for executor access verification. It does not set `AWS_ROLE_ARN`, so the connector reports `SimulationUnavailable`.
- Static credentials that include a session token are also reported as not verifiable, because the caller is an assumed-role session rather than an IAM user or role.
- Simulation may not reflect organization SCPs or other guardrails outside the identity's own policies.
- `gke` and `cloudsql` executors are not probed. They never appear in `executorAccess`.
- `K8SCluster` resources with `spec.gke` are not probed: no Job runs, `ready` is false, and all three conditions are `Unknown` with reason `ProbeUnsupported`.
- `rds` and `eks` declare the union of all their modes. `rds` requires the cluster, instance and snapshot actions, so a role limited to instances, or one without snapshot permissions because snapshots are skipped, shows `AccessDenied`.

## Security Considerations

- Probes run as the runner ServiceAccount, which can read Secrets cluster-wide.
- A connector may reference a Secret or kubeconfig in another namespace.
- Anyone who can create a `CloudProvider` or `K8SCluster` makes the controller use the referenced credentials, and can read the resulting status.
- Kubeconfigs are used as given, including any credential plugins or file references they contain.

Restrict who can create and edit connectors accordingly.

## Troubleshooting

Reasons appear in `status.conditions[*].reason` and in `executorAccess[*].reason`.

| Reason | Where | Cause | What to do |
|--------|-------|-------|------------|
| `InvalidCredentials` | `Ready`, `AuthValid` | Credentials are incomplete, the AWS config could not be built, or `sts:GetCallerIdentity` failed (bad keys, role cannot be assumed, IRSA not working). | Read `status.message`. Check the Secret keys, the IRSA annotation on the runner ServiceAccount, and the trust policy of `assumeRoleArn`. |
| `AccountMismatch` | `Ready`, `AuthValid` | The credentials authenticate to a different account than `spec.aws.accountId`. | Fix `accountId` or the credentials / `assumeRoleArn`. |
| `SecretNotFound` | `Ready`, `AuthValid` | The referenced Secret does not exist. | Create the Secret or fix the reference. A Secret change triggers a new probe. |
| `ClusterUnreachable` | `Ready`, `AuthValid` | Clients could not be built or the server version call failed. | Check the kubeconfig or EKS cluster name/region, network reachability from the controller namespace, and token validity. |
| `ProviderNotReady` | `Ready`, `AuthValid`, `ExecutorAccessVerified` on `K8SCluster` | An `eks` cluster references a `CloudProvider` that is missing or not ready. No probe ran. `Ready` is `False` when the provider is missing or failed, `Unknown` when its own state is unknown; the other two are `Unknown` and the executor lists are cleared. | Fix the `CloudProvider` first; the cluster is re-probed once it is ready. |
| `ProbeFailed` | `Ready`, `AuthValid`, `ExecutorAccessVerified` (status `Unknown`) | The probe Job could not run or returned no result (image pull, unschedulable pod, timeout, missing termination message). The executor lists are cleared. | Inspect the probe Job and pod in the connector's namespace. The probe is retried at the resync interval. |
| `ProbeUnsupported` | `Ready`, `AuthValid`, `ExecutorAccessVerified` (status `Unknown`) on `K8SCluster` | The cluster uses `spec.gke`, which is not probed yet. | Nothing to do; the status does not reflect whether the connector works. |
| `AccessDenied` | `executorAccess` | Simulation or access review found a denied action. The message lists the actions and the operation (shutdown or wakeup). | Grant the listed actions to the identity, or ignore it if the executor is not used with this connector. |
| `SimulationUnavailable` | `ExecutorAccessVerified` | The identity lacks the simulate permissions, or its ARN cannot be resolved (Pod Identity, session-token credentials). | Grant `iam:SimulatePrincipalPolicy`, see above. The connector is otherwise unaffected. |
| `ProbeError` | `ExecutorAccessVerified` | Authentication passed but the access check failed for another reason (for example IAM throttling). | Read the message; it clears on the next successful probe. |
| `Unverified` | `ExecutorAccessVerified` (status `False`) | Executors require access, but none could be verified: missing condition-key values on AWS, or failed reviews on Kubernetes. | Read the message. On AWS, check which condition keys the identity's policies use. On Kubernetes, check that the target cluster serves `SelfSubjectAccessReview`. |
| `NoExecutorsToVerify` | `ExecutorAccessVerified` (status `True`) | No executor declares access requirements for this connector kind. | Nothing to do. |

`ExecutorAccessVerified` also uses `AuthFailed` (status `Unknown`) when authentication failed, since access cannot be checked, and `Verified` (status `True`) when the check completed.
