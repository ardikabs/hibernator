<!--
RFC: 0004
Title: Scale Subresource Executor for Workload Downscaling
Author: Hibernator Team
Status: Implemented
Date: 2026-01-31
-->

# RFC 0004 — Scale Subresource Executor for Workload Downscaling

**Keywords:** Executors, Kubernetes, Scale-Subresource, Downscale, Restore-Metadata, RBAC

**Status:** Implemented ✅

## Summary

Introduce a generic executor that downscales Kubernetes workloads by using the `<resource>/scale` subresource, equivalent to `kubectl scale <type>/<name> --replicas=0`. The executor targets any workload kind that supports the scale subresource, using explicit scoping and selectors provided in the `HibernatePlan` target parameters. It captures current replica counts as restore data and re-applies them during wake-up.

**Implementation status:** Completed in `internal/executor/workloadscaler/` with validation in `pkg/executorparams` and webhook target type support.

## Motivation

Teams often need to hibernate applications inside clusters, not only infrastructure. A workload-level downscaler provides a universal, Kubernetes-native mechanism that:

- Works across different workload types as long as they expose `<resource>/scale`.
- Avoids per-kind custom logic by using the standard scale API.
- Captures restore data to safely revert to the previous replica counts.
- Enables explicit scope control via namespaces and label selectors.

## Goals

- Provide a generic scale-based executor for workloads that support the scale subresource.
- Require explicit targeting (no automatic discovery).
- Require explicit namespace scope (literal list or namespace selector).
- Preserve and restore replica counts for wake-up.
- Document RBAC requirements to safely grant scale permissions only to included kinds and namespaces.

## Non-Goals

- Automatically discover all scalable workloads in the cluster.
- Provide per-kind special handling beyond the generic scale subresource.
- Enforce any maximum namespace limit (operator relies on user responsibility).

## Proposal

Add a new executor named `workloadscaler` that performs:

- **Shutdown**: For all matched workloads, read their scale subresource, store the current `spec.replicas` value, and set replicas to `0`.
- **WakeUp**: Re-apply stored replica counts by updating the scale subresource for each workload.

The executor is generic and does not depend on workload types beyond the scale subresource contract.

### Discovery Model (Explicit Only)

Target discovery is explicit and scoped by the user:

- **Kinds** are constrained via `includedGroups`.
- **Namespaces** must be specified via either a literal list or a namespace label selector.
- **Workload selection** can be filtered by a label selector.

No runtime enumeration of all scalable kinds is performed beyond the explicit configuration.

## API / CRD Updates

### Target type

A new `type` value will be introduced for the executor (name to be finalized). Example:

```yaml
targets:
  - name: downscaler
    type: workloadscaler
    connectorRef:
      kind: K8SCluster
      name: eks-production
    parameters:
      includedGroups:
        - Deployment
        - ReplicaSet
      namespace:
        literals:
          - app-a
          - app-b
      workloadSelector:
        matchExpressions:
          - key: app.kubernetes.io/part-of
            operator: In
            values: ["payments"]
```

### Parameters

```yaml
parameters:
  includedGroups:               # Optional. Defaults to [Deployment].
    - Deployment                # Built-in Kubernetes resource
    - StatefulSet              # Built-in Kubernetes resource
    - argoproj.io/v1alpha1/rollouts  # Custom CRD (group/version/resource format)
  namespace:                    # Required. Must choose exactly one.
    literals:                   # Literal namespace list (preferred).
      - team-a
      - team-b
    # selector:                 # Label selector for namespaces (mutually exclusive).
    #   environment: staging
  workloadSelector:             # Optional. Standard LabelSelector for workloads.
    matchLabels:
      app: api
    # matchExpressions:
    #   - key: tier
    #     operator: In
    #     values: ["backend", "worker"]
```

**Rules:**

- If `includedGroups` is omitted, it defaults to `Deployment`.
- `namespace.literals` and `namespace.selector` are **mutually exclusive**; if both are set, the literal list is used and validation should reject the selector.
- No maximum namespace limit is enforced; users are responsible for scoping appropriately.

#### Supported Kind Formats

The `includedGroups` array supports two formats:

##### 1. Built-in Kubernetes Resources (Hardcoded Mapping)

Simple names that map to well-known Kubernetes API groups:

```yaml
includedGroups:
  - Deployment      # → apps/v1/deployments
  - StatefulSet     # → apps/v1/statefulsets
  - ReplicaSet      # → apps/v1/replicasets
```

##### 2. Custom CRDs (Dynamic Parsing)

Explicit CRD specification using `group/version/resource` format:

```yaml
includedGroups:
  - argoproj.io/v1alpha1/rollouts           # ArgoCD Rollouts
  - helm.fluxcd.io/v2beta1/helmreleases     # Flux HelmRelease
  - example.com/v1/customscalables          # Custom CRD
```

The executor dynamically parses the CRD format and resolves the API group, version, and resource name without requiring code changes. This enables extensibility for any custom CRD that supports the scale subresource.

**Parsing Rules:**

- Format: `group/version/resource`
- Example: `argoproj.io/v1alpha1/rollouts`
  - Group: `argoproj.io`
  - Version: `v1alpha1`
  - Resource: `rollouts`
- All parts (group, version, resource) must be non-empty
- If a kind is not found in the hardcoded map, it's parsed as CRD format
- Invalid format returns a clear error message

## Restore Data

Restore data is stored per workload identity and includes the previous replica count:

```json
{
  "items": [
    {
      "group": "apps",
      "kind": "Deployment",
      "namespace": "team-a",
      "name": "api",
      "replicas": 3
    }
  ]
}
```

- Executor writes restore data during shutdown and uses it during wake-up.
- If a workload no longer exists during wake-up, behavior follows plan `behavior.mode` (Strict vs BestEffort).

## RBAC & Security

The scale executor requires permissions for the selected kinds and namespaces:

- **Read**: `get`, `list`, `watch` on the workload resources (for discovery and metadata).
- **Scale**: `get`, `update`, `patch` on the scale subresource of each included kind.

Example (for Deployments and StatefulSets):

```yaml
- apiGroups: ["apps"]
  resources: ["deployments", "statefulsets"]
  verbs: ["get", "list", "watch"]
- apiGroups: ["apps"]
  resources: ["deployments/scale", "statefulsets/scale"]
  verbs: ["get", "update", "patch"]
```

RBAC must be updated by operators whenever new kinds are added to `includedGroups`.

## Error Handling & Behavior

- If a workload does not expose the scale subresource, executor treats this as:
  - **Strict**: error and stop.
  - **BestEffort**: warning and skip.
- If scale operations fail for any item, executor records the failure in status and follows plan retry behavior.

## Example Configurations

### Literal namespace list

```yaml
targets:
  - name: downscaler
    type: workloadscaler
    connectorRef:
      kind: K8SCluster
      name: eks-production
    parameters:
      includedGroups:
        - Deployment
        - StatefulSet
      namespace:
        literals:
          - app-a
          - app-b
      workloadSelector:
        matchLabels:
          app.kubernetes.io/part-of: payments
```

### Namespace label selector

```yaml
targets:
  - name: downscaler
    type: workloadscaler
    connectorRef:
      kind: K8SCluster
      name: eks-production
    parameters:
      includedGroups:
        - Deployment
        - StatefulSet
      namespace:
        selector:
          environment: staging
      workloadSelector:
        matchLabels:
          app.kubernetes.io/part-of: payments
```

## Alternatives Considered

- **Dynamic discovery of all scalable workloads**: rejected due to least-privilege concerns and unbounded scope.
- **Per-kind executor implementations**: rejected in favor of a generic scale subresource approach.

## Implementation Plan (High-Level)

1. Define executor parameter types and validation rules (new entry in `pkg/executorparams`).
2. Implement executor using the Kubernetes scale subresource via dynamic client.
3. Register executor in runner factory.
4. Update webhook target validation and documentation.
5. Add unit tests for validation and executor behavior.

## Unresolved Questions

- None.
