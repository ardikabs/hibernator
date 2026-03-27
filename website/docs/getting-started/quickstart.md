# Quick Start

This guide walks through creating a complete hibernation plan that shuts down a development environment during off-hours.

## 1. Create a CloudProvider

First, set up AWS credentials that Hibernator will use to manage resources:

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

```bash
kubectl apply -f cloudprovider.yaml
```

## 2. Create a K8SCluster Connector

If you need to manage Kubernetes-level resources (like Karpenter NodePools), create a K8SCluster connector:

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

```bash
kubectl apply -f k8scluster.yaml
```

## 3. Create a HibernatePlan

Now define a plan that hibernates resources every weeknight from 8 PM to 6 AM:

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
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]

  execution:
    strategy:
      type: DAG
      maxConcurrency: 3
      dependencies:
        - from: dev-karpenter
          to: dev-eks-nodegroups
        - from: dev-db
          to: dev-eks-nodegroups

  targets:
    - name: dev-db
      type: rds
      connectorRef:
        kind: CloudProvider
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
        nodeGroups: []

    - name: dev-karpenter
      type: karpenter
      connectorRef:
        kind: K8SCluster
        name: dev-eks
      parameters:
        nodePools: []

  behavior:
    mode: Strict
    retries: 3
```

```bash
kubectl apply -f hibernateplan.yaml
```

## 4. Monitor Execution

```bash
# Watch plan status
kubectl get hibernateplan dev-offhours -n hibernator-system -w

# Check execution details
kubectl get hibernateplan dev-offhours -n hibernator-system \
  -o jsonpath='{.status}' | jq

# View runner job logs
kubectl logs -n hibernator-system -l hibernator/plan=dev-offhours
```

### Understanding Plan Phases

| Phase | Meaning |
|-------|---------|
| `Active` | Plan is active and waiting for the next schedule window |
| `Hibernating` | Shutdown is in progress |
| `Hibernated` | All targets are hibernated |
| `WakingUp` | Wakeup is in progress |
| `Suspended` | Plan is manually suspended |
| `Error` | An error occurred during execution |

## What Happens Next?

- At **20:00 Asia/Jakarta**, the controller evaluates the schedule and begins shutdown
- Targets are executed following DAG order: `dev-karpenter` and `dev-db` first (parallel), then `dev-eks-nodegroups`
- Restore metadata is captured and persisted in ConfigMaps
- At **06:00 Asia/Jakarta**, the controller triggers wakeup in reverse order
- Resources are restored using the saved metadata

## Next Steps

- Learn about [Execution Strategies](../user-guides/execution-strategies.md) for more orchestration options
- Set up [Schedule Exceptions](../user-guides/schedule-exceptions.md) for maintenance windows
- Read the [Concepts](../concepts/index.md) section for deeper understanding
