# Scaling Kubernetes Workloads

This guide covers how to scale down Kubernetes workloads (Deployments, StatefulSets, and custom resources) using the `workloadscaler` executor.

## Prerequisites

- A `K8SCluster` resource configured for the target cluster
- RBAC on the target cluster, see [Required Permissions](#required-permissions)
- For custom CRDs: the scale subresource must be enabled on the CRD

## Required Permissions

The identity the `K8SCluster` authenticates as needs these Kubernetes permissions. [Connector status](connectors.md#executor-access-on-kubernetes) checks them with `SelfSubjectAccessReview` and reports `allowed: false` for `workloadscaler` when one is missing.

| Operation | API group | Resource | Verbs |
|-----------|-----------|----------|-------|
| Shutdown | core | `namespaces` | `list` |
| Shutdown | `apps` | `deployments`, `statefulsets`, `replicasets` | `list` |
| Shutdown | `apps` | `deployments/scale`, `statefulsets/scale`, `replicasets/scale` | `get`, `update` |
| Wakeup | `apps` | `deployments/scale`, `statefulsets/scale`, `replicasets/scale` | `get`, `update` |

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: hibernator-workloadscaler
rules:
  - apiGroups: [""]
    resources: ["namespaces"]
    verbs: ["list"]
  - apiGroups: ["apps"]
    resources: ["deployments", "statefulsets", "replicasets"]
    verbs: ["list"]
  - apiGroups: ["apps"]
    resources: ["deployments/scale", "statefulsets/scale", "replicasets/scale"]
    verbs: ["get", "update"]
```

The access review is cluster-wide. An identity that only has namespaced RoleBindings shows `AccessDenied` even though it works in the namespaces it is bound in. If that is how you grant access, treat `AccessDenied` as expected.

!!! note "Custom resource kinds"
    Custom CRD workload kinds are not covered by the connector check. For each one you scale, also grant `list` on the resource and `get` and `update` on its `scale` subresource in its API group.

## Basic Setup

### 1. Create the K8SCluster Connector

For a remote cluster:

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: K8SCluster
metadata:
  name: eks-production
  namespace: hibernator-system
spec:
  providerRef:
    name: aws-production
  eks:
    name: production-cluster
    region: us-west-2
```

For a local cluster using kubeconfig:

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: K8SCluster
metadata:
  name: local
  namespace: hibernator-system
spec:
  k8s:
    kubeconfigRef:
      name: kubeconfig-secret
      namespace: hibernator-system
```

### 2. Create the HibernatePlan

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: workload-hibernate
  namespace: hibernator-system
spec:
  schedule:
    timezone: America/New_York
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
  execution:
    strategy:
      type: Sequential
  behavior:
    mode: Strict
  targets:
    - name: app-workloads
      type: workloadscaler
      connectorRef:
        kind: K8SCluster
        name: eks-production
      parameters:
        namespace:
          literals:
            - default
            - app-namespace
        includedGroups:
          - Deployment
        awaitCompletion:
          enabled: true
          timeout: "5m"
```

## Use Cases

### Scale Down Deployments in Specific Namespaces

The most common use case — scale all Deployments in a list of namespaces:

```yaml
targets:
  - name: app-deployments
    type: workloadscaler
    connectorRef:
      kind: K8SCluster
      name: eks-production
    parameters:
      namespace:
        literals:
          - frontend
          - backend
          - workers
      includedGroups:
        - Deployment
      awaitCompletion:
        enabled: true
```

### Scale Down Deployments and StatefulSets

Target multiple workload kinds:

```yaml
targets:
  - name: all-workloads
    type: workloadscaler
    connectorRef:
      kind: K8SCluster
      name: eks-production
    parameters:
      namespace:
        literals:
          - default
      includedGroups:
        - Deployment
        - StatefulSet
      awaitCompletion:
        enabled: true
```

### Filter Workloads by Labels

Only scale workloads matching specific labels:

```yaml
targets:
  - name: team-a-workloads
    type: workloadscaler
    connectorRef:
      kind: K8SCluster
      name: eks-production
    parameters:
      namespace:
        literals:
          - default
      workloadSelector:
        matchLabels:
          team: team-a
          hibernatable: "true"
      includedGroups:
        - Deployment
      awaitCompletion:
        enabled: true
```

### Discover Namespaces by Labels

Instead of listing namespaces explicitly, discover them using label selectors:

```yaml
targets:
  - name: dev-workloads
    type: workloadscaler
    connectorRef:
      kind: K8SCluster
      name: eks-production
    parameters:
      namespace:
        selector:
          environment: development
      includedGroups:
        - Deployment
      awaitCompletion:
        enabled: true
```

This discovers all namespaces with the label `environment=development` and scales their Deployments.

### Scale Custom CRDs (Argo Rollouts)

Any Kubernetes resource with a scale subresource can be targeted. Use the `group/version/resource` format:

```yaml
targets:
  - name: argo-rollouts
    type: workloadscaler
    connectorRef:
      kind: K8SCluster
      name: eks-production
    parameters:
      namespace:
        literals:
          - default
      includedGroups:
        - Deployment
        - argoproj.io/v1alpha1/rollouts
      awaitCompletion:
        enabled: true
```

### Multi-Tier Application (Staged Strategy)

Scale down tiers in order — frontend first, then backend, then workers:

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: tiered-hibernate
  namespace: hibernator-system
spec:
  schedule:
    timezone: Asia/Jakarta
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
  execution:
    strategy:
      type: Staged
      stages:
        - name: frontend
          parallel: true
          targets:
            - frontend-workloads
        - name: backend
          parallel: true
          targets:
            - backend-workloads
        - name: workers
          parallel: false
          targets:
            - worker-workloads
  targets:
    - name: frontend-workloads
      type: workloadscaler
      connectorRef:
        kind: K8SCluster
        name: eks-production
      parameters:
        namespace:
          literals: [frontend]
        includedGroups: [Deployment]
        awaitCompletion:
          enabled: true

    - name: backend-workloads
      type: workloadscaler
      connectorRef:
        kind: K8SCluster
        name: eks-production
      parameters:
        namespace:
          literals: [backend]
        includedGroups: [Deployment, StatefulSet]
        awaitCompletion:
          enabled: true

    - name: worker-workloads
      type: workloadscaler
      connectorRef:
        kind: K8SCluster
        name: eks-production
      parameters:
        namespace:
          literals: [workers]
        includedGroups: [Deployment]
        awaitCompletion:
          enabled: true
```

### Workloads + Infrastructure (DAG Strategy)

Scale down workloads first, then the underlying infrastructure:

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: full-stack-hibernate
  namespace: hibernator-system
spec:
  schedule:
    timezone: Asia/Jakarta
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
  execution:
    strategy:
      type: DAG
      maxConcurrency: 3
      dependencies:
        - from: app-workloads
          to: karpenter-pools
        - from: app-workloads
          to: eks-nodegroups
  targets:
    - name: app-workloads
      type: workloadscaler
      connectorRef:
        kind: K8SCluster
        name: eks-production
      parameters:
        namespace:
          literals: [default, app]
        includedGroups: [Deployment]
        awaitCompletion:
          enabled: true

    - name: karpenter-pools
      type: karpenter
      connectorRef:
        kind: K8SCluster
        name: eks-production
      parameters:
        nodePools: []
        awaitCompletion:
          enabled: true

    - name: eks-nodegroups
      type: eks
      connectorRef:
        kind: CloudProvider
        name: aws-production
      parameters:
        clusterName: production-cluster
        nodeGroups: []
        awaitCompletion:
          enabled: true
```

## What Happens During Hibernation

1. Target namespaces are resolved (from literal list or label selector)
2. Workloads are discovered in each namespace, filtered by `includedGroups` and `workloadSelector`
3. For each workload, the current replica count is read from the scale subresource
4. The replica count is saved to the restore ConfigMap
5. The scale subresource is updated to `replicas: 0`

## What Happens During Wakeup

1. Saved workload states are loaded from the restore ConfigMap
2. For each workload, the scale subresource is updated back to the original replica count
3. The workload controller (Deployment controller, StatefulSet controller, etc.) reconciles and creates pods

## Troubleshooting

### Workloads not scaling down

- Verify the namespace and label selectors match your workloads
- Check that the `K8SCluster` connector has the right RBAC permissions
- Confirm the `includedGroups` list includes the correct resource kinds

### Custom CRD not recognized

- Custom CRDs must use the `group/version/resource` format (e.g., `argoproj.io/v1alpha1/rollouts`)
- The CRD must have the scale subresource enabled in its definition
- Verify the runner ServiceAccount has RBAC for the custom resource's scale subresource

### Workloads not scaling back up

- Check the restore ConfigMap exists: `kubectl get cm hibernator-restore-{plan-name} -n hibernator-system`
- Verify the saved replica counts are correct in the ConfigMap
- If a workload was deleted and recreated during hibernation, the restore will fail (name mismatch)
