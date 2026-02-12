# Hibernator Usage Guide

This guide provides instructions for installing, configuring, and monitoring the Hibernator Operator.

## Quick Start

### Prerequisites

- Kubernetes 1.34+ cluster
- Go 1.24+ (for development)
- AWS credentials with appropriate IAM permissions for target resources

### Installation

#### **Option 1: Using Helm (Recommended)**

```bash
# Add Hibernator chart repository
helm repo add hibernator https://your-registry/charts
helm repo update

# Install with default values
helm install hibernator hibernator/hibernator -n hibernator-system --create-namespace
```

#### **Option 2: Using kubectl**

```bash
# Apply CRDs
kubectl apply -f config/crd/bases/

# Deploy the operator
kubectl apply -f config/manager/manager.yaml

# Apply RBAC
kubectl apply -f config/rbac/
```

### Create Your First HibernatePlan

**Basic example:**

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: dev-offhours
  namespace: hibernator-system
spec:
  schedule:
    timezone: "Asia/Jakarta"
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday"]

  execution:
    strategy:
      type: DAG
      maxConcurrency: 3
      dependencies:
        - from: dev-karpenter
          to: dev-eks-nodegroups  # Karpenter first, then managed node groups
        - from: dev-db
          to: dev-eks-nodegroups  # Shutdown cluster after DB

  targets:
    - name: dev-db
      type: rds
      connectorRef:
        name: aws-dev
      parameters:
        snapshotBeforeStop: true

    - name: dev-eks-nodegroups
      type: eks
      connectorRef:
        kind: CloudProvider
        name: aws-dev
      parameters:
        clusterName: dev-cluster
        nodeGroups: []  # empty means all node groups

    - name: dev-karpenter
      type: karpenter
      connectorRef:
        kind: K8SCluster
        name: dev-cluster
      parameters:
        nodePools: []  # empty means all NodePools
```

### Monitor Execution

```bash
# Watch plan status
kubectl get hibernateplan dev-offhours -n hibernator-system -w

# Check execution details
kubectl get hibernateplan dev-offhours -n hibernator-system -o jsonpath='{.status.executions[*]}' | jq

# View runner job logs
kubectl logs -n hibernator-system -l hibernator/plan=dev-offhours
```

## Configuration

### CloudProvider Connector (AWS)

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: CloudProvider
metadata:
  name: aws-dev
  namespace: hibernator-system
spec:
  type: aws
  aws:
    accountId: "123456789012"
    region: ap-southeast-3
    assumeRoleArn: arn:aws:iam::123456789012:role/hibernator-runner
    auth:
      serviceAccount: {}
```

### K8SCluster Connector

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: K8SCluster
metadata:
  name: dev-eks
  namespace: hibernator-system
spec:
  providerRef:
    name: aws-dev
    namespace: hibernator-system
  eks:
    name: dev-cluster
    region: ap-southeast-3
```
