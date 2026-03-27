# Hibernating Karpenter NodePools

This guide covers how to hibernate Karpenter-managed NodePools using the `karpenter` executor.

## Prerequisites

- A `K8SCluster` resource configured for the target cluster
- Karpenter v1 (`karpenter.sh/v1`) installed on the target cluster
- RBAC: `karpenter.sh nodepools` (get, list, delete, create), `v1 nodes` (list, get)

## Basic Setup

### 1. Create the K8SCluster Connector

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

### 2. Create the HibernatePlan

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: karpenter-hibernate
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
    - name: karpenter-pools
      type: karpenter
      connectorRef:
        kind: K8SCluster
        name: eks-production
      parameters:
        nodePools:
          - default
          - gpu-pool
        awaitCompletion:
          enabled: true
          timeout: "5m"
```

## Use Cases

### Hibernate All NodePools

Leave `nodePools` empty to discover and hibernate every NodePool:

```yaml
targets:
  - name: all-karpenter
    type: karpenter
    connectorRef:
      kind: K8SCluster
      name: eks-production
    parameters:
      nodePools: []  # discovers all NodePools
      awaitCompletion:
        enabled: true
```

### Hibernate Specific NodePools

Target only named pools:

```yaml
targets:
  - name: dev-pools
    type: karpenter
    connectorRef:
      kind: K8SCluster
      name: eks-dev
    parameters:
      nodePools:
        - batch-processing
        - dev-workloads
      awaitCompletion:
        enabled: true
        timeout: "10m"
```

### Protect Critical NodePools

To hibernate most pools while keeping a critical one running, list only the non-critical pools explicitly:

```yaml
targets:
  - name: non-critical-pools
    type: karpenter
    connectorRef:
      kind: K8SCluster
      name: eks-production
    parameters:
      nodePools:
        - batch-workers
        - dev-workloads
        # "monitoring" pool is NOT listed — stays running
      awaitCompletion:
        enabled: true
```

### Combined EKS + Karpenter with Dependencies

A common pattern is to hibernate Karpenter pools before EKS managed node groups to prevent Karpenter from rescheduling pods onto managed nodes:

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: full-cluster-hibernate
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
      dependencies:
        - from: karpenter-pools
          to: eks-nodegroups
  targets:
    - name: karpenter-pools
      type: karpenter
      connectorRef:
        kind: K8SCluster
        name: eks-production
      parameters:
        nodePools: []
        awaitCompletion:
          enabled: true
          timeout: "5m"

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
          timeout: "10m"
```

## What Happens During Hibernation

1. The executor retrieves the full NodePool spec (template, limits, disruption budget, labels)
2. The NodePool resource is deleted from the cluster
3. Karpenter detects the deleted pool and begins draining nodes managed by that pool
4. Nodes are cordoned, pods are evicted, and underlying EC2 instances are terminated
5. The complete NodePool definition is stored in restore data for exact reconstruction

## What Happens During Wakeup

1. The executor recreates each NodePool with the exact spec and labels from the restore data
2. Karpenter detects the new pool and begins provisioning nodes based on pending pod requirements
3. New nodes register with the cluster and pods are scheduled

## Troubleshooting

### Nodes not draining within timeout

- Check for Pod Disruption Budgets blocking eviction
- Verify Karpenter's disruption budget settings on the NodePool
- Increase timeout: `awaitCompletion.timeout: "15m"`
- Inspect Karpenter controller logs for eviction errors

### NodePool recreation fails

- Check if a NodePool with the same name already exists
- Verify RBAC grants `create` permission for `karpenter.sh nodepools`
- Review Karpenter webhook logs for admission errors

### Wrong Karpenter API version

- This executor uses `karpenter.sh/v1`. If your cluster runs an older Karpenter version using `v1beta1`, the executor may fail to discover or recreate pools.
