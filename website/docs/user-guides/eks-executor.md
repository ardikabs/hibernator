# Hibernating EKS Node Groups

This guide covers how to hibernate AWS EKS managed node groups using the `eks` executor.

## Prerequisites

- A `CloudProvider` resource configured for your AWS account
- IAM permissions: `eks:ListNodegroups`, `eks:DescribeNodegroup`, `eks:UpdateNodegroupConfig`
- The EKS cluster must have at least one managed node group

## Basic Setup

### 1. Create the CloudProvider

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: CloudProvider
metadata:
  name: aws-production
  namespace: hibernator-system
spec:
  type: aws
  aws:
    accountId: "123456789012"
    region: us-west-2
    assumeRoleArn: arn:aws:iam::123456789012:role/HibernatorRole
    auth:
      serviceAccount: {}  # IRSA-based authentication (recommended)
```

### 2. Create the HibernatePlan

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: eks-hibernate
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
    - name: eks-nodegroups
      type: eks
      connectorRef:
        kind: CloudProvider
        name: aws-production
      parameters:
        clusterName: production-cluster
        nodeGroups:
          - name: app-nodes
          - name: worker-nodes
        awaitCompletion:
          enabled: true
          timeout: "10m"
```

## Use Cases

### Hibernate All Node Groups in a Cluster

Leave `nodeGroups` empty to target every managed node group in the cluster:

```yaml
targets:
  - name: all-nodegroups
    type: eks
    connectorRef:
      kind: CloudProvider
      name: aws-production
    parameters:
      clusterName: production-cluster
      nodeGroups: []  # discovers all node groups
      awaitCompletion:
        enabled: true
```

### Hibernate Specific Node Groups

Target only certain node groups by name:

```yaml
targets:
  - name: dev-nodegroups
    type: eks
    connectorRef:
      kind: CloudProvider
      name: aws-production
    parameters:
      clusterName: dev-cluster
      nodeGroups:
        - name: app-tier
        - name: worker-tier
      awaitCompletion:
        enabled: true
        timeout: "15m"
```

### Hibernate Multiple EKS Clusters

Use multiple targets within the same plan:

```yaml
targets:
  - name: staging-eks
    type: eks
    connectorRef:
      kind: CloudProvider
      name: aws-staging
    parameters:
      clusterName: staging-cluster
      nodeGroups: []

  - name: dev-eks
    type: eks
    connectorRef:
      kind: CloudProvider
      name: aws-dev
    parameters:
      clusterName: dev-cluster
      nodeGroups: []
```

### EKS with Karpenter (DAG Strategy)

When a cluster uses both managed node groups and Karpenter, shut down Karpenter pools first to avoid rescheduling onto managed nodes:

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: full-eks-hibernate
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
          to: managed-nodegroups  # Karpenter first, then node groups
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

    - name: managed-nodegroups
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

1. Node groups are scaled to `minSize=0`, `desiredSize=0` (maxSize stays unchanged)
2. AWS begins terminating nodes in the node group
3. Pods running on those nodes are evicted
4. The cluster API server remains available throughout

## What Happens During Wakeup

1. Node groups are restored to their original `desiredSize`, `minSize`, and `maxSize`
2. AWS provisions new nodes matching the node group configuration
3. The Kubernetes scheduler places pods onto the new nodes

## Troubleshooting

### Nodes not scaling down

- Verify the IAM role has `eks:UpdateNodegroupConfig` permission
- Check for Pod Disruption Budgets that prevent eviction
- Review runner logs: `kubectl logs -l hibernator.ardikabs.com/plan=eks-hibernate -n hibernator-system`

### Timeout during await

- EKS node group operations can take time depending on instance count
- Increase the timeout: `awaitCompletion.timeout: "20m"`
- Check the AWS Console for node group update status

### Node group not found

- Ensure the `clusterName` matches the actual EKS cluster name
- Verify node group names match (case-sensitive)
- Confirm the CloudProvider region is correct
